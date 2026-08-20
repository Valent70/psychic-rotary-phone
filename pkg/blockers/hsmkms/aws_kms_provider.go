// AWSKMSProvider is a real, production-usable implementation of
// keys.KeyProvider backed by the AWS KMS Sign and GetPublicKey APIs,
// authenticated with a hand-rolled AWS Signature Version 4 (SigV4)
// request signer. This repo permits zero external Go dependencies, so
// there is no AWS SDK to import -- everything here is net/http,
// crypto/hmac, crypto/sha256, and the rest of the standard library.
//
// Honest scope of what has and has not been proven in this session:
// this sandbox has no AWS credentials and no network route to
// kms.*.amazonaws.com, so AWSKMSProvider has NOT been exercised against
// a real AWS KMS endpoint here. What aws_kms_provider_test.go DOES
// prove is that the SigV4 signing logic itself -- canonical request
// construction, string-to-sign, the AWS4-HMAC-SHA256 signing-key
// derivation chain, and Authorization header assembly -- is
// deterministic and internally correct (same inputs produce the same
// signature; a different secret key or a tampered request produces a
// different one), and that the full HTTP request/response plumbing
// (headers, JSON request/response shapes, base64 decoding) round-trips
// correctly against a local httptest server standing in for the KMS
// endpoint. Pointing Endpoint at a real regional KMS endpoint with real
// credentials is a configuration change, not a rewrite -- the same
// posture pkg/blockers/supplychain's HTTPVulnerabilityProvider
// documents for its own real external dependency.
package hsmkms

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const awsKMSService = "kms"

// AWSKMSProvider implements keys.KeyProvider against the real AWS KMS
// Sign and GetPublicKey APIs. Mode() reports "REAL": this genuinely goes
// over the network to AWS, which is exactly why RefuseRealProvider
// exists to keep it out of fixture qualification paths.
type AWSKMSProvider struct {
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string // optional; set for temporary/STS credentials
	Endpoint        string // defaults to https://kms.<region>.amazonaws.com
	HTTPClient      *http.Client

	// SigningAlgorithm is the AWS KMS SigningAlgorithm requested on every
	// Sign call (e.g. "ECDSA_SHA_256", "RSASSA_PKCS1_V1_5_SHA_256"). KMS
	// has no single default across key specs, so this must be set.
	SigningAlgorithm string

	// now is overridable for tests; production callers leave it nil and
	// get time.Now().UTC().
	now func() time.Time
}

// Mode implements the realModeProvider capability interface: this
// provider is always "REAL".
func (p *AWSKMSProvider) Mode() string { return "REAL" }

func (p *AWSKMSProvider) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now().UTC()
}

func (p *AWSKMSProvider) endpoint() string {
	if p.Endpoint != "" {
		return p.Endpoint
	}
	return fmt.Sprintf("https://kms.%s.amazonaws.com", p.Region)
}

func (p *AWSKMSProvider) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}

type kmsSignRequest struct {
	KeyId            string `json:"KeyId"`
	Message          string `json:"Message"`
	MessageType      string `json:"MessageType"`
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

type kmsSignResponse struct {
	KeyId            string `json:"KeyId"`
	Signature        string `json:"Signature"`
	SigningAlgorithm string `json:"SigningAlgorithm"`
}

// Sign implements keys.KeyProvider by calling the real AWS KMS Sign API
// with MessageType=DIGEST -- digest is already a hash, never the
// original message, matching how keys.Manager calls its provider.
func (p *AWSKMSProvider) Sign(ctx context.Context, keyID string, digest []byte) ([]byte, error) {
	if p.SigningAlgorithm == "" {
		return nil, errors.New("hsmkms: AWSKMSProvider.SigningAlgorithm must be set")
	}
	body, err := json.Marshal(kmsSignRequest{
		KeyId: keyID, Message: base64.StdEncoding.EncodeToString(digest),
		MessageType: "DIGEST", SigningAlgorithm: p.SigningAlgorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("hsmkms: marshal KMS Sign request: %w", err)
	}
	respBody, err := p.call(ctx, "Sign", body)
	if err != nil {
		return nil, err
	}
	var resp kmsSignResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("hsmkms: decode KMS Sign response: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(resp.Signature)
	if err != nil {
		return nil, fmt.Errorf("hsmkms: decode KMS Sign Signature base64: %w", err)
	}
	return sig, nil
}

type kmsGetPublicKeyRequest struct {
	KeyId string `json:"KeyId"`
}

type kmsGetPublicKeyResponse struct {
	KeyId     string `json:"KeyId"`
	PublicKey string `json:"PublicKey"`
}

// PublicKey implements keys.KeyProvider by calling the real AWS KMS
// GetPublicKey API. The returned bytes are the DER-encoded SubjectPublicKeyInfo
// KMS returns, exactly as AWS defines it -- callers that need an
// ed25519.PublicKey-shaped value (as keys.Manager.Verify does) must use
// a provider whose key spec actually produces one; KMS does not support
// ed25519 keys as of this writing, so AWSKMSProvider is meant for
// production key custody wired through code that understands DER SPKI,
// not as a drop-in for keys.Manager.Verify's hardcoded ed25519 check.
func (p *AWSKMSProvider) PublicKey(ctx context.Context, keyID string) ([]byte, error) {
	body, err := json.Marshal(kmsGetPublicKeyRequest{KeyId: keyID})
	if err != nil {
		return nil, fmt.Errorf("hsmkms: marshal KMS GetPublicKey request: %w", err)
	}
	respBody, err := p.call(ctx, "GetPublicKey", body)
	if err != nil {
		return nil, err
	}
	var resp kmsGetPublicKeyResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("hsmkms: decode KMS GetPublicKey response: %w", err)
	}
	pub, err := base64.StdEncoding.DecodeString(resp.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("hsmkms: decode KMS GetPublicKey PublicKey base64: %w", err)
	}
	return pub, nil
}

// call issues one SigV4-signed request against a KMS JSON-protocol
// action ("Sign", "GetPublicKey", ...) and returns the raw response body
// on a 200 OK, or an error including the response body on anything else.
func (p *AWSKMSProvider) call(ctx context.Context, action string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint()+"/", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hsmkms: build KMS %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "TrentService."+action)
	if err := p.signV4(req, body, p.clock()); err != nil {
		return nil, fmt.Errorf("hsmkms: sign KMS %s request: %w", action, err)
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("hsmkms: KMS %s: %w", action, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hsmkms: read KMS %s response: %w", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hsmkms: KMS %s returned status %d: %s", action, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// --- AWS Signature Version 4 ------------------------------------------
//
// Implements the four canonical steps from AWS's own SigV4 spec:
//  1. Build a canonical request from the HTTP method, path, query
//     string, a fixed set of signed headers, and the payload hash.
//  2. Build a string to sign from a fixed algorithm tag, the request
//     timestamp, a credential scope (date/region/service), and the hash
//     of the canonical request.
//  3. Derive a signing key by HMAC-SHA256-chaining the secret key
//     through date -> region -> service -> "aws4_request".
//  4. HMAC-SHA256 the string to sign with the derived key to get the
//     signature, then assemble the Authorization header.

// signV4 computes AWS Signature Version 4 for req against the kms
// service and sets the Host, X-Amz-Date, X-Amz-Content-Sha256,
// X-Amz-Security-Token (if set), and Authorization headers.
func (p *AWSKMSProvider) signV4(req *http.Request, body []byte, t time.Time) error {
	if p.AccessKeyID == "" || p.SecretAccessKey == "" {
		return errors.New("hsmkms: AWSKMSProvider.AccessKeyID and SecretAccessKey must be set")
	}
	if p.Region == "" {
		return errors.New("hsmkms: AWSKMSProvider.Region must be set")
	}

	amzDate := t.Format("20060102T150405Z")
	dateStamp := t.Format("20060102")
	host := req.URL.Host

	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Date", amzDate)
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if p.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", p.SessionToken)
	}

	canonicalURI := uriEncodePath(req.URL.Path)
	canonicalQuery := canonicalQueryString(req.URL.Query())
	signedHeaderNames, canonicalHeaders := canonicalizeHeaders(req.Header, host)
	signedHeaders := strings.Join(signedHeaderNames, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, p.Region, awsKMSService)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(p.SecretAccessKey, dateStamp, p.Region, awsKMSService)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.AccessKeyID, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
	return nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// deriveSigningKey implements the AWS4-HMAC-SHA256 key-derivation chain:
// kSecret -> kDate -> kRegion -> kService -> kSigning.
func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

// canonicalizeHeaders returns the sorted, lower-cased signed-header
// names and the canonical-headers block SigV4 requires. Only Host and
// the X-Amz-*/Content-Type headers this signer itself sets are ever
// included -- deliberately minimal, so Go's http.Header map (which
// iterates in unspecified order) can never make two runs of the same
// logical request sign different content.
func canonicalizeHeaders(h http.Header, host string) (names []string, block string) {
	include := map[string]string{"host": host}
	for k := range h {
		lk := strings.ToLower(k)
		if lk == "host" {
			continue
		}
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" {
			include[lk] = strings.TrimSpace(h.Get(k))
		}
	}
	names = make([]string, 0, len(include))
	for k := range include {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, k := range names {
		b.WriteString(k)
		b.WriteByte(':')
		b.WriteString(include[k])
		b.WriteByte('\n')
	}
	return names, b.String()
}

// canonicalQueryString renders q as SigV4's canonical query string:
// URI-encoded, sorted by key then value. KMS's JSON-protocol actions
// never carry query parameters in practice, but this is implemented to
// spec rather than left as dead code returning "".
func canonicalQueryString(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string(nil), q[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, uriEncodeComponent(k)+"="+uriEncodeComponent(v))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncodePath URI-encodes a path per SigV4 rules (RFC 3986 unreserved
// characters left alone, "/" preserved as a path separator).
func uriEncodePath(path string) string {
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = uriEncodeComponent(seg)
	}
	return strings.Join(segments, "/")
}

// uriEncodeComponent URI-encodes one path segment or query key/value per
// SigV4's rules: unreserved characters (A-Z a-z 0-9 - _ . ~) are left
// alone, everything else becomes %XX with uppercase hex digits.
func uriEncodeComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

package awskms

// Hand-rolled AWS Signature Version 4 request signing for the KMS
// REST/JSON API, using ONLY net/http + crypto/hmac + crypto/sha256 --
// the AWS SDK is not a dependency this module is allowed to take (see
// the package doc comment). This follows the algorithm AWS documents
// at https://docs.aws.amazon.com/general/latest/gr/sigv4-signing.html:
// build a canonical request, hash it, build a string-to-sign, derive a
// signing key by an HMAC chain seeded from the secret key, and sign
// the string-to-sign with it.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sigv4Algorithm     = "AWS4-HMAC-SHA256"
	sigv4RequestSuffix = "aws4_request"
	kmsService         = "kms"
	kmsContentType     = "application/x-amz-json-1.1"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hmacSHA256 computes an HMAC-SHA256 MAC. hash.Hash.Write on an hmac
// instance never returns an error (it cannot fail short of an internal
// panic), so the error is deliberately discarded rather than plumbed
// through every caller.
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

// deriveSigningKey computes SigV4's signing key via the documented
// HMAC chain: AWS4 + secret -> date -> region -> service -> "aws4_request".
func deriveSigningKey(secretAccessKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretAccessKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte(sigv4RequestSuffix))
}

// newSignedRequest builds a SigV4-signed POST request for one KMS
// JSON-1.1 action against endpoint. target is the X-Amz-Target header
// value, e.g. "TrentService.Sign".
func newSignedRequest(ctx context.Context, endpoint, region, target string, payload []byte, creds Credentials) (*http.Request, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("awskms: bad endpoint %q: %w", endpoint, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("awskms: endpoint %q has no host", endpoint)
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	canonicalHeaders := map[string]string{
		"content-type": kmsContentType,
		"host":         u.Host,
		"x-amz-date":   amzDate,
		"x-amz-target": target,
	}
	if creds.SessionToken != "" {
		canonicalHeaders["x-amz-security-token"] = creds.SessionToken
	}

	signedHeaderNames := make([]string, 0, len(canonicalHeaders))
	for k := range canonicalHeaders {
		signedHeaderNames = append(signedHeaderNames, k)
	}
	sort.Strings(signedHeaderNames)

	var canonicalHeaderBlock strings.Builder
	for _, name := range signedHeaderNames {
		canonicalHeaderBlock.WriteString(name)
		canonicalHeaderBlock.WriteString(":")
		canonicalHeaderBlock.WriteString(canonicalHeaders[name])
		canonicalHeaderBlock.WriteString("\n")
	}
	signedHeaders := strings.Join(signedHeaderNames, ";")

	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		"/", // canonical URI: KMS actions are always POST /
		"",  // canonical query string: none
		canonicalHeaderBlock.String(),
		signedHeaders,
		sha256Hex(payload),
	}, "\n")

	credentialScope := strings.Join([]string{dateStamp, region, kmsService, sigv4RequestSuffix}, "/")
	stringToSign := strings.Join([]string{
		sigv4Algorithm,
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveSigningKey(creds.SecretAccessKey, dateStamp, region, kmsService)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	authorization := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		sigv4Algorithm, creds.AccessKeyID, credentialScope, signedHeaders, signature)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Host = u.Host
	req.Header.Set("Content-Type", kmsContentType)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("Authorization", authorization)
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	return req, nil
}

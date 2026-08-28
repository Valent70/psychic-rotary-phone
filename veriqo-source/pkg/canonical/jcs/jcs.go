// Package jcs implements RFC 8785 (JSON Canonicalization Scheme,
// "JCS"), the canonicalization authority VTECP-001 §7 names explicitly
// as a correction to the source blueprint's own hand-rolled JSON
// sorting: "The Perplexity proposal's custom JSON sorting
// implementation MUST NOT become VERIQO's canonicalization authority
// ... Do not implement an ad-hoc canonical JSON algorithm if JCS can be
// used."
//
// Every VTECP/CRE package that needs a stable, cross-implementation
// hash over structured data (evidence manifests, custody events,
// ontology objects/links, policy decisions, inference traces) canonicalizes
// through Canonicalize/Hash here, rather than each struct hand-rolling
// its own field-by-field string builder the way earlier packages in
// this repository (payment.canonicalBytes, authz.Document.canonicalBytes,
// etc.) independently do. Those earlier callers are NOT migrated by
// this package — REUSE > EXTEND > REFACTOR > CREATE means new callers
// use this, existing ones are untouched.
//
// # Honest scope limitation
//
// RFC 8785 §3.2.2.3 requires JSON numbers to be serialized via the
// ECMAScript Number::toString algorithm, including its exact rules for
// exponential notation at very large/small magnitudes. This
// implementation is exact for every number shape this codebase actually
// produces on a canonicalization path -- 64-bit integers (ticks, minor-
// unit fixed-point money, sequence numbers: this repo's own
// established "money and ticks are integers, never float" discipline
// means these are the overwhelming majority) and float64 values in or
// near [-1e15, 1e15] with no more than 17 significant digits (every
// confidence/probability/score value anywhere in this codebase is in
// [0,1], and no float money value exists at all -- see
// pkg/insurance/quantum.Amount). It has NOT been verified against the
// ECMAScript spec's exact output for floats at the extreme ends of the
// IEEE-754 range (subnormals, values requiring exponential notation).
// Anything that reaches Canonicalize with such a value returns
// ErrUnsupportedNumber rather than silently emitting a possibly-
// non-interoperable representation -- honest refusal, not a fabricated
// claim of full spec compliance.
package jcs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	// ErrUnsupportedNumber is returned when a json.Number (or float64,
	// when marshaling a Go value directly) falls outside the range this
	// implementation has verified against the ECMAScript Number::toString
	// algorithm -- see the package doc comment's "honest scope
	// limitation."
	ErrUnsupportedNumber = errors.New("jcs: number magnitude is outside this implementation's verified range")
	// ErrUnsupportedType is returned for a Go value with no JSON
	// representation (e.g. a channel, a func, a complex number).
	ErrUnsupportedType = errors.New("jcs: value has no canonical JSON representation")
)

// Canonicalize returns v's RFC 8785 canonical JSON byte representation.
// v may be any value encoding/json can marshal (typically a struct with
// json tags, or a map[string]any/[]any tree already produced by
// json.Unmarshal) -- Canonicalize round-trips it through encoding/json
// first so struct tags, omitempty, etc. are honoured exactly as they
// would be for a normal marshal, then re-serializes the resulting
// generic tree under JCS's own ordering/formatting rules.
func Canonicalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("jcs: marshaling input: %w", err)
	}
	var generic any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("jcs: re-decoding for canonicalization: %w", err)
	}
	var b strings.Builder
	if err := encode(&b, generic); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// Hash returns the hex-encoded SHA-256 digest of v's canonical bytes --
// the identity/integrity primitive every new VTECP/CRE canonical type
// uses (manifest identity, custody-event chaining, PolicyInputsHash,
// InferenceTrace input/output hashes).
func Hash(v any) (string, error) {
	b, err := Canonicalize(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// MustHash is Hash for call sites that have already validated v is
// canonicalizable (e.g. it was constructed from known-good fields) and
// would treat a canonicalization failure as a programming error, not a
// caller-facing one -- mirrors the rest of this codebase's own
// established convention of separating "validate, then trust" from
// "recheck on every call."
func MustHash(v any) string {
	h, err := Hash(v)
	if err != nil {
		panic("jcs: MustHash: " + err.Error())
	}
	return h
}

func encode(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
		return nil
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case string:
		encodeString(b, t)
		return nil
	case json.Number:
		return encodeNumber(b, t)
	case float64:
		return encodeNumber(b, json.Number(strconv.FormatFloat(t, 'g', -1, 64)))
	case map[string]any:
		return encodeObject(b, t)
	case []any:
		return encodeArray(b, t)
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedType, v)
	}
}

// encodeObject writes members sorted by the shortest sequence of UTF-16
// code units of the member name (RFC 8785 §3.2.3), not a raw byte or
// rune comparison -- these differ once a key contains a character
// outside the Basic Multilingual Plane.
func encodeObject(b *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		encodeString(b, k)
		b.WriteByte(':')
		if err := encode(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

func encodeArray(b *strings.Builder, a []any) error {
	b.WriteByte('[')
	for i, v := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := encode(b, v); err != nil {
			return err
		}
	}
	b.WriteByte(']')
	return nil
}

// utf16Less reports whether a sorts before b under RFC 8785's UTF-16
// code-unit ordering.
func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// encodeString writes a JSON string literal per RFC 8785 §3.2.2.2:
// quote, backslash and the C0 control range are escaped (with the
// short forms \b \f \n \r \t where they apply), every other character
// -- including all non-ASCII -- is emitted as literal UTF-8, never a
// forced \uXXXX escape.
func encodeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else if r == utf8.RuneError {
				b.WriteString(`�`)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// encodeNumber writes n per the ECMAScript Number::toString algorithm,
// within this implementation's verified range -- see the package doc
// comment's "honest scope limitation."
func encodeNumber(b *strings.Builder, n json.Number) error {
	s := n.String()
	if iv, err := n.Int64(); err == nil {
		b.WriteString(strconv.FormatInt(iv, 10))
		return nil
	}
	// A tick or sequence number can legitimately exceed int64's range
	// (this codebase's ticks and sequence numbers are uint64
	// throughout) without needing float64's lossy path at all -- try an
	// exact uint64 parse before falling through to float handling.
	if uv, err := strconv.ParseUint(s, 10, 64); err == nil {
		b.WriteString(strconv.FormatUint(uv, 10))
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnsupportedNumber, s)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%w: %s is not representable in JSON", ErrUnsupportedNumber, s)
	}
	if math.Abs(f) >= 1e15 && f != 0 {
		return fmt.Errorf("%w: %s exceeds this implementation's verified magnitude range", ErrUnsupportedNumber, s)
	}
	out := strconv.FormatFloat(f, 'g', -1, 64)
	if strings.ContainsAny(out, "eE") {
		return fmt.Errorf("%w: %s would require exponential notation, unverified against ECMAScript Number::toString", ErrUnsupportedNumber, s)
	}
	b.WriteString(out)
	return nil
}

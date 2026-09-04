// Package jcs implements RFC 8785, the JSON Canonicalization Scheme,
// and the SHA-256 digest VERIQO derives from it.
//
// # Why this is the first package in the system
//
// Every integrity guarantee VERIQO makes reduces to one question: do
// two parties, serialising the same value independently, produce the
// same bytes? If the answer is "usually", then a hash chain proves
// nothing, a replay proves nothing, and a signature covers a document
// that only one party can reconstruct.
//
// Go's encoding/json is not an answer to that question. It sorts map
// keys by Go string comparison (byte order, not UTF-16 code-unit
// order), it renders floats with strconv's shortest-round-trip
// algorithm (which differs from ECMAScript's at the edges), and it
// HTML-escapes <, > and & by default. Each of those is harmless in an
// API response and fatal in a ledger.
//
// # What canonicalisation fixes, precisely
//
//	object keys   sorted by UTF-16 code unit, not by byte
//	numbers       ECMAScript Number::toString (RFC 8785 s3.2.2.3)
//	strings       minimal escaping; no \u for printable non-ASCII
//	whitespace    none
//	literals      true, false, null verbatim
//
// # What this package refuses
//
// NaN and +/-Inf have no JSON representation, so a value carrying one
// cannot be canonicalised and must not be silently rendered as null --
// that would let a computation that diverged produce a valid-looking
// hash. Likewise a non-integral float that is actually an integer is
// rendered as an integer, because RFC 8785 says so and because
// 1 and 1.0 hashing differently is a defect that only appears in
// production.
package jcs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	// ErrNotFinite is returned for NaN and infinities. They are not
	// representable in JSON and must not be coerced.
	ErrNotFinite = errors.New("jcs: value is not a finite number, so it has no canonical JSON form")
	// ErrUnsupported is returned for a Go value with no JSON form.
	ErrUnsupported = errors.New("jcs: value has no JSON representation")
	// ErrInvalidUTF8 is returned for a string that is not valid UTF-8.
	// Canonicalising it would require a lossy repair, and a lossy
	// repair is a silent change of the thing being hashed.
	ErrInvalidUTF8 = errors.New("jcs: string is not valid UTF-8")
)

// Canonicalize renders v as RFC 8785 canonical JSON.
//
// v may be a Go value (marshalled through encoding/json first, with
// number fidelity preserved) or already-parsed JSON.
func Canonicalize(v any) ([]byte, error) {
	parsed, err := toGeneric(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := write(&buf, parsed); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CanonicalizeBytes canonicalises an existing JSON document.
func CanonicalizeBytes(doc []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("jcs: parsing input: %w", err)
	}
	if dec.More() {
		return nil, errors.New("jcs: input carries trailing content after the JSON value")
	}
	var buf bytes.Buffer
	if err := write(&buf, parsed); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Hash returns the lowercase hex SHA-256 of the canonical form.
//
// This is the digest every VERIQO hash chain, passport signature and
// replay manifest is built on.
func Hash(v any) (string, error) {
	b, err := Canonicalize(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// HashBytes returns the lowercase hex SHA-256 of raw bytes, without
// canonicalisation. Use it for opaque artefacts (a PDF, an image),
// never for structured records.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// MustHash is Hash for values known at construction time. It panics
// rather than returning an error, which is correct only for literals.
func MustHash(v any) string {
	h, err := Hash(v)
	if err != nil {
		panic(fmt.Sprintf("jcs: MustHash: %v", err))
	}
	return h
}

// toGeneric routes a Go value into the generic tree the writer walks,
// keeping numbers as json.Number so no float rounding happens before
// the RFC 8785 number rules are applied.
func toGeneric(v any) (any, error) {
	switch t := v.(type) {
	case nil, bool, string, json.Number:
		return v, nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil, fmt.Errorf("%w: %v", ErrNotFinite, t)
		}
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: %v", ErrNotFinite, t)
		}
	}
	// encoding/json silently replaces invalid UTF-8 with U+FFFD rather
	// than failing. That repair happens BEFORE anything here can see
	// it, so the digest would cover bytes nobody supplied. The value is
	// therefore walked for string validity first.
	if err := assertUTF8(reflect.ValueOf(v), make(map[uintptr]bool)); err != nil {
		return nil, err
	}
	enc, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %T: %v", ErrUnsupported, v, err)
	}
	dec := json.NewDecoder(bytes.NewReader(enc))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("jcs: re-reading marshalled value: %w", err)
	}
	return parsed, nil
}

func write(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
		return nil
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil
	case string:
		return writeString(buf, t)
	case json.Number:
		return writeNumberLiteral(buf, t.String())
	case float64:
		return writeNumber(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := write(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sortUTF16(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := write(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	default:
		return fmt.Errorf("%w: %T after normalisation", ErrUnsupported, v)
	}
}

func writeNumberLiteral(buf *bytes.Buffer, lit string) error {
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return fmt.Errorf("jcs: number %q is not representable: %w", lit, err)
	}
	return writeNumber(buf, f)
}

// writeNumber implements RFC 8785 s3.2.2.3: the serialisation is
// ECMAScript's Number::toString.
//
// Go's 'g' format and ECMAScript agree on the shortest round-trip
// digits but disagree on when to use exponential notation and on the
// exponent's own spelling, so the digits come from Go and the layout
// is rebuilt here.
func writeNumber(buf *bytes.Buffer, f float64) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%w: %v", ErrNotFinite, f)
	}
	if f == 0 {
		// -0 canonicalises to 0: RFC 8785 follows ECMAScript, where
		// String(-0) is "0". Preserving the sign would make two
		// arithmetically equal results hash differently.
		buf.WriteString("0")
		return nil
	}
	buf.WriteString(ecmaNumberToString(f))
	return nil
}

// ecmaNumberToString reproduces ECMAScript Number::toString for a
// finite non-zero double.
func ecmaNumberToString(f float64) string {
	neg := f < 0
	if neg {
		f = -f
	}
	// Shortest decimal digits that round-trip, as 'e' format: d.dddde±dd
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, expPart, _ := strings.Cut(sci, "e")
	exp, err := strconv.Atoi(expPart)
	if err != nil {
		return sci
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	k := len(digits) // number of significant digits
	n := exp + 1     // position of the decimal point relative to digits

	var out string
	switch {
	case k <= n && n <= 21:
		// integer, possibly with trailing zeros
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		e := n - 1
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		if k == 1 {
			out = digits + "e" + sign + strconv.Itoa(e)
		} else {
			out = digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(e)
		}
	}
	if neg {
		return "-" + out
	}
	return out
}

// writeString applies RFC 8785 s3.2.2.2 escaping: the two-character
// escapes where they exist, \u00XX for the remaining control
// characters, and the literal character for everything else --
// including non-ASCII, which is emitted as UTF-8 rather than \u.
func writeString(buf *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %q", ErrInvalidUTF8, s)
	}
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return nil
}

// sortUTF16 orders keys by UTF-16 code unit, which is what RFC 8785
// specifies and what Go's native string comparison does NOT do for
// characters outside the BMP.
//
// The divergence is real: U+FFFD sorts after U+1F600 in UTF-16 code
// units (0xFFFD > 0xD83D) and before it in UTF-8 bytes. Sorting by
// bytes would produce a canonical form that a conforming
// implementation rejects.
func sortUTF16(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		return lessUTF16(keys[i], keys[j])
	})
}

func lessUTF16(a, b string) bool {
	ua := utf16.Encode([]rune(a))
	ub := utf16.Encode([]rune(b))
	n := len(ua)
	if len(ub) < n {
		n = len(ub)
	}
	for i := 0; i < n; i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// assertUTF8 walks a value looking for a string that is not valid
// UTF-8, in a map key or anywhere else. It exists because
// encoding/json's silent repair is indistinguishable, after the fact,
// from the caller having supplied the repaired text.
//
// seen guards against cyclic structures, which encoding/json refuses
// anyway but which must not hang this walk first.
func assertUTF8(v reflect.Value, seen map[uintptr]bool) error {
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("%w: %q", ErrInvalidUTF8, v.String())
		}
		return nil
	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		if v.Kind() == reflect.Ptr {
			if seen[v.Pointer()] {
				return nil
			}
			seen[v.Pointer()] = true
		}
		return assertUTF8(v.Elem(), seen)
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		fallthrough
	case reflect.Array:
		// []byte is opaque data, not text: it JSON-encodes as base64,
		// so its bytes never need to be valid UTF-8.
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		for i := 0; i < v.Len(); i++ {
			if err := assertUTF8(v.Index(i), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		iter := v.MapRange()
		for iter.Next() {
			if err := assertUTF8(iter.Key(), seen); err != nil {
				return err
			}
			if err := assertUTF8(iter.Value(), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				continue
			}
			if err := assertUTF8(v.Field(i), seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

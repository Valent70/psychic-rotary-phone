package jcs

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestECMAScriptNumberSerialisation pins RFC 8785 s3.2.2.3.
//
// These are the boundaries where a naive strconv.FormatFloat(f,'g',-1,64)
// diverges from ECMAScript, and every one of them is reachable from
// ordinary data: 1e21 is a large monetary amount in minor units, 1e-7
// is a coordinate delta.
func TestECMAScriptNumberSerialisation(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{1.5, "1.5"},
		{100, "100"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{1e30, "1e+30"},
		{1e-6, "0.000001"},
		{1e-7, "1e-7"},
		{5e-324, "5e-324"},
		{1.7976931348623157e308, "1.7976931348623157e+308"},
		{333333333.33333329, "333333333.3333333"},
	}
	for _, c := range cases {
		got, err := Canonicalize(c.in)
		if err != nil {
			t.Fatalf("Canonicalize(%v): %v", c.in, err)
		}
		if string(got) != c.want {
			t.Errorf("Canonicalize(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

// TestNegativeZeroCanonicalisesToZero is separate because it is the one
// number rule people argue with. -0 and 0 are arithmetically equal, so
// hashing them differently would make two identical computations
// produce different ledger entries.
func TestNegativeZeroCanonicalisesToZero(t *testing.T) {
	a, err := Hash(map[string]any{"v": 0.0})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash(map[string]any{"v": negZero()})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("-0 and 0 hash differently: %s vs %s", a, b)
	}
}

func negZero() float64 { z := 0.0; return -z }

// TestKeysAreSortedByUTF16CodeUnit is the divergence from Go's own
// string ordering. If this rule is dropped, the system still works and
// silently stops interoperating with every other JCS implementation.
//
// U+FFFD encodes as the single UTF-16 unit 0xFFFD; U+1F600 encodes as
// the surrogate pair 0xD83D 0xDE00. In UTF-16 order the emoji sorts
// FIRST (0xD83D < 0xFFFD). In UTF-8 byte order it sorts LAST
// (0xF0... > 0xEF...). Only one of those is RFC 8785.
func TestKeysAreSortedByUTF16CodeUnit(t *testing.T) {
	replacement := "�"
	emoji := "\U0001F600"

	got, err := Canonicalize(map[string]any{replacement: 1, emoji: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"` + emoji + `":2,"` + replacement + `":1}`
	if string(got) != want {
		t.Fatalf("UTF-16 ordering not applied:\n got %q\nwant %q", got, want)
	}
	// And state the trap explicitly: Go's own comparison disagrees.
	if !(replacement < emoji) {
		t.Fatal("premise of this test changed: Go byte order no longer puts U+FFFD first")
	}
}

// TestNonASCIIIsNotEscaped: RFC 8785 emits UTF-8, not \u sequences,
// and encoding/json's default HTML escaping must not survive.
func TestNonASCIIIsNotEscaped(t *testing.T) {
	const s = "café <&> 日本"
	got, err := Canonicalize(map[string]any{"k": s})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `\u`) {
		t.Fatalf("escaping applied where none is specified: %s", got)
	}
	if !strings.Contains(string(got), s) {
		t.Fatalf("literal text not preserved: %s", got)
	}
}

// TestControlCharactersUseTheSpecifiedForm.
func TestControlCharactersUseTheSpecifiedForm(t *testing.T) {
	got, err := Canonicalize(map[string]any{"k": "a\tb\ncd\u0001"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"k":"a\tb\ncd\u0001"}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

// TestNonFiniteIsRefusedNotCoerced.
//
// A canonicaliser that fell back to null would let a computation that
// diverged produce a valid-looking hash.
func TestNonFiniteIsRefusedNotCoerced(t *testing.T) {
	for _, v := range []float64{nan(), inf(1), inf(-1)} {
		if _, err := Canonicalize(v); err == nil {
			t.Fatalf("Canonicalize(%v) was accepted", v)
		}
	}
}

func nan() float64 { z := 0.0; return z / z }

func inf(s int) float64 {
	z := 0.0
	if s < 0 {
		return -1 / z
	}
	return 1 / z
}

// TestCanonicalFormIsStableAcrossKeyInsertionOrder is the property the
// whole ledger rests on.
func TestCanonicalFormIsStableAcrossKeyInsertionOrder(t *testing.T) {
	a := `{"b":2,"a":1,"c":{"z":1,"y":[3,2,1]}}`
	b := `{"c":{"y":[3,2,1],"z":1},"a":1,"b":2}`
	ca, err := CanonicalizeBytes([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	cb, err := CanonicalizeBytes([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("insertion order leaked into the canonical form:\n%s\n%s", ca, cb)
	}
	if string(ca) != `{"a":1,"b":2,"c":{"y":[3,2,1],"z":1}}` {
		t.Fatalf("unexpected canonical form: %s", ca)
	}
}

// TestArrayOrderIsPreserved: arrays are ordered data, unlike objects.
func TestArrayOrderIsPreserved(t *testing.T) {
	got, err := Canonicalize([]any{3, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[3,1,2]" {
		t.Fatalf("array reordered: %s", got)
	}
}

// TestIntegerValuedFloatsRenderAsIntegers.
//
// 1 and 1.0 must hash identically or a value that round-trips through
// JSON changes its own digest.
func TestIntegerValuedFloatsRenderAsIntegers(t *testing.T) {
	a, err := Hash(json.Number("1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash(1.0)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("1 and 1.0 hash differently: %s vs %s", a, b)
	}
}

// TestTrailingContentIsRefused: two concatenated JSON values must not
// canonicalise to the first one silently.
func TestTrailingContentIsRefused(t *testing.T) {
	if _, err := CanonicalizeBytes([]byte(`{"a":1}{"b":2}`)); err == nil {
		t.Fatal("trailing content was accepted")
	}
}

// TestInvalidUTF8IsRefused: repairing it would change the thing being
// hashed without telling anyone.
func TestInvalidUTF8IsRefused(t *testing.T) {
	if _, err := Canonicalize(map[string]any{"k": string([]byte{0xff, 0xfe})}); err == nil {
		t.Fatal("invalid UTF-8 was accepted")
	}
}

// TestHashIsStableAcrossRuns guards against map-iteration order
// leaking into the digest.
func TestHashIsStableAcrossRuns(t *testing.T) {
	v := map[string]any{"z": 1, "a": 2, "m": []any{1, "x", true, nil}}
	first, err := Hash(v)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		h, err := Hash(v)
		if err != nil {
			t.Fatal(err)
		}
		if h != first {
			t.Fatalf("hash unstable at iteration %d: %s vs %s", i, h, first)
		}
	}
}

// TestHashBytesIsNotCanonicalisation documents the boundary: opaque
// artefacts are hashed as bytes, structured records as canonical JSON.
// Conflating them is how a PDF's digest ends up depending on how a
// struct was spelled.
func TestHashBytesIsNotCanonicalisation(t *testing.T) {
	raw := []byte(`{"b":1,"a":2}`)
	byBytes := HashBytes(raw)
	byCanon, err := func() (string, error) {
		c, err := CanonicalizeBytes(raw)
		if err != nil {
			return "", err
		}
		return HashBytes(c), nil
	}()
	if err != nil {
		t.Fatal(err)
	}
	if byBytes == byCanon {
		t.Fatal("the two digests coincided; this fixture no longer distinguishes them")
	}
}

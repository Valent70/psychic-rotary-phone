package jcs

import (
	"math"
	"testing"
)

func TestCanonicalizeSortsObjectKeys(t *testing.T) {
	got, err := Canonicalize(map[string]any{"b": 1, "a": 2, "c": 3})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != `{"a":2,"b":1,"c":3}` {
		t.Fatalf("expected sorted keys, got %s", got)
	}
}

func TestCanonicalizeIsDeterministicAcrossMapIterationOrder(t *testing.T) {
	// Go map iteration order is randomized; canonicalizing the same
	// logical object many times must always produce byte-identical
	// output.
	m := map[string]any{"z": 1, "y": 2, "x": 3, "w": 4, "v": 5, "u": 6}
	first, err := Canonicalize(m)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := Canonicalize(m)
		if err != nil {
			t.Fatalf("Canonicalize: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("non-deterministic output on iteration %d: %s vs %s", i, got, first)
		}
	}
}

func TestCanonicalizeNestedStructure(t *testing.T) {
	v := map[string]any{
		"outer": map[string]any{"b": "2", "a": "1"},
		"list":  []any{3, 1, 2},
	}
	got, err := Canonicalize(v)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := `{"list":[3,1,2],"outer":{"a":"1","b":"2"}}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeStringEscaping(t *testing.T) {
	got, err := Canonicalize(map[string]any{"s": "a\"b\\c\nd\te"})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := `{"s":"a\"b\\c\nd\te"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeControlCharacterEscaping(t *testing.T) {
	got, err := Canonicalize(map[string]any{"s": "\x01\x1f"})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := "{\"s\":\"\\u0001\\u001f\"}"
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeNonASCIIEmittedLiterally(t *testing.T) {
	got, err := Canonicalize(map[string]any{"s": "héllo日本語"})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := `{"s":"héllo日本語"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestCanonicalizeIntegersHaveNoDecimalPoint(t *testing.T) {
	got, err := Canonicalize(map[string]any{"n": 42})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != `{"n":42}` {
		t.Fatalf("got %s", got)
	}
}

func TestCanonicalizeUint64Ticks(t *testing.T) {
	got, err := Canonicalize(struct {
		Tick uint64 `json:"tick"`
	}{Tick: 18446744073709551615})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != `{"tick":18446744073709551615}` {
		t.Fatalf("got %s", got)
	}
}

func TestCanonicalizeFractionalConfidenceScore(t *testing.T) {
	got, err := Canonicalize(map[string]any{"confidence": 0.87})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != `{"confidence":0.87}` {
		t.Fatalf("got %s", got)
	}
}

func TestCanonicalizeRefusesUnverifiedLargeMagnitude(t *testing.T) {
	_, err := Canonicalize(map[string]any{"n": 1e20})
	if err == nil {
		t.Fatal("expected a magnitude outside the verified range to be refused, not silently mis-canonicalized")
	}
}

func TestCanonicalizeRefusesNaNAndInf(t *testing.T) {
	if _, err := Canonicalize(map[string]any{"n": math.NaN()}); err == nil {
		t.Fatal("expected NaN to be refused")
	}
}

func TestCanonicalizeArraysPreserveOrder(t *testing.T) {
	got, err := Canonicalize([]any{"z", "a", "m"})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != `["z","a","m"]` {
		t.Fatalf("expected array order preserved, got %s", got)
	}
}

func TestCanonicalizeBooleanAndNull(t *testing.T) {
	got, err := Canonicalize(map[string]any{"t": true, "f": false, "n": nil})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(got) != `{"f":false,"n":null,"t":true}` {
		t.Fatalf("got %s", got)
	}
}

func TestHashIsStableAndSensitiveToContent(t *testing.T) {
	h1, err := Hash(map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	h2, err := Hash(map[string]any{"b": 2, "a": 1}) // same content, different literal order
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected identical logical content to hash identically regardless of field order, got %s vs %s", h1, h2)
	}
	h3, err := Hash(map[string]any{"a": 1, "b": 3})
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if h1 == h3 {
		t.Fatal("expected different content to produce a different hash")
	}
	if len(h1) != 64 {
		t.Fatalf("expected a 64-hex-char SHA-256 digest, got %d chars", len(h1))
	}
}

func TestMustHashPanicsOnUncanonicalizableValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected MustHash to panic on an uncanonicalizable value")
		}
	}()
	MustHash(map[string]any{"n": 1e20})
}

// TestUTF16KeyOrderingMatchesCodeUnitValue proves object keys sort by
// UTF-16 code unit value (RFC 8785 §3.2.3), not raw byte order and not
// naive rune-value order, using \u-escaped keys of known code point so
// the expected order is unambiguous rather than hand-derived from
// printed glyphs. U+007E ('~') < U+00E9 ('é') < U+0101 ('ā') as single
// BMP code units, so that must also be their UTF-16 code-unit order.
func TestUTF16KeyOrderingMatchesCodeUnitValue(t *testing.T) {
	got, err := Canonicalize(map[string]any{
		"ā": "a-with-macron", // U+0101
		"~": "tilde",         // U+007E
		"é": "e-with-acute",  // U+00E9
	})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := `{"~":"tilde","é":"e-with-acute","ā":"a-with-macron"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// TestUTF16KeyOrderingHandlesSupplementaryCharacters proves a key
// containing a supplementary-plane character (code point > U+FFFF,
// encoded as a UTF-16 surrogate pair) sorts by its surrogate pair's
// code units rather than by its raw code point value -- the specific
// subtlety RFC 8785 §3.2.3 requires UTF-16 comparison (not UTF-32/rune
// comparison) to get right. U+1F600 (a supplementary-plane emoji)
// encodes as the surrogate pair D83D DE00; D83D < U+E000, so the
// supplementary character must sort BEFORE a plain U+E000 key despite
// U+1F600 being numerically larger as a raw code point.
func TestUTF16KeyOrderingHandlesSupplementaryCharacters(t *testing.T) {
	got, err := Canonicalize(map[string]any{
		"\U0001F600": "supplementary-plane", // U+1F600 -> surrogate pair D83D DE00
		"":          "private-use-bmp",     // U+E000, a single BMP code unit
	})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	want := "{\"\U0001F600\":\"supplementary-plane\",\"\":\"private-use-bmp\"}"
	if string(got) != want {
		t.Fatalf("got %s, want %s -- supplementary character did not sort by its UTF-16 surrogate pair", got, want)
	}
}

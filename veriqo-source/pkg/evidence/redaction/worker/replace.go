package worker

import (
	"bytes"
	"html"
	"strings"
	"time"
)

// fixedModTime keeps derivatives deterministic. Two redactions of the
// same original with the same terms must produce byte-identical
// output, or the derivative's hash records when it was made rather than
// what it contains -- and an evidence version whose identity changes
// with the clock cannot be verified by anyone else.
var fixedModTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// termRenderings returns the forms of a term that can appear in an XML
// part: the term itself, its XML-escaped form, and both case-folded.
//
// The escaped form is not a nicety. A name containing an ampersand
// appears in the XML as "R&amp;D Ltd", and a redactor that searched
// only for "R&D Ltd" would leave it in place while reporting success.
func termRenderings(term string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(term)
	add(html.EscapeString(term))
	add(strings.ToUpper(term))
	add(strings.ToLower(term))
	add(html.EscapeString(strings.ToUpper(term)))
	add(html.EscapeString(strings.ToLower(term)))
	return out
}

// replaceAll removes every rendering of every term, returning the new
// bytes and a count per term.
//
// Matching is case-insensitive because a redaction that removes
// "Acme Holdings" and leaves "ACME HOLDINGS" has not removed anything;
// the verifier checks a case-folded encoding for exactly this reason,
// and a worker that did less than the verifier checks would fail its
// own pipeline.
func replaceAll(body []byte, terms []string, marker string) ([]byte, map[string]int) {
	counts := map[string]int{}
	out := body
	for _, term := range terms {
		for _, rendering := range termRenderings(term) {
			n := countFold(out, rendering)
			if n == 0 {
				continue
			}
			counts[term] += n
			out = replaceFold(out, rendering, marker)
		}
	}
	return out, counts
}

// countFold counts case-insensitive occurrences of needle in haystack.
func countFold(haystack []byte, needle string) int {
	if needle == "" {
		return 0
	}
	return strings.Count(strings.ToLower(string(haystack)), strings.ToLower(needle))
}

// replaceFold replaces every case-insensitive occurrence of needle.
//
// It walks the lower-cased copy for positions and cuts the original at
// those offsets, so the surrounding bytes keep their original case.
func replaceFold(haystack []byte, needle, with string) []byte {
	if needle == "" {
		return haystack
	}
	lowerHay := strings.ToLower(string(haystack))
	lowerNeedle := strings.ToLower(needle)
	var out bytes.Buffer
	off := 0
	for {
		i := strings.Index(lowerHay[off:], lowerNeedle)
		if i < 0 {
			out.Write(haystack[off:])
			break
		}
		out.Write(haystack[off : off+i])
		out.WriteString(with)
		off += i + len(lowerNeedle)
	}
	return out.Bytes()
}

// containsAnyTerm returns the first term present in body, or "".
func containsAnyTerm(body []byte, terms []string) string {
	lower := strings.ToLower(string(body))
	for _, term := range terms {
		for _, rendering := range termRenderings(term) {
			if strings.Contains(lower, strings.ToLower(rendering)) {
				return term
			}
		}
	}
	return ""
}

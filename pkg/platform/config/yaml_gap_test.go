package config

import "testing"

// --- safeLineNo: both the "out of range but lines exist" branch and
// the "no lines at all" branch, reached via parseBlock's own malformed-
// indent error path so this exercises the real call site, not a bare
// unit call.

func TestParseMalformedIndentReportsLastLineNumber(t *testing.T) {
	// An empty "-" sequence item expects its nested block at indent+2 on
	// the very next line; giving it a sibling "- foo" back at the
	// dash's own indent instead is an indent mismatch that IS still
	// within range, exercising safeLineNo's "idx < len(lines)" branch.
	_, err := Parse("items:\n  -\n  - foo\n")
	if err == nil {
		t.Fatalf("expected a malformed-line error for an indent mismatch after an empty sequence item")
	}
}

func TestParseMalformedIndentAtEndOfDocument(t *testing.T) {
	// An empty "-" sequence item as the LAST line of the document has
	// nowhere to find its expected nested block at all, exercising
	// safeLineNo's "idx >= len(lines)" out-of-range branch.
	_, err := Parse("items:\n  -\n")
	if err == nil {
		t.Fatalf("expected a malformed-line error for an empty sequence item at end of document")
	}
}

func TestParseEmptyDocumentErrors(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Fatalf("expected ErrEmptyDocument for an empty document")
	}
	if _, err := Parse("# just a comment\n\n"); err == nil {
		t.Fatalf("expected ErrEmptyDocument for a document with only comments/blank lines")
	}
}

func TestParseTabIndentationRejected(t *testing.T) {
	if _, err := Parse("a:\n\tb: 1"); err == nil {
		t.Fatalf("expected ErrTabIndentation for a tab-indented line")
	}
}

// --- Node accessor nil-safety and error branches ----------------------

func TestNodeGetOnNilAndNonMappingIsNilSafe(t *testing.T) {
	var n *Node
	if got := n.Get("x"); got != nil {
		t.Fatalf("Get on nil node = %v, want nil", got)
	}
	scalar := &Node{Kind: KindScalar, Scalar: "v"}
	if got := scalar.Get("x"); got != nil {
		t.Fatalf("Get on a non-mapping node = %v, want nil", got)
	}
}

func TestNodeStringOnNilIsEmpty(t *testing.T) {
	var n *Node
	if got := n.String(); got != "" {
		t.Fatalf("String on nil node = %q, want empty", got)
	}
}

func TestNodeFloat64OnNilErrors(t *testing.T) {
	var n *Node
	if _, err := n.Float64(); err == nil {
		t.Fatalf("expected error calling Float64 on a nil node")
	}
}

func TestNodeFloat64ParseErrorPropagates(t *testing.T) {
	n := &Node{Kind: KindScalar, Scalar: "not-a-number"}
	if _, err := n.Float64(); err == nil {
		t.Fatalf("expected a strconv parse error for a non-numeric scalar")
	}
}

func TestNodeUint64OnNilErrors(t *testing.T) {
	var n *Node
	if _, err := n.Uint64(); err == nil {
		t.Fatalf("expected error calling Uint64 on a nil node")
	}
}

func TestNodeUint64ParsesValidAndRejectsInvalid(t *testing.T) {
	ok := &Node{Kind: KindScalar, Scalar: "42"}
	got, err := ok.Uint64()
	if err != nil || got != 42 {
		t.Fatalf("Uint64() = (%d, %v), want (42, nil)", got, err)
	}
	bad := &Node{Kind: KindScalar, Scalar: "-1"}
	if _, err := bad.Uint64(); err == nil {
		t.Fatalf("expected error parsing a negative value as Uint64")
	}
	nonNumeric := &Node{Kind: KindScalar, Scalar: "abc"}
	if _, err := nonNumeric.Uint64(); err == nil {
		t.Fatalf("expected error parsing a non-numeric value as Uint64")
	}
}

func TestNodeItemsOnNilAndNonSequenceIsNilSafe(t *testing.T) {
	var n *Node
	if got := n.Items(); got != nil {
		t.Fatalf("Items on nil node = %v, want nil", got)
	}
	scalar := &Node{Kind: KindScalar, Scalar: "v"}
	if got := scalar.Items(); got != nil {
		t.Fatalf("Items on a non-sequence node = %v, want nil", got)
	}
}

// --- parseMapping: missing ':' error branch ---------------------------

func TestParseMappingMissingColonErrors(t *testing.T) {
	if _, err := Parse("this line has no colon at all"); err == nil {
		t.Fatalf("expected a malformed-line error for a mapping line missing ':'")
	}
}

// --- stripComment: '#' immediately after a quoted value (not preceded
// by a space) must NOT be treated as a comment start, and a '#' with no
// preceding space at position 0 still strips normally.

func TestStripCommentRespectsQuotesAndLeadingHash(t *testing.T) {
	node, err := Parse(`a: "value#nothash"`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if got := node.Get("a").String(); got != "value#nothash" {
		t.Fatalf("a = %q, want %q (a '#' inside quotes must not start a comment)", got, "value#nothash")
	}

	node2, err := Parse("b: 1 #trailing comment without leading space is still stripped only when preceded by space\nc: 2")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if got := node2.Get("c").String(); got != "2" {
		t.Fatalf("c = %q, want %q", got, "2")
	}
}

// --- parseSequence: inline "- key:" (bare trailing colon, no value on
// the same line) starting a nested mapping under a sequence item.

func TestParseSequenceInlineMappingWithTrailingColon(t *testing.T) {
	src := "items:\n  - name: a\n    nested:\n      x: 1\n  - name: b\n"
	node, err := Parse(src)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	items := node.Get("items").Items()
	if len(items) != 2 {
		t.Fatalf("expected 2 sequence items, got %d", len(items))
	}
	if got := items[0].Get("name").String(); got != "a" {
		t.Fatalf("items[0].name = %q, want %q", got, "a")
	}
	if got := items[0].Get("nested").Get("x").String(); got != "1" {
		t.Fatalf("items[0].nested.x = %q, want %q", got, "1")
	}
}

// --- Quoted-string edge case in unquote: single-character content
// (too short to be a quote pair) must be returned unchanged.

func TestUnquoteLeavesShortOrUnquotedScalarsUnchanged(t *testing.T) {
	node, err := Parse("a: x\nb: \"\"\n")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if got := node.Get("a").String(); got != "x" {
		t.Fatalf("a = %q, want %q", got, "x")
	}
	if got := node.Get("b").String(); got != "" {
		t.Fatalf("b = %q, want empty (empty quoted string)", got)
	}
}

// Package config implements Veriqo's config-driven policy loader — the
// gap named explicitly in Yang_masih_kurang.docx: "config-driven
// policy/reliability/ontology (still hard-coded in Go, not YAML/JSON)".
//
// It provides a minimal, pure-stdlib YAML-subset parser (block mappings,
// block sequences, scalars, comments, 2-space indentation) plus typed
// loaders that turn a YAML file into fusion.SourceProfile and
// decision.Policy values. A hand-rolled parser is a deliberate choice,
// not an oversight: the project's zero-external-dependency invariant
// (enforced across every kernel package, verified via `go list -deps`)
// rules out gopkg.in/yaml.v3, and this sandbox's network egress blocks
// proxy.golang.org regardless. The supported subset is intentionally
// small — exactly what policy/reliability configuration needs — rather
// than a general-purpose YAML implementation.
//
// Supported subset:
//   - Block mappings: "key: value" and nested "key:" followed by an
//     indented block.
//   - Block sequences: "- item" (scalar or mapping items).
//   - Scalars: bare strings, quoted strings, integers, floats, booleans.
//   - Comments: "# ..." to end of line (outside quoted strings).
//   - 2-space indentation increments; tabs are rejected (matches real
//     YAML's own prohibition on tabs for indentation).
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrTabIndentation = errors.New("config: tab characters are not permitted for indentation")
	ErrMalformedLine  = errors.New("config: malformed line")
	ErrEmptyDocument  = errors.New("config: empty document")
)

// Node is a parsed YAML-subset value: exactly one of Scalar, Mapping, or
// Sequence is populated (Kind disambiguates for the zero-value-empty
// case, e.g. an empty string scalar vs an absent node).
type Node struct {
	Kind     NodeKind
	Scalar   string
	Mapping  map[string]*Node
	MapOrder []string // insertion order, since Go maps don't preserve it
	Sequence []*Node
}

type NodeKind int

const (
	KindScalar NodeKind = iota
	KindMapping
	KindSequence
)

type rawLine struct {
	indent  int
	content string
	lineNo  int
}

// Parse parses YAML-subset source text into a root Node.
func Parse(src string) (*Node, error) {
	lines, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, ErrEmptyDocument
	}
	node, _, err := parseBlock(lines, 0, lines[0].indent)
	return node, err
}

func tokenize(src string) ([]rawLine, error) {
	var out []rawLine
	for i, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, "\t") {
			leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			if strings.Contains(leading, "\t") {
				return nil, fmt.Errorf("%w at line %d", ErrTabIndentation, i+1)
			}
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		out = append(out, rawLine{indent: indent, content: strings.TrimRight(line[indent:], " "), lineNo: i + 1})
	}
	return out, nil
}

// stripComment removes a trailing "# ..." comment, respecting quotes.
func stripComment(line string) string {
	inSingle, inDouble := false, false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ') {
				return line[:i]
			}
		}
	}
	return line
}

// parseBlock parses a sequence of lines at a fixed indent level starting
// at index start, returning the constructed Node and the index of the
// next unconsumed line.
func parseBlock(lines []rawLine, start, indent int) (*Node, int, error) {
	if start >= len(lines) || lines[start].indent != indent {
		return nil, start, fmt.Errorf("%w at line %d", ErrMalformedLine, safeLineNo(lines, start))
	}
	if strings.HasPrefix(lines[start].content, "- ") || lines[start].content == "-" {
		return parseSequence(lines, start, indent)
	}
	return parseMapping(lines, start, indent)
}

func safeLineNo(lines []rawLine, idx int) int {
	if idx < len(lines) {
		return lines[idx].lineNo
	}
	if len(lines) > 0 {
		return lines[len(lines)-1].lineNo
	}
	return 0
}

func parseSequence(lines []rawLine, start, indent int) (*Node, int, error) {
	node := &Node{Kind: KindSequence}
	i := start
	for i < len(lines) && lines[i].indent == indent && (strings.HasPrefix(lines[i].content, "- ") || lines[i].content == "-") {
		item := strings.TrimPrefix(lines[i].content, "-")
		item = strings.TrimPrefix(item, " ")
		if strings.TrimSpace(item) == "" {
			// Nested block under "-" on the following, more-indented lines.
			child, next, err := parseBlock(lines, i+1, indent+2)
			if err != nil {
				return nil, i, err
			}
			node.Sequence = append(node.Sequence, child)
			i = next
			continue
		}
		if strings.Contains(item, ": ") || strings.HasSuffix(item, ":") {
			// Inline "- key: value" starts a mapping. Continuation keys
			// (e.g. "reliability: 0.9" on the next line) align with the
			// column right after "- ", i.e. indent+2, not the dash's own
			// indent — so the synthetic first line is normalized to that
			// column too, letting parseMapping treat them as siblings.
			contIndent := indent + 2
			synthetic := []rawLine{{indent: contIndent, content: item, lineNo: lines[i].lineNo}}
			j := i + 1
			for j < len(lines) && lines[j].indent >= contIndent {
				synthetic = append(synthetic, lines[j])
				j++
			}
			child, _, err := parseMapping(synthetic, 0, contIndent)
			if err != nil {
				return nil, i, err
			}
			node.Sequence = append(node.Sequence, child)
			i = j
			continue
		}
		node.Sequence = append(node.Sequence, &Node{Kind: KindScalar, Scalar: unquote(item)})
		i++
	}
	return node, i, nil
}

func parseMapping(lines []rawLine, start, indent int) (*Node, int, error) {
	node := &Node{Kind: KindMapping, Mapping: make(map[string]*Node)}
	i := start
	for i < len(lines) && lines[i].indent == indent {
		line := lines[i].content
		colon := strings.Index(line, ":")
		if colon < 0 {
			return nil, i, fmt.Errorf("%w at line %d: missing ':'", ErrMalformedLine, lines[i].lineNo)
		}
		key := unquote(strings.TrimSpace(line[:colon]))
		val := strings.TrimSpace(line[colon+1:])
		if val == "" {
			// Nested block on subsequent more-indented lines.
			if i+1 < len(lines) && lines[i+1].indent > indent {
				child, next, err := parseBlock(lines, i+1, lines[i+1].indent)
				if err != nil {
					return nil, i, err
				}
				node.Mapping[key] = child
				node.MapOrder = append(node.MapOrder, key)
				i = next
				continue
			}
			node.Mapping[key] = &Node{Kind: KindScalar, Scalar: ""}
			node.MapOrder = append(node.MapOrder, key)
			i++
			continue
		}
		node.Mapping[key] = &Node{Kind: KindScalar, Scalar: unquote(val)}
		node.MapOrder = append(node.MapOrder, key)
		i++
	}
	return node, i, nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

// --- typed accessors -------------------------------------------------

func (n *Node) Get(key string) *Node {
	if n == nil || n.Kind != KindMapping {
		return nil
	}
	return n.Mapping[key]
}

func (n *Node) String() string {
	if n == nil {
		return ""
	}
	return n.Scalar
}

func (n *Node) Float64() (float64, error) {
	if n == nil {
		return 0, fmt.Errorf("config: nil node")
	}
	return strconv.ParseFloat(n.Scalar, 64)
}

func (n *Node) Uint64() (uint64, error) {
	if n == nil {
		return 0, fmt.Errorf("config: nil node")
	}
	return strconv.ParseUint(n.Scalar, 10, 64)
}

func (n *Node) Items() []*Node {
	if n == nil || n.Kind != KindSequence {
		return nil
	}
	return n.Sequence
}

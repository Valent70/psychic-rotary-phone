package coverage

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// IssueRow is one row of a Coverage Issue Matrix — blueprint §19's own
// table shape: an issue, the evidence backing it, the policy clause it
// maps to, and its resolution status.
type IssueRow struct {
	Issue        string     `json:"issue"`
	Evidence     string     `json:"evidence,omitempty"`
	PolicyClause string     `json:"policy_clause,omitempty"`
	Status       FactStatus `json:"status"`
}

var (
	ErrEmptyIssue = errors.New("coverage: IssueRow.Issue must be non-empty")
)

// Validate checks r's own internal consistency.
func (r IssueRow) Validate() error {
	if r.Issue == "" {
		return ErrEmptyIssue
	}
	if !IsKnownStatus(r.Status) {
		return ErrUnknownFactStatus
	}
	return nil
}

// IssueMatrix is the Coverage Issue Matrix blueprint §19 names and
// tables — the dossier's own "13. Coverage Issue Matrix" section
// (§30). It is a thin, ordered, thread-safe collection of IssueRow;
// rows are kept in insertion order because the blueprint's own worked
// table has a meaningful reading order (peril, period, exclusion,
// notice, quantum), not an alphabetical one.
type IssueMatrix struct {
	mu   sync.RWMutex
	rows []IssueRow
}

// NewIssueMatrix returns an empty Coverage Issue Matrix.
func NewIssueMatrix() *IssueMatrix {
	return &IssueMatrix{}
}

// Add appends row to the matrix, in order. Refuses an invalid row
// (empty Issue, or a Status outside the four this package models)
// rather than silently accepting it.
func (m *IssueMatrix) Add(row IssueRow) error {
	if err := row.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, row)
	return nil
}

// Rows returns every row in insertion order.
func (m *IssueMatrix) Rows() []IssueRow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]IssueRow, len(m.rows))
	copy(out, m.rows)
	return out
}

// Len returns the number of rows in the matrix.
func (m *IssueMatrix) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rows)
}

// Render returns the matrix as a plain-text table — one line per row,
// tab-separated columns in the blueprint's own §19 column order (Issue,
// Evidence, Policy clause, Status). Intended for the dossier generator
// (blueprint §30 item 13), not for machine parsing.
func (m *IssueMatrix) Render() string {
	rows := m.Rows()
	var b strings.Builder
	b.WriteString("Issue\tEvidence\tPolicy clause\tStatus\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", r.Issue, r.Evidence, r.PolicyClause, r.Status)
	}
	return b.String()
}

// IssueMatrix renders a CoverageAnalysis's Facts as an *IssueMatrix —
// one row per fact, Evidence populated from EvidenceIDs and
// PolicyClause from ClauseIDs (both joined with ", " when there is
// more than one). This lets a real Analyze() result be presented in
// exactly the §19 table shape, in addition to the matrix being
// buildable directly (see the package's §19 worked-example test).
func (a CoverageAnalysis) IssueMatrix() *IssueMatrix {
	m := NewIssueMatrix()
	for _, f := range a.Facts {
		row := IssueRow{
			Issue:        f.Description,
			Evidence:     strings.Join(f.EvidenceIDs, ", "),
			PolicyClause: strings.Join(f.ClauseIDs, ", "),
			Status:       f.Status,
		}
		// Facts always carry a valid status by construction; Add
		// cannot fail here except on an empty Description, which would
		// itself be a bug worth surfacing loudly rather than
		// swallowing.
		if err := m.Add(row); err != nil {
			panic(fmt.Sprintf("coverage: CoverageAnalysis produced an invalid IssueRow for fact %q: %v", f.FactID, err))
		}
	}
	return m
}

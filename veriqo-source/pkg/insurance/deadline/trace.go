package deadline

import "veriqo/pkg/insurance/policy"

// TracesToClause reports whether r is genuinely traceable to c: a real
// pkg/insurance/policy.Clause, not a bare hard-coded constant. This is
// the concrete mechanism behind blueprint §24's requirement that a
// policy-sourced deadline rule trace back to "policy" as one of its six
// legitimate origins — SourceClause, SourceDocument and
// EffectiveVersion must all match c's ClauseID, DocumentID and Version
// respectively, and r.SourceType must actually declare itself as
// policy-sourced.
//
// A Rule whose SourceType is one of the five non-policy origins (B/L,
// contract, carrier terms, governing law configuration, arbitration
// clause) can never satisfy this — by design, since none of those are
// a policy.Clause. Use those Rules' SourceDocument/SourceClause against
// the corresponding non-policy document store instead.
func (r Rule) TracesToClause(c policy.Clause) bool {
	return r.SourceType == SourceTypePolicy &&
		r.SourceClause == c.ClauseID &&
		r.SourceDocument == c.DocumentID &&
		r.EffectiveVersion == c.Version
}

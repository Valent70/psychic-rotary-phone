// Copy detection: catching corroboration that is really repetition.
//
// # The failure
//
// Fifty sources report the same fact. The corroboration count says
// fifty, the finding is founded, and forty-nine of them are copies of
// the fiftieth. This is the classic route by which a false report
// acquires apparent verification: a putative second source, in a
// connected network, is very often traceable back to the first, and a
// consumer counting producers cannot see it.
//
// The existing graph in this package handles the case where a copy
// DECLARES its origin -- a syndicated article that names the wire, a
// derived dataset that names its input. That is the easy half. The
// hard half is a copy that declares nothing, which is the normal case
// in news, in registry scraping, and in commercial data resale.
//
// # The principle used here
//
// From the truth-discovery literature: sources that share the same
// unusual errors are unlikely to be independent. An error is
// information-bearing in a way agreement is not -- two analysts can
// independently get the right answer, but independently making the
// same typo, the same transposition, the same stale-value carry-over
// is improbable.
//
// The same literature makes the second point this file implements:
// very high similarity between two accounts is evidence of copying
// rather than of corroboration. Two witnesses whose statements match
// word for word have not corroborated each other twice as strongly.
//
// I have implemented the principle. I have not implemented any
// specific published algorithm, and the thresholds below are chosen by
// judgement rather than derived from a validation set -- there is no
// validation set, which is itself one of the things Round 6 says has
// to be bought.
//
// # The refusal that matters
//
// There is no function here that reports two sources as independent.
//
// Finding no shared errors between two sources establishes that no
// shared error was found. It does not establish independence: the
// copy may be clean, the sources may share an upstream neither
// declares, or both may be wrong in the same way for a reason nobody
// has looked for. Demotion only, never promotion -- which is
// UNKNOWN != NEGATIVE, applied to the one dimension where the
// temptation to promote is strongest.
package independence

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoField is an analysis over no comparable fields.
var ErrNoField = errors.New("independence: nothing comparable between these accounts")

// Account is one source's report, as received.
type Account struct {
	// ID identifies the account (an evidence version, an article).
	ID string `json:"id"`
	// Producer is who published it. Two accounts from one producer are
	// trivially dependent and are not the interesting case.
	Producer ProducerID `json:"producer"`
	// Values are the discrete claims: field name to reported value.
	// These are what shared-error analysis runs over.
	Values map[string]string `json:"values,omitempty"`
	// Text is the narrative, when there is one. Near-identical text is
	// evidence of copying regardless of what the values say.
	Text string `json:"text,omitempty"`
}

// Strength is how strongly dependence is indicated. It is ordinal and
// deliberately not a probability: a 0.72 here would be multiplied by
// something downstream and become a confidence.
type Strength string

const (
	// Suspected: one indicator. Enough to stop counting the two as
	// separate producers, not enough to assert a copy.
	Suspected Strength = "SUSPECTED"
	// Strong: several indicators, or one that is hard to produce
	// independently.
	Strong Strength = "STRONG"
)

// Dependence is evidence that two accounts are not separate
// observations.
type Dependence struct {
	A        string   `json:"a"`
	B        string   `json:"b"`
	Strength Strength `json:"strength"`
	// Basis states what was found, specifically enough to argue with.
	Basis []string `json:"basis"`
}

func (d Dependence) String() string {
	return fmt.Sprintf("%s ~ %s [%s] %s", d.A, d.B, d.Strength, strings.Join(d.Basis, "; "))
}

// Policy tunes the analysis.
//
// The defaults are deliberately conservative in the direction of
// under-reporting dependence. A false dependence finding merges two
// genuine sources and silently weakens a finding that deserved its
// corroboration; a missed one leaves the corroboration count where it
// already was. The first error is worse because it is invisible.
type Policy struct {
	// TextContainment above which two narratives are treated as one.
	//
	// The measure is CONTAINMENT -- the shared tokens over the SHORTER
	// account -- not Jaccard. Jaccard is the obvious choice and it is
	// the wrong one here, because it penalises length difference, and
	// the commonest real copy is a wire story reprinted with a
	// paragraph added. On the case in pkg/intel/maritime/flagship, a
	// verbatim reprint plus one added sentence scores 0.84 on Jaccard
	// and 1.00 on containment; a Jaccard threshold tuned to catch it
	// would have to sit so low that unrelated accounts of one event
	// would trip it.
	//
	// Syndication adds material and rarely removes it, so containment
	// is the shape of the thing being detected.
	//
	// 0.85 is a judgement, not a calibration. There is no validation
	// corpus, and a threshold presented as tuned when nothing tuned it
	// would be a fabricated number.
	TextContainment float64
	// MinSharedDeviations is how many shared minority values indicate
	// dependence. One shared deviation is suspicious; the default of 1
	// reflects that a shared deviation is already unlikely.
	MinSharedDeviations int
	// MinAccountsForMajority is how many accounts must report a field
	// before a majority value is meaningful. Below it, "deviation from
	// the majority" is deviation from one other account.
	MinAccountsForMajority int
	// MinTokensForContainment is how long the shorter account must be
	// before containment means anything.
	//
	// Without it the measure is worthless on short text: "A tanker was
	// reported alongside an unidentified vessel east of the strait" is
	// contained in almost any longer account of the same event,
	// because there are only so many ways to write that sentence. A
	// containment finding on twelve generic tokens is a finding about
	// the English language.
	MinTokensForContainment int
}

func DefaultPolicy() Policy {
	return Policy{
		TextContainment: 0.85, MinSharedDeviations: 1,
		MinAccountsForMajority: 3, MinTokensForContainment: 15,
	}
}

// Analysis is what copy detection found.
type Analysis struct {
	Accounts     []Account    `json:"accounts"`
	Dependencies []Dependence `json:"dependencies"`
	// Fields compared, so a reader can see how thin the basis was.
	Fields []string `json:"fields"`
	// UncomparedAccounts are accounts with neither values nor text.
	// They are not independent and not dependent; they are unexamined,
	// and that is reported rather than resolved.
	UncomparedAccounts []string `json:"uncompared_accounts,omitempty"`
}

// Detect runs copy detection over a set of accounts.
func Detect(p Policy, accounts ...Account) (*Analysis, error) {
	if len(accounts) < 2 {
		return nil, errors.New("independence: copy detection needs at least two accounts")
	}
	seen := map[string]bool{}
	for _, a := range accounts {
		if strings.TrimSpace(a.ID) == "" {
			return nil, errors.New("independence: an account with no id")
		}
		if seen[a.ID] {
			return nil, fmt.Errorf("independence: %s appears twice", a.ID)
		}
		seen[a.ID] = true
	}

	an := &Analysis{Accounts: accounts}
	for _, a := range accounts {
		if len(a.Values) == 0 && strings.TrimSpace(a.Text) == "" {
			an.UncomparedAccounts = append(an.UncomparedAccounts, a.ID)
		}
	}

	// Which fields have enough reporters for a majority to mean
	// anything.
	counts := map[string]int{}
	for _, a := range accounts {
		for f := range a.Values {
			counts[f]++
		}
	}
	majority := map[string]string{}
	for f, n := range counts {
		if n < p.MinAccountsForMajority {
			continue
		}
		an.Fields = append(an.Fields, f)
		majority[f] = majorityValue(accounts, f)
	}
	sort.Strings(an.Fields)

	found := map[string]*Dependence{}
	for i := 0; i < len(accounts); i++ {
		for j := i + 1; j < len(accounts); j++ {
			a, b := accounts[i], accounts[j]
			var basis []string

			// Shared deviations: both report the same value for a
			// field, and that value is NOT the majority. Agreeing on
			// the majority is what independent sources do; agreeing on
			// a departure from it is not.
			var shared int
			for _, f := range an.Fields {
				av, aok := a.Values[f]
				bv, bok := b.Values[f]
				if !aok || !bok || !strings.EqualFold(av, bv) {
					continue
				}
				if strings.EqualFold(av, majority[f]) {
					continue
				}
				shared++
				basis = append(basis, fmt.Sprintf("both report %s=%q where the majority "+
					"reports %q", f, av, majority[f]))
			}

			cont, jac := containment(a.Text, b.Text), jaccard(a.Text, b.Text)
			shortest := shorterLen(a.Text, b.Text)
			if a.Text != "" && b.Text != "" && cont >= p.TextContainment &&
				shortest >= p.MinTokensForContainment {
				basis = append(basis, fmt.Sprintf("%.0f%% of the shorter account's tokens "+
					"appear in the longer one (Jaccard %.2f, which is the measure that "+
					"would have missed this): a reprint with material added, not a "+
					"second account", cont*100, jac))
			}

			if shared < p.MinSharedDeviations && len(basis) == 0 {
				continue
			}
			if shared > 0 && shared < p.MinSharedDeviations {
				continue
			}
			st := Suspected
			if len(basis) > 1 {
				st = Strong
			}
			d := &Dependence{A: a.ID, B: b.ID, Strength: st, Basis: basis}
			found[a.ID+"|"+b.ID] = d
		}
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		an.Dependencies = append(an.Dependencies, *found[k])
	}
	return an, nil
}

// Clusters groups accounts that copy detection could not separate.
//
// Each cluster counts as ONE observation for corroboration purposes,
// which is the whole point: fifty accounts in one cluster corroborate
// nothing.
func (an *Analysis) Clusters() [][]string {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" || parent[x] == x {
			parent[x] = x
			return x
		}
		parent[x] = find(parent[x])
		return parent[x]
	}
	for _, a := range an.Accounts {
		parent[a.ID] = a.ID
	}
	for _, d := range an.Dependencies {
		ra, rb := find(d.A), find(d.B)
		if ra != rb {
			parent[ra] = rb
		}
	}
	groups := map[string][]string{}
	for _, a := range an.Accounts {
		r := find(a.ID)
		groups[r] = append(groups[r], a.ID)
	}
	var out [][]string
	for _, g := range groups {
		sort.Strings(g)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// EffectiveCount is how many separate observations survive.
//
// It is the cluster count, never the account count. The second return
// is the statement a reader needs beside it, because the number alone
// invites the reading this function exists to prevent: that the
// surviving clusters have been shown to be independent of each other.
// They have not. They have been shown not to be caught.
func (an *Analysis) EffectiveCount() (int, string) {
	c := len(an.Clusters())
	stmt := fmt.Sprintf("%d account(s) reduce to %d observation(s) that copy detection "+
		"could not merge. This is NOT a finding that those %d are independent: it is a "+
		"finding that no shared deviation and no near-identical text was found between "+
		"them. An undeclared common upstream would look exactly like this.",
		len(an.Accounts), c, c)
	if len(an.UncomparedAccounts) > 0 {
		stmt += fmt.Sprintf(" %d account(s) carried neither values nor text and were "+
			"not examined at all.", len(an.UncomparedAccounts))
	}
	if len(an.Fields) == 0 {
		stmt += " No field had enough reporters for a majority, so shared-deviation " +
			"analysis did not run; only text similarity was applied."
	}
	return c, stmt
}

func (an *Analysis) Report() string {
	var b strings.Builder
	b.WriteString("COPY DETECTION\n")
	fmt.Fprintf(&b, "  %d account(s), %d comparable field(s): %s\n",
		len(an.Accounts), len(an.Fields), strings.Join(an.Fields, ", "))
	if len(an.Dependencies) == 0 {
		b.WriteString("\n  No dependence indicator was found between any pair.\n")
	}
	for _, d := range an.Dependencies {
		fmt.Fprintf(&b, "\n  %-9s %s ~ %s\n", d.Strength, d.A, d.B)
		for _, r := range d.Basis {
			b.WriteString(wrapC("    - ", r))
		}
	}
	b.WriteString("\n  CLUSTERS -- each counts as one observation\n")
	for _, c := range an.Clusters() {
		fmt.Fprintf(&b, "    [%s]\n", strings.Join(c, ", "))
	}
	n, stmt := an.EffectiveCount()
	fmt.Fprintf(&b, "\n  EFFECTIVE OBSERVATIONS: %d\n", n)
	b.WriteString(wrapC("  ", stmt))
	return b.String()
}

func majorityValue(accounts []Account, field string) string {
	counts := map[string]int{}
	for _, a := range accounts {
		if v, ok := a.Values[field]; ok {
			counts[strings.ToLower(v)]++
		}
	}
	best, n := "", 0
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic on ties
	for _, k := range keys {
		if counts[k] > n {
			best, n = k, counts[k]
		}
	}
	return best
}

// containment is the shared tokens over the shorter account.
//
// It answers "is the smaller account contained in the larger one",
// which is what syndication looks like. It is asymmetric in intent and
// symmetric in code: min() picks the shorter side either way.
//
// It is deliberately crude -- token-level, no shingling, no stopword
// handling. A sophisticated measure would be tuned against a corpus,
// there is no corpus, and a tuned-looking number over no corpus is
// worse than an obviously rough one.
func containment(a, b string) float64 {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := overlap(ta, tb)
	shorter := len(ta)
	if len(tb) < shorter {
		shorter = len(tb)
	}
	return float64(inter) / float64(shorter)
}

// jaccard is reported alongside containment so a reader can see the
// gap between them. It is not used for the decision.
func jaccard(a, b string) float64 {
	ta, tb := tokens(a), tokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := overlap(ta, tb)
	union := len(ta) + len(tb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// shorterLen is the token count of the shorter of two accounts.
func shorterLen(a, b string) int {
	na, nb := len(tokens(a)), len(tokens(b))
	if nb < na {
		return nb
	}
	return na
}

func overlap(ta, tb map[string]bool) int {
	n := 0
	for t := range ta {
		if tb[t] {
			n++
		}
	}
	return n
}

func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.Fields(strings.ToLower(s)) {
		f = strings.Trim(f, ".,;:!?\"'()[]")
		if f != "" {
			out[f] = true
		}
	}
	return out
}

func wrapC(label, text string) string {
	const width = 78
	indent := strings.Repeat(" ", len(label))
	var b strings.Builder
	line := label
	for i, word := range strings.Fields(text) {
		if i > 0 && len(line)+1+len(word) > width {
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			line = indent
		} else if i > 0 {
			line += " "
		}
		line += word
	}
	b.WriteString(strings.TrimRight(line, " ") + "\n")
	return b.String()
}

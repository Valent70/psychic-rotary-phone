package corpus

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/evidence/redaction/worker"
)

// Run builds every fixture, puts it through the worker, and reports
// what happened against what the design says should happen.
//
// # What the three outcomes mean here
//
//	ACCEPTED  a derivative was produced AND verified against its own
//	          inspectable view
//	REFUSED   the worker declined, by design, and said which structure
//	FAILED    anything else -- a defect, always
//
// # Why the comparison matters more than the count
//
// A variant that flips from ACCEPTED to REFUSED is a regression, and
// one that was always refused is a gap. Both show as "not accepted" in
// a coverage ratio and only one of them is a bug. Mismatched carries
// that distinction; a non-zero Mismatched is never a tuning parameter.
//
// # The leak check
//
// Leaked is the outcome that matters more than every other number in
// this file combined. It is populated only from the derivative's
// INSPECTABLE view (FC-002), and any non-zero value means a forbidden
// term survived into something VERIQO would have released.
func Run() ([]Outcome, Coverage, error) {
	outcomes := make([]Outcome, 0, len(Variants))
	cov := Coverage{Total: len(Variants)}

	for _, v := range Variants {
		o := Outcome{Variant: v}

		doc, err := Build(v)
		if err != nil {
			return nil, Coverage{}, fmt.Errorf("corpus: building %s: %w", v.ID, err)
		}

		// FC-005: a container fixture must genuinely hide its term.
		// This is checked here as well as in the tests, so a
		// downgraded fixture cannot make the corpus look healthy in a
		// report that nobody ran the tests alongside.
		if v.TermUnreadable && bytes.Contains(doc, []byte(Term)) &&
			v.ID != "PDF-MALFORMED" && v.ID != "PDF-INCREMENTAL" {
			return nil, Coverage{}, fmt.Errorf(
				"corpus: fixture %s is marked unreadable and carries the term in plain bytes; "+
					"this is FC-005 recurring", v.ID)
		}

		derivative, manifest, err := worker.Redact(v.Kind, doc, []string{Term})
		switch {
		case err == nil:
			o.Actual = Accepted
			o.Detail = fmt.Sprintf("%d replacement(s) across %d part(s); verified=%v",
				manifest.Replacements, manifest.PartsProcessed, manifest.Verified)
			if !manifest.Verified {
				o.Actual = Failed
				o.Detail = "a derivative was produced and never verified"
			}
			// The leak check, run again here over the derivative that
			// would actually be released.
			view, verr := worker.Inspect(v.Kind, derivative)
			if verr != nil {
				o.Actual = Failed
				o.Detail = "the released derivative cannot be inspected: " + verr.Error()
			} else if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(Term))) {
				o.Leaked = true
				o.LeakNote = "the forbidden term is present in the derivative's inspectable view"
			}
		case worker.IsRefusal(err):
			o.Actual = Refused
			o.Detail = err.Error()
		default:
			o.Actual = Failed
			o.Detail = err.Error()
		}

		o.Matches = o.Actual == v.Expected
		if !o.Matches {
			cov.Mismatched = append(cov.Mismatched, v.ID)
		}
		if o.Leaked {
			cov.Leaked = append(cov.Leaked, v.ID)
		}

		switch o.Actual {
		case Accepted:
			cov.Accepted++
		case Refused:
			cov.Refused++
		default:
			cov.Failed++
		}

		w := v.RealWorldWeight.Prevalence()
		cov.WeightedTotal += w
		if o.Actual == Accepted {
			cov.WeightedSupported += w
		} else if v.RealWorldWeight == Ubiquitous || v.RealWorldWeight == Common {
			cov.WeightedGap = append(cov.WeightedGap,
				fmt.Sprintf("%s (%s)", v.ID, v.RealWorldWeight))
		}

		outcomes = append(outcomes, o)
	}

	sort.Strings(cov.Mismatched)
	sort.Strings(cov.Leaked)
	sort.Strings(cov.WeightedGap)
	return outcomes, cov, nil
}

// RunExternal runs the same worker over documents VERIQO did not
// create, if VERIQO_CORPUS_DIR names any.
//
// It is the L3 path. It returns (nil, false, nil) when no corpus is
// configured, which is this repository's actual state -- and the
// distinction between "no corpus" and "a corpus that found nothing" is
// the whole reason it returns a boolean rather than an empty slice.
func RunExternal() ([]ExternalOutcome, bool, error) {
	paths, configured := ExternalCorpus()
	if !configured {
		return nil, false, nil
	}
	out := make([]ExternalOutcome, 0, len(paths))
	for _, p := range paths {
		kind, ok := KindOf(p)
		if !ok {
			continue
		}
		out = append(out, ExternalOutcome{Path: p, Kind: kind,
			Note: "the file was enumerated; redaction over external documents requires a term " +
				"list supplied per document, which the caller provides"})
	}
	return out, true, nil
}

// ExternalOutcome is one document from a real corpus.
type ExternalOutcome struct {
	Path string
	Kind worker.Kind
	Note string
}

// QualificationLevel states how far the corpus has been taken.
//
// It exists so that a green run cannot be quoted as external
// validation. L2 is where this repository is and L3 is where it is not.
type QualificationLevel string

const (
	// L1: the workers run without crashing.
	L1 QualificationLevel = "L1_EXECUTES"
	// L2: fixtures VERIQO built exercise the structures VERIQO named,
	// and the results match the stated design.
	L2 QualificationLevel = "L2_FIXTURE_CORRECTNESS"
	// L3: documents VERIQO did not create.
	L3 QualificationLevel = "L3_INDEPENDENT_CORPUS"
	// L4: an outside party attempted to recover redacted content.
	L4 QualificationLevel = "L4_ADVERSARIAL_RECOVERY"
)

// Level reports the highest level the current run supports.
func Level() QualificationLevel {
	if _, configured := ExternalCorpus(); configured {
		return L3
	}
	return L2
}

// LevelStatement is the sentence that must accompany any coverage
// figure quoted outside this repository.
func LevelStatement() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Corpus qualification level: %s. ", Level())
	if Level() == L2 {
		b.WriteString("Every fixture was built by VERIQO. No document in this run came from " +
			"outside VERIQO, and no party outside VERIQO has attempted to recover redacted " +
			"content from a derivative. Surviving one's own corpus is the weakest form of " +
			"survival available.")
	}
	return b.String()
}

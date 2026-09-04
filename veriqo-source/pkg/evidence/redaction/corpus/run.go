package corpus

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"veriqo/pkg/evidence/redaction"
	"veriqo/pkg/evidence/redaction/worker"
)

// TermFor returns the forbidden term a variant is built around.
//
// One variant uses a different term because its whole point is the
// XML-escaping of an ampersand.
func TermFor(v Variant) string {
	if v.ID == "XLSX-ESCAPED-XML" {
		return "R&D Partners"
	}
	return "Acme Holdings Ltd"
}

// Run executes every variant through the real pipeline and measures
// what happened.
//
// It never treats a refusal as a failure and never treats an acceptance
// as a success: both are recorded, and the comparison against the
// variant's declared expectation is what produces a verdict.
func Run() ([]Outcome, Coverage, error) {
	p := worker.NewPipeline()
	var outcomes []Outcome
	cov := Coverage{Total: len(Variants)}

	for _, v := range Variants {
		term := TermFor(v)
		doc, err := Build(v, term)
		if err != nil {
			return nil, Coverage{}, fmt.Errorf("building %s: %w", v.ID, err)
		}

		o := Outcome{Variant: v}
		rel, runErr := p.Run(worker.Request{
			Kind:                v.Kind,
			Original:            doc,
			OriginalVersionID:   "CORPUS-" + v.ID,
			DerivativeVersionID: "CORPUS-" + v.ID + "-R1",
			PinnedOriginalHash:  redaction.Hash(doc),
			ForbiddenTerms:      []string{term},
		})

		switch {
		case runErr == nil:
			o.Actual, o.Detail = Accepted, "verified derivative released"
			// A released derivative must not carry the term. This is
			// checked here as well as inside the pipeline, because a
			// corpus run that trusted the pipeline's own verdict would
			// be measuring the pipeline against itself.
			view, verr := worker.Inspectable(v.Kind, rel.Derivative())
			if verr != nil {
				o.Actual, o.Detail = Failed, "released a derivative that cannot be inspected: "+verr.Error()
			} else if bytes.Contains(bytes.ToLower(view), bytes.ToLower([]byte(term))) {
				o.Leaked, o.LeakNote = true, "the forbidden term survives in the released derivative"
				o.Actual, o.Detail = Failed, o.LeakNote
			}
		case errors.Is(runErr, worker.ErrRefused),
			errors.Is(runErr, worker.ErrUnsupportedKind),
			errors.Is(runErr, worker.ErrVerifyFailed):
			o.Actual, o.Detail = Refused, runErr.Error()
		default:
			o.Actual, o.Detail = Failed, runErr.Error()
		}

		o.Matches = o.Actual == v.Expected
		// Prevalence-weighted accumulation. A variant contributes its
		// estimated prevalence to the total, and to the supported sum
		// only if the worker actually redacted it. A REFUSED variant
		// contributes to the denominator and not the numerator, which
		// is precisely the asymmetry a structural count loses.
		w := v.RealWorldWeight.Prevalence()
		cov.WeightedTotal += w
		if o.Actual == Accepted {
			cov.WeightedSupported += w
		}
		switch o.Actual {
		case Accepted:
			cov.Accepted++
		case Refused:
			cov.Refused++
		default:
			cov.Failed++
		}
		if !o.Matches {
			cov.Mismatched = append(cov.Mismatched, fmt.Sprintf("%s: expected %s, got %s (%s)",
				v.ID, v.Expected, o.Actual, o.Detail))
		}
		if o.Leaked {
			cov.Leaked = append(cov.Leaked, v.ID)
		}
		if o.Actual == Refused && (v.RealWorldWeight == Ubiquitous || v.RealWorldWeight == Common) {
			cov.WeightedGap = append(cov.WeightedGap, fmt.Sprintf("%s (%s)", v.ID, v.RealWorldWeight))
		}
		outcomes = append(outcomes, o)
	}

	sort.Strings(cov.Mismatched)
	sort.Strings(cov.Leaked)
	sort.Strings(cov.WeightedGap)
	return outcomes, cov, nil
}

// RunExternal executes the same pipeline over a corpus VERIQO did not
// create, if one is configured.
//
// The second return value is false when no corpus is configured, which
// is this repository's state. Distinguishing "ran and found nothing
// wrong" from "did not run" is the whole point: a silent zero would
// read as a clean corpus result.
func RunExternal() (Coverage, bool, error) {
	files, configured := ExternalCorpus()
	if !configured {
		return Coverage{}, false, nil
	}
	p := worker.NewPipeline()
	cov := Coverage{}
	for _, f := range files {
		kind, ok := KindOf(f)
		if !ok {
			continue
		}
		cov.Total++
		doc, err := readFile(f)
		if err != nil {
			cov.Failed++
			cov.Mismatched = append(cov.Mismatched, f+": "+err.Error())
			continue
		}
		// A real corpus carries no known forbidden term, so the term
		// is drawn from the document itself: the first line of its
		// inspectable view long enough to be distinctive. This makes
		// the run a genuine exercise of the machinery rather than a
		// search for a string nobody put there.
		term, ok := representativeTerm(kind, doc)
		if !ok {
			cov.Failed++
			cov.Mismatched = append(cov.Mismatched, f+": no representative term could be drawn from the document")
			continue
		}
		_, runErr := p.Run(worker.Request{
			Kind: kind, Original: doc,
			OriginalVersionID: "EXT-" + f, DerivativeVersionID: "EXT-" + f + "-R1",
			PinnedOriginalHash: redaction.Hash(doc), ForbiddenTerms: []string{term},
		})
		switch {
		case runErr == nil:
			cov.Accepted++
		case errors.Is(runErr, worker.ErrRefused), errors.Is(runErr, worker.ErrVerifyFailed):
			cov.Refused++
		default:
			cov.Failed++
		}
	}
	return cov, true, nil
}

// representativeTerm draws a distinctive string from a document so that
// an external corpus can be exercised without knowing its contents.
func representativeTerm(kind worker.Kind, doc []byte) (string, bool) {
	view, err := worker.Inspectable(kind, doc)
	if err != nil {
		return "", false
	}
	for _, field := range bytes.FieldsFunc(view, func(r rune) bool {
		return r < 'A' || (r > 'Z' && r < 'a') || r > 'z'
	}) {
		if len(field) >= 12 {
			return string(field), true
		}
	}
	return "", false
}

// ExternalCorpusStatus states, in one line, whether L3 was attempted.
func ExternalCorpusStatus() string {
	files, configured := ExternalCorpus()
	if !configured {
		return fmt.Sprintf("L3 NOT ATTEMPTED: %s is unset, so no corpus of documents VERIQO did not "+
			"create was available. Every result below is L2, over containers this package generated.",
			ExternalCorpusDir)
	}
	return fmt.Sprintf("L3 attempted over %d document(s) from %s.", len(files), ExternalCorpusDir)
}

// Assess turns a coverage result into the verdict sentence, refusing
// the two readings that would be misleading.
func Assess(cov Coverage) string {
	var b strings.Builder
	b.WriteString(cov.Headline())
	b.WriteString("\n\n")
	b.WriteString(ExternalCorpusStatus())
	b.WriteString("\n\nWhat this does NOT establish: that VERIQO can redact the real-world document\n")
	b.WriteString("population. The refused variants above are safe outcomes, and several of them\n")
	b.WriteString("are UBIQUITOUS in documents produced since PDF 1.5. A corpus drawn from real\n")
	b.WriteString("documents would land in those buckets in bulk, and no run in this repository\n")
	b.WriteString("has measured that.\n")
	return b.String()
}

// readFile is separated so the external path has one place that touches
// the filesystem.
func readFile(p string) ([]byte, error) { return os.ReadFile(p) }

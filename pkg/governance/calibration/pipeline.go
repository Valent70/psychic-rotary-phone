package calibration

// PHASE F2 (P1-10) — the real-world calibration interface.
//
// The program is explicit that this phase is "pure infrastructure, not
// data-sourcing (do not go looking for real data)". So this file builds
// the machinery a real corpus would arrive through, and nothing here
// invents, downloads, synthesises or approximates a corpus.
//
// Reconciliation first, per rule 0. Of the nine stages the program
// names, four already existed in corpus.go and are NOT rebuilt:
//
//	Real Event   -> LabeledEvent           (exists)
//	Ground Truth -> LabeledEvent.TrueState (exists)
//	Label        -> LabeledEvent.Value     (exists)
//	Dataset      -> Dataset + Dataset.Hash (exists)
//	Fit          -> Fit()                  (exists: real frequentist MLE)
//
// and one more exists elsewhere and is reused rather than reimplemented:
//
//	Evaluation   -> pkg/moat/reliability's LogLoss / BrierScore /
//	                ExpectedCalibrationError, which are real, tested
//	                metrics this repository already ships.
//
// Three genuinely did not exist anywhere:
//
//	Holdout      -> a deterministic train/test split, so a table is
//	                never evaluated on the same events it was fit on.
//	Model        -> a single object binding a fitted table, the split it
//	                came from, and its held-out evaluation together, so
//	                none of the three can be quoted without the others.
//	Outcome      -> CorpusStatus: the honest, machine-readable answer to
//	                "is this calibration backed by real data yet".
//
// The status is the point of the whole file. Until a real corpus is
// supplied, every model this pipeline produces reports
// EXTERNAL_DATA_REQUIRED, and no amount of running the machinery
// changes that — exactly as the program requires. A fixture corpus can
// exercise every line of code here and the status still says the data
// is missing, because the data IS missing.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/moat/hbayes"
	"veriqo/pkg/moat/reliability"
)

// CorpusStatus is the outcome half of this pipeline: whether the
// calibration behind a model is backed by a real corpus, or by a
// clearly-labelled fixture that proves the machinery runs.
type CorpusStatus string

const (
	// StatusExternalDataRequired is the standing, honest answer for
	// every model this repository can currently produce. It is NOT a
	// failure — the machinery works — it is a statement that the input
	// this machinery needs is a real labeled historical corpus, which is
	// a data-acquisition action (a commercial contract, or years of this
	// system's own resolved case history), not an engineering one.
	StatusExternalDataRequired CorpusStatus = "EXTERNAL_DATA_REQUIRED"
	// StatusRealCorpusFitted is reachable only when a corpus is
	// explicitly declared real by an operator AND meets the pipeline's
	// own sample-count and holdout requirements. Nothing in this
	// repository declares one, and nothing here can: see
	// CorpusDeclaration.
	StatusRealCorpusFitted CorpusStatus = "REAL_CORPUS_FITTED"
	// StatusInsufficientCorpus is a real corpus that is too small to fit
	// or to hold out from. Distinct from EXTERNAL_DATA_REQUIRED: data
	// arrived, and there is not enough of it.
	StatusInsufficientCorpus CorpusStatus = "INSUFFICIENT_CORPUS"
)

// Provenance classifies where a corpus came from. It is deliberately a
// tiny, closed vocabulary with no "unknown" member: a corpus whose
// origin nobody can state is a fixture, and must be declared as one.
type Provenance string

const (
	// ProvenanceFixture is a hand-constructed or synthetic corpus that
	// exists to prove code runs. It can NEVER produce
	// REAL_CORPUS_FITTED.
	ProvenanceFixture Provenance = "FIXTURE"
	// ProvenanceRealInvestigated is a corpus of genuinely investigated,
	// ground-truth-labeled historical events. Declaring one is an
	// operator action with a named owner behind it.
	ProvenanceRealInvestigated Provenance = "REAL_INVESTIGATED"
)

// Errors.
var (
	ErrCorpusUndeclared   = errors.New("calibration: a corpus must declare its provenance and owner before it can be fitted")
	ErrHoldoutTooSmall    = errors.New("calibration: holdout split leaves too few events on one side to be meaningful")
	ErrHoldoutFraction    = errors.New("calibration: holdout fraction must be in (0,1)")
	ErrNoEvaluationSample = errors.New("calibration: holdout produced no evaluable sample for this predicate")
)

// CorpusDeclaration is what an operator must state about a corpus
// before this pipeline will fit anything from it. There is no default:
// a zero-value declaration is refused.
//
// Provenance is the load-bearing field, and it is deliberately supplied
// by a human rather than inferred. No code can look at a slice of
// LabeledEvents and tell whether they describe real investigated
// history or something someone generated — so this pipeline does not
// pretend it can, and asks instead.
type CorpusDeclaration struct {
	Provenance Provenance `json:"provenance"`
	// Owner is the person or team accountable for the claim Provenance
	// makes. Mandatory for both provenance kinds: a fixture with no
	// owner is how a fixture quietly becomes load-bearing.
	Owner string `json:"owner"`
	// Description states, in the declarer's own words, what this corpus
	// is. For a FIXTURE it must say so plainly.
	Description string `json:"description"`
}

func (d CorpusDeclaration) validate() error {
	switch d.Provenance {
	case ProvenanceFixture, ProvenanceRealInvestigated:
	default:
		return fmt.Errorf("%w: provenance %q", ErrCorpusUndeclared, d.Provenance)
	}
	if strings.TrimSpace(d.Owner) == "" {
		return fmt.Errorf("%w: owner", ErrCorpusUndeclared)
	}
	if strings.TrimSpace(d.Description) == "" {
		return fmt.Errorf("%w: description", ErrCorpusUndeclared)
	}
	return nil
}

// Holdout is a deterministic split of a Dataset into a fitting set and
// an evaluation set. Deterministic matters: this repository has no wall
// clock and no unseeded randomness anywhere in its deterministic
// packages, and a split that differed between two runs would make a
// model's evaluation unreproducible.
//
// The split is by a hash of each event's own identity, not by position,
// so adding an event to the middle of a corpus does not reshuffle every
// other event across the boundary.
type Holdout struct {
	Train Dataset `json:"train"`
	Test  Dataset `json:"test"`
	// Fraction is the requested test fraction; Actual is what the
	// hash-based assignment really produced, which will not be exactly
	// Fraction for a small corpus. Reporting both stops a reader
	// assuming an exact split happened.
	Fraction float64 `json:"fraction"`
	Actual   float64 `json:"actual_test_fraction"`
}

// Split partitions ds deterministically. minPerSide refuses a split
// that would leave either side too small to mean anything, rather than
// producing a "holdout" of two events.
func Split(ds Dataset, fraction float64, minPerSide int) (Holdout, error) {
	if fraction <= 0 || fraction >= 1 {
		return Holdout{}, fmt.Errorf("%w: %.4f", ErrHoldoutFraction, fraction)
	}
	h := Holdout{
		Train:    Dataset{Name: ds.Name + "/train"},
		Test:     Dataset{Name: ds.Name + "/test"},
		Fraction: fraction,
	}
	for _, e := range ds.Events {
		if eventBucket(e) < fraction {
			h.Test.Events = append(h.Test.Events, e)
		} else {
			h.Train.Events = append(h.Train.Events, e)
		}
	}
	if len(h.Train.Events) < minPerSide || len(h.Test.Events) < minPerSide {
		return Holdout{}, fmt.Errorf("%w: train=%d test=%d, need at least %d on each side",
			ErrHoldoutTooSmall, len(h.Train.Events), len(h.Test.Events), minPerSide)
	}
	if total := len(ds.Events); total > 0 {
		h.Actual = float64(len(h.Test.Events)) / float64(total)
	}
	return h, nil
}

// eventBucket maps one event deterministically into [0,1) using a hash
// of its own identity. Two corpora containing the same event always
// assign it to the same side.
func eventBucket(e LabeledEvent) float64 {
	sum := sha256.Sum256([]byte("veriqo.calibration.holdout/v1|" + e.EventID + "|" + e.Predicate))
	// Use the first 8 bytes as a big-endian unsigned value scaled into
	// [0,1). 2^53 keeps the division exact in float64.
	var v uint64
	for i := 0; i < 7; i++ {
		v = v<<8 | uint64(sum[i])
	}
	return float64(v%(1<<53)) / float64(uint64(1)<<53)
}

// Evaluation is a fitted table's measured performance on events it was
// NOT fit on. The three metrics are computed by pkg/moat/reliability,
// which already implements and tests them; this type does not
// reimplement any metric.
type Evaluation struct {
	Predicate string `json:"predicate"`
	// TargetState is the hidden state whose probability was scored. A
	// multi-state model is evaluated one state at a time, because
	// reliability's metrics are binary by construction and pretending
	// otherwise would be a silent approximation.
	TargetState string  `json:"target_state"`
	Samples     int     `json:"samples"`
	LogLoss     float64 `json:"log_loss"`
	Brier       float64 `json:"brier"`
	ECE         float64 `json:"expected_calibration_error"`
	Bins        int     `json:"bins"`
}

// Evaluate scores table against a held-out dataset for one target
// state. Every sample's predicted probability is the posterior the
// table itself implies for that event's observed value — so this
// measures the fitted table, not some separate scoring function.
func Evaluate(table LikelihoodTable, test Dataset, target hbayes.State, bins int) (Evaluation, error) {
	if bins <= 0 {
		bins = 10
	}
	var samples []reliability.Sample
	for _, e := range test.Events {
		if e.Predicate != table.Predicate {
			continue
		}
		dist, ok := table.Likelihood[e.Value]
		if !ok {
			// A held-out value the training set never contained. Skipping
			// it is correct and is REPORTED via Samples below: the
			// alternative — assigning it a made-up probability — would be
			// inventing evidence.
			continue
		}
		p := posteriorFor(dist, table.Record.Prior, target)
		samples = append(samples, reliability.Sample{
			ClaimKey:  table.Predicate + "|" + e.EventID,
			Predicted: p,
			Outcome:   e.TrueState == target,
		})
	}
	if len(samples) == 0 {
		return Evaluation{}, fmt.Errorf("%w: predicate %q state %q", ErrNoEvaluationSample, table.Predicate, target)
	}

	logLoss, err := reliability.LogLoss(samples)
	if err != nil {
		return Evaluation{}, err
	}
	brier, err := reliability.BrierScore(samples)
	if err != nil {
		return Evaluation{}, err
	}
	ece, err := reliability.ExpectedCalibrationError(samples, bins)
	if err != nil {
		return Evaluation{}, err
	}
	return Evaluation{
		Predicate: table.Predicate, TargetState: string(target), Samples: len(samples),
		LogLoss: logLoss, Brier: brier, ECE: ece, Bins: bins,
	}, nil
}

// posteriorFor applies Bayes' rule for one observed value: P(target |
// value) = P(value | target)·P(target) / sum_s P(value | s)·P(s). It is
// deliberately computed here from the table's OWN likelihood and prior
// rather than delegated, so the evaluation scores exactly the numbers
// the table would contribute in production.
func posteriorFor(dist map[hbayes.State]float64, prior map[hbayes.State]float64, target hbayes.State) float64 {
	denom := 0.0
	for s, p := range prior {
		denom += dist[s] * p
	}
	if denom <= 0 {
		// Every state assigns this value zero likelihood. Returning the
		// prior is the only non-inventing answer: the observation carries
		// no information under this table.
		return prior[target]
	}
	p := dist[target] * prior[target] / denom
	switch {
	case p < 0:
		return 0
	case p > 1:
		return 1
	default:
		return p
	}
}

// CalibratedModel binds a fitted table to the split it came from and to
// its held-out evaluation, so none of the three can be quoted without
// the others. This is the "Model" stage the program names, and the
// binding is the point: a likelihood table with no stated evaluation,
// or an evaluation with no stated corpus, is precisely the kind of
// free-floating number this project refuses to ship.
type CalibratedModel struct {
	Table       LikelihoodTable   `json:"table"`
	Declaration CorpusDeclaration `json:"declaration"`
	Holdout     Holdout           `json:"holdout"`
	Evaluations []Evaluation      `json:"evaluations"`
	// Status is DERIVED (see deriveStatus). There is no setter, and no
	// argument to BuildModel that can raise it.
	Status CorpusStatus `json:"status"`
	// Limitations states what this model does NOT establish. It is
	// mandatory and non-empty for every non-real corpus.
	Limitations []string `json:"limitations,omitempty"`
	Hash        string   `json:"hash"`
}

// BuildModel runs the whole pipeline: declare -> split -> fit on train
// -> evaluate on test -> derive status. It is the only constructor for
// a CalibratedModel.
//
// It cannot produce REAL_CORPUS_FITTED for a fixture no matter what
// arguments it is given, because Status is derived from Declaration
// .Provenance, and nothing in this repository declares
// REAL_INVESTIGATED.
func BuildModel(predicate string, ds Dataset, decl CorpusDeclaration, states []hbayes.State,
	modelVersion string, effectiveTick uint64, minSamples int, holdoutFraction float64, minPerSide int) (CalibratedModel, error) {

	if err := decl.validate(); err != nil {
		return CalibratedModel{}, err
	}
	holdout, err := Split(ds, holdoutFraction, minPerSide)
	if err != nil {
		return CalibratedModel{}, err
	}
	// Fit on the TRAIN half only. Fitting on the whole corpus and then
	// "evaluating" on part of it would report a number that means
	// nothing, which is worse than reporting none.
	source := string(decl.Provenance) + ":" + decl.Description
	table, err := Fit(predicate, holdout.Train, states, source, modelVersion, effectiveTick, minSamples)
	if err != nil {
		return CalibratedModel{}, err
	}

	m := CalibratedModel{Table: table, Declaration: decl, Holdout: holdout}
	for _, s := range states {
		ev, err := Evaluate(table, holdout.Test, s, 10)
		if err != nil {
			// A state with no evaluable held-out sample is reported by its
			// ABSENCE from Evaluations, and deriveStatus below treats an
			// incomplete evaluation as insufficient rather than fine.
			continue
		}
		m.Evaluations = append(m.Evaluations, ev)
	}
	sort.Slice(m.Evaluations, func(i, j int) bool { return m.Evaluations[i].TargetState < m.Evaluations[j].TargetState })

	m.Status, m.Limitations = deriveStatus(decl, states, m.Evaluations)
	m.Hash = modelHash(m)
	return m, nil
}

// deriveStatus is the honesty core of this file. It reads only the
// declaration and the evaluation completeness; there is no path by
// which running the machinery harder produces a stronger status.
func deriveStatus(decl CorpusDeclaration, states []hbayes.State, evals []Evaluation) (CorpusStatus, []string) {
	if decl.Provenance != ProvenanceRealInvestigated {
		return StatusExternalDataRequired, []string{
			"corpus provenance is " + string(decl.Provenance) + ": this model demonstrates that the fitting, " +
				"holdout and evaluation machinery runs, and establishes nothing whatsoever about real-world " +
				"calibration accuracy",
			"a real labeled historical corpus is a data-acquisition action (a commercial contract, or years of " +
				"this system's own resolved case history), not an engineering one",
		}
	}
	if len(evals) < len(states) {
		return StatusInsufficientCorpus, []string{
			fmt.Sprintf("only %d of %d declared hidden states had an evaluable held-out sample; "+
				"a model that cannot be scored on every state it claims to model is not qualified", len(evals), len(states)),
		}
	}
	return StatusRealCorpusFitted, nil
}

func modelHash(m CalibratedModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "veriqo.calibration.model/v1\npredicate=%s\nprovenance=%s\nowner=%s\n",
		m.Table.Predicate, m.Declaration.Provenance, m.Declaration.Owner)
	fmt.Fprintf(&b, "train=%s\ntest=%s\nfraction=%.9f\nactual=%.9f\n",
		m.Holdout.Train.Hash(), m.Holdout.Test.Hash(), m.Holdout.Fraction, m.Holdout.Actual)
	fmt.Fprintf(&b, "dataset_provenance=%s\nmodel_version=%s\n",
		m.Table.Record.DatasetProvenance, m.Table.Record.ModelVersion)
	for _, e := range m.Evaluations {
		fmt.Fprintf(&b, "eval.%s=n%d/ll%.9f/br%.9f/ece%.9f\n",
			e.TargetState, e.Samples, e.LogLoss, e.Brier, e.ECE)
	}
	fmt.Fprintf(&b, "status=%s\n", m.Status)
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Qualified reports whether this model may be used as production
// calibration evidence. Only one status qualifies, and it is not
// reachable from anything in this repository today.
func (m CalibratedModel) Qualified() bool { return m.Status == StatusRealCorpusFitted }

// Assert converts an unqualified model into an error, so a caller
// cannot read Status and use the table anyway.
func (m CalibratedModel) Assert() error {
	if m.Qualified() {
		return nil
	}
	return fmt.Errorf("calibration: model for %q is %s: %s",
		m.Table.Predicate, m.Status, strings.Join(m.Limitations, "; "))
}

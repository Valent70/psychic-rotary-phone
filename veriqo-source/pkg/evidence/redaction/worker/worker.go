// Package worker turns the redaction verifier into a live pipeline.
//
// pkg/evidence/redaction can prove that a term is absent from a byte
// slice. That is necessary and, on its own, useless: nothing produced
// the byte slice. Article 18 was recorded as INTEGRATION_GAP for
// exactly that reason -- a verifier with no worker in front of it.
//
// This package is the worker. It takes an original document, produces a
// derivative with forbidden terms removed, and releases it only if the
// verifier confirms the terms are gone.
//
// # The trap this package exists to avoid
//
// PDF, XLSX and PPTX all compress their content. Searching the
// container's bytes for a forbidden term finds nothing whether or not
// the term is there, because the bytes are deflated. A pipeline built
// the obvious way would therefore pass every document, including one
// where nothing was redacted at all, and would look like it worked.
//
// So verification never runs against the container. It runs against an
// inspectable view: every part of an OOXML package decompressed, every
// content stream of a PDF decompressed, concatenated with the container
// bytes so that anything stored uncompressed is covered too. See
// inspect.go.
//
// # Fail-closed
//
// A derivative is released only when the verifier says every forbidden
// term is absent from the inspectable view. Anything else -- a leak, a
// format the worker cannot fully account for, an original that changed
// under it -- produces no derivative and an error. There is no path
// through this package that emits a document with a warning attached.
package worker

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"veriqo/pkg/evidence/redaction"
)

// Kind is a document format this package can redact.
type Kind string

const (
	// KindPDF is a PDF whose content streams are uncompressed or
	// FlateDecode. See pdf.go for what is refused.
	KindPDF Kind = "PDF"
	// KindXLSX is an Office Open XML workbook.
	KindXLSX Kind = "XLSX"
	// KindPPTX is an Office Open XML presentation.
	KindPPTX Kind = "PPTX"
)

// Kinds returns every supported format.
func Kinds() []Kind { return []Kind{KindPDF, KindXLSX, KindPPTX} }

var (
	// ErrUnsupportedKind is returned for a format with no worker. It is
	// deliberately not a fallback to "copy the bytes through": a
	// document nobody can redact must not be released as redacted.
	ErrUnsupportedKind = errors.New("redaction/worker: no worker for this format")
	// ErrRefused is returned when a worker can read the document but
	// cannot account for all of it. Refusing is the safe answer; a
	// partial redaction presented as complete is the failure this whole
	// article is about.
	ErrRefused = errors.New("redaction/worker: the document has a structure this worker cannot fully account for")
	// ErrVerifyFailed is returned when the derivative was produced but
	// the verifier found a forbidden term still present. NO_RELEASE.
	ErrVerifyFailed = errors.New("redaction/worker: VERIFY_FAIL, the derivative is not released")
	// ErrNoTerms is returned when no forbidden term was supplied. A
	// redaction with nothing to redact is a copy, and calling a copy a
	// redaction is how a document with nothing removed acquires a
	// redaction provenance record.
	ErrNoTerms = errors.New("redaction/worker: a redaction requires at least one forbidden term")
	// ErrOriginalChanged is returned when the original's bytes no
	// longer hash to the value the caller pinned.
	ErrOriginalChanged = errors.New("redaction/worker: the original does not match its pinned hash")
)

// TransformManifest records what the worker did, in enough detail that
// the transformation can be argued about without re-running it.
//
// It is not a log. A log says what happened; this says what was
// changed, where, and what the worker could not see.
type TransformManifest struct {
	Kind Kind
	// Worker names the code that produced the derivative.
	Worker string
	// PartsInspected are the container parts the worker read. For
	// OOXML these are zip entry names; for PDF, object numbers.
	PartsInspected []string
	// PartsModified are the parts whose bytes changed.
	PartsModified []string
	// Replacements counts substitutions made, per term.
	Replacements map[string]int
	// Unaccounted names structures the worker recognised but could not
	// process. A non-empty list means the worker refused: it is
	// recorded rather than dropped, because "what could not be seen" is
	// the part of a redaction claim that matters most.
	Unaccounted []string
	// RedactionMarker is what replaced the removed content.
	RedactionMarker string
	// Normalization records a structural transformation applied before
	// redaction -- currently the PDF 1.5+ unpacking of object streams.
	//
	// It is separate from PartsModified because it changes the
	// document's SHAPE rather than its content, and a reader comparing
	// the derivative against the original needs to know that before
	// concluding something was lost.
	Normalization []string
}

// Release is a derivative that passed verification, together with the
// evidence that it did. A Release cannot be constructed except by
// Pipeline.Run, and Run only returns one when the verifier passed, so
// holding a Release is itself the statement that verification succeeded.
type Release struct {
	derivative  []byte
	chain       redaction.Chain
	manifest    TransformManifest
	originalID  string
	derivedID   string
	ledgerEvent DisclosureEvent
}

// Derivative returns a copy of the released bytes.
func (r Release) Derivative() []byte { return append([]byte(nil), r.derivative...) }

// Chain returns the redaction evidence chain that licensed the release.
func (r Release) Chain() redaction.Chain { return r.chain }

// Manifest returns the transformation manifest.
func (r Release) Manifest() TransformManifest { return r.manifest }

// OriginalVersionID and DerivativeVersionID are the two evidence
// versions the release links.
func (r Release) OriginalVersionID() string   { return r.originalID }
func (r Release) DerivativeVersionID() string { return r.derivedID }

// LedgerEvent returns the disclosure event this release must emit.
func (r Release) LedgerEvent() DisclosureEvent { return r.ledgerEvent }

// DisclosureEvent is what the ledger records when a derivative is
// released. Article 24 requires every disclosure to emit one.
type DisclosureEvent struct {
	Action              string
	OriginalVersionID   string
	DerivativeVersionID string
	OriginalHash        string
	DerivativeHash      string
	TermsRemoved        int
	EncodingsChecked    int
	Worker              string
}

// Worker produces a derivative with the forbidden terms removed.
//
// A worker is responsible for removing the content and for saying what
// it could not account for. It is NOT responsible for deciding whether
// the result is releasable: that is the verifier's job, and keeping the
// two apart is what stops a worker from certifying its own output.
type Redactor interface {
	Kind() Kind
	Redact(original []byte, terms []string, marker string) ([]byte, TransformManifest, error)
}

// Pipeline runs a redaction end to end.
type Pipeline struct {
	// Marker replaces removed content. It must not itself contain a
	// forbidden term, which Run checks.
	Marker string

	redactors map[Kind]Redactor
}

// NewPipeline returns a pipeline with the three built-in workers.
func NewPipeline() *Pipeline {
	p := &Pipeline{
		Marker:    "[REDACTED]",
		redactors: map[Kind]Redactor{},
	}
	for _, r := range []Redactor{NewPDFRedactor(), NewXLSXRedactor(), NewPPTXRedactor()} {
		p.redactors[r.Kind()] = r
	}
	return p
}

// Request is one redaction job.
type Request struct {
	Kind Kind
	// Original is the document as it stands. It is never modified.
	Original []byte
	// OriginalVersionID and DerivativeVersionID are the evidence
	// versions. They must differ.
	OriginalVersionID   string
	DerivativeVersionID string
	// PinnedOriginalHash is the hash the caller believes the original
	// has. Checking it here is what makes Article 17 -- redaction never
	// modifies the original -- verifiable rather than asserted.
	PinnedOriginalHash string
	// ForbiddenTerms must be removed.
	ForbiddenTerms []string
}

// Run produces a verified derivative, or refuses.
//
// The order is the argument. The original is checked first, because a
// derivative of the wrong original is meaningless however clean it is.
// The worker runs second. The verifier runs third, over the inspectable
// view rather than the container. The release is constructed last, and
// only if the verifier passed.
func (p *Pipeline) Run(req Request) (Release, error) {
	if len(req.ForbiddenTerms) == 0 {
		return Release{}, ErrNoTerms
	}
	if strings.TrimSpace(req.OriginalVersionID) == "" || strings.TrimSpace(req.DerivativeVersionID) == "" {
		return Release{}, errors.New("redaction/worker: both evidence version ids are required")
	}
	if req.OriginalVersionID == req.DerivativeVersionID {
		return Release{}, errors.New("redaction/worker: the derivative must be a new version, not the original's id")
	}
	if len(req.Original) == 0 {
		return Release{}, errors.New("redaction/worker: the original is empty")
	}

	// 1. The original is what the caller pinned.
	actual := redaction.Hash(req.Original)
	if strings.TrimSpace(req.PinnedOriginalHash) != "" && actual != req.PinnedOriginalHash {
		return Release{}, fmt.Errorf("%w: pinned %s, actual %s", ErrOriginalChanged, req.PinnedOriginalHash, actual)
	}

	marker := p.Marker
	if strings.TrimSpace(marker) == "" {
		marker = "[REDACTED]"
	}
	// The marker must not smuggle a forbidden term back in.
	for _, term := range req.ForbiddenTerms {
		if strings.TrimSpace(term) == "" {
			return Release{}, errors.New("redaction/worker: a forbidden term may not be blank")
		}
		if strings.Contains(strings.ToLower(marker), strings.ToLower(term)) {
			return Release{}, fmt.Errorf("redaction/worker: the redaction marker contains the forbidden term %q", term)
		}
	}

	r, ok := p.redactors[req.Kind]
	if !ok {
		return Release{}, fmt.Errorf("%w: %s", ErrUnsupportedKind, req.Kind)
	}

	// 2. The worker produces a derivative.
	derivative, manifest, err := r.Redact(req.Original, req.ForbiddenTerms, marker)
	if err != nil {
		return Release{}, err
	}
	if len(manifest.Unaccounted) > 0 {
		sort.Strings(manifest.Unaccounted)
		return Release{}, fmt.Errorf("%w: %s", ErrRefused, strings.Join(manifest.Unaccounted, "; "))
	}
	// A worker that changed nothing did not redact anything. Releasing
	// an unchanged document under a redaction provenance record is the
	// worst outcome available here, so it is refused before the
	// verifier -- which would otherwise pass it if the terms happened
	// to be absent from the original too.
	if len(manifest.PartsModified) == 0 {
		return Release{}, fmt.Errorf("%w: the worker modified nothing", ErrRefused)
	}

	// 3. Verification, over the inspectable view of both documents.
	originalView, err := Inspectable(req.Kind, req.Original)
	if err != nil {
		return Release{}, fmt.Errorf("redaction/worker: inspecting the original: %w", err)
	}
	derivativeView, err := Inspectable(req.Kind, derivative)
	if err != nil {
		return Release{}, fmt.Errorf("redaction/worker: inspecting the derivative: %w", err)
	}

	// The terms are verified in the forms they actually take inside the
	// container. A name containing an ampersand is stored in OOXML as
	// "R&amp;D Partners", so asking the verifier about "R&D Partners"
	// would get the honest answer that the term was never in the
	// original -- true of the raw bytes, and useless.
	//
	// Expanding to renderings makes the check STRICTER, not looser:
	// every form that was present must now be absent. A term with no
	// rendering present anywhere still produces the verifier's refusal,
	// which is what stops a caller inflating a clean report with terms
	// the document never contained.
	effective := presentRenderings(originalView, req.ForbiddenTerms)
	if len(effective) == 0 {
		return Release{}, fmt.Errorf("%w: none of the forbidden terms appears in the original, "+
			"so their absence from the derivative would prove nothing", ErrVerifyFailed)
	}

	chain, err := redaction.Verify(
		originalView, derivativeView,
		req.OriginalVersionID, req.DerivativeVersionID,
		redaction.Hash(originalView), effective,
	)
	if err != nil {
		return Release{}, fmt.Errorf("%w: %v", ErrVerifyFailed, err)
	}
	if !chain.Verified || !chain.Absent() {
		return Release{}, fmt.Errorf("%w: %s", ErrVerifyFailed, chain.Explain())
	}

	// 4. The original must still be byte-identical. A worker that
	// modified its input in place would otherwise pass everything above.
	if redaction.Hash(req.Original) != actual {
		return Release{}, fmt.Errorf("%w: the worker modified the original in place", ErrOriginalChanged)
	}

	// The chain reported above is over the inspectable view. The
	// release records the container hashes, because those are the
	// evidence versions a reader will fetch.
	chain.OriginalHash = actual
	chain.DerivativeHash = redaction.Hash(derivative)
	chain.Limitations = append(chain.Limitations,
		"byte-level absence was verified over the decompressed inspectable view of the container, "+
			"not over the compressed container bytes, because compression would hide any surviving term")

	return Release{
		derivative: derivative,
		chain:      chain,
		manifest:   manifest,
		originalID: req.OriginalVersionID,
		derivedID:  req.DerivativeVersionID,
		ledgerEvent: DisclosureEvent{
			Action:              "redaction.derivative_released",
			OriginalVersionID:   req.OriginalVersionID,
			DerivativeVersionID: req.DerivativeVersionID,
			OriginalHash:        chain.OriginalHash,
			DerivativeHash:      chain.DerivativeHash,
			TermsRemoved:        len(req.ForbiddenTerms),
			EncodingsChecked:    len(chain.EncodingsChecked),
			Worker:              manifest.Worker,
		},
	}, nil
}

// presentRenderings returns, for every requested term, each rendering of
// it that actually occurs in the original view.
//
// The result is what the verifier is asked about. A term whose only
// occurrence is XML-escaped contributes its escaped form; a term that
// occurs both plainly and escaped contributes both, and both must then
// be absent from the derivative.
func presentRenderings(originalView []byte, terms []string) []string {
	lower := strings.ToLower(string(originalView))
	seen := map[string]bool{}
	var out []string
	for _, term := range terms {
		for _, r := range termRenderings(term) {
			if r == "" || seen[r] {
				continue
			}
			if strings.Contains(lower, strings.ToLower(r)) {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}

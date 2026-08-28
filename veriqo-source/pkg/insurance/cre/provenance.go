package cre

import (
	"errors"
	"fmt"

	"veriqo/pkg/inference"
	"veriqo/pkg/insurance/finding"
)

// ErrInferenceTraceNotFound is VerifyFindingProvenance's refusal when a
// Finding cites a SourceInferenceTraceID that is not present in the
// given traces.
var ErrInferenceTraceNotFound = errors.New("cre: Finding cites a SourceInferenceTraceID that does not exist in the given traces")

// VerifyFindingProvenance closes the loop CRE's AI-governance discipline
// opens: a Finding.SourceInferenceTraceID is a CITATION, and a citation
// nobody ever checks is not meaningfully different from an unverifiable
// assertion. This confirms f's own Hash verifies, and -- when
// SourceInferenceTraceID is set -- that it names a REAL trace within
// traces (typically Recorder.Traces()) whose own Hash also verifies. A
// Finding citing a trace ID that does not exist, or whose hash no
// longer verifies, fails here even though f.Hash alone might still be
// perfectly valid: f's own internal consistency says nothing about
// whether what it cites is real.
func VerifyFindingProvenance(f finding.Finding, traces []inference.InferenceTrace) error {
	if err := finding.VerifyFindingHash(f); err != nil {
		return err
	}
	if f.SourceInferenceTraceID == "" {
		return nil
	}
	for _, t := range traces {
		if t.TraceID == f.SourceInferenceTraceID {
			return inference.VerifyTraceHash(t)
		}
	}
	return fmt.Errorf("%w: %s", ErrInferenceTraceNotFound, f.SourceInferenceTraceID)
}

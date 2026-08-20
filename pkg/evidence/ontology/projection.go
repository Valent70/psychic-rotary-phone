package ontology

import (
	"veriqo/pkg/moat/evidencegraph"
)

// DependencyProjection derives the dependency records implied by an
// evidence record's own provenance. This is what makes the ontology
// load-bearing rather than descriptive: declaring a TransformationID
// on an Evidence is enough for the dependency graph to discount two
// analysts running the same model, with no separate manual wiring.
//
// Three edges are emitted when the corresponding provenance field is
// present, plus one edge per DerivedFrom basis:
//
//	source        --DependsOnSource-->         upstream feed
//	source        --DependsOnProducer-->       producer:<id>
//	source        --DependsOnTransformation--> model:<id>
//	source        --DependsOnDerivation-->     evidence:<basis id>
func DependencyProjection(e Evidence) []evidencegraph.DependencyRecord {
	node := evidencegraph.NodeID(e.Source)
	var out []evidencegraph.DependencyRecord
	add := func(up evidencegraph.NodeID, kind evidencegraph.DependencyKind) {
		out = append(out, evidencegraph.DependencyRecord{
			NodeID: node, UpstreamID: up, Kind: kind, Confidence: 1,
			SourceID: e.Source, ProducerID: e.Provenance.ProducerID,
			TransformationID: e.Provenance.TransformationID,
			ValidFrom:        e.ValidFrom, ValidTo: e.ValidTo,
			TxTick: e.ReceivedAt, EvidenceHash: e.ComputeID(),
		})
	}
	if e.Provenance.UpstreamID != "" {
		add(evidencegraph.NodeID(e.Provenance.UpstreamID), evidencegraph.DependsOnSource)
	}
	if e.Provenance.ProducerID != "" {
		add(evidencegraph.NodeID("producer:"+e.Provenance.ProducerID), evidencegraph.DependsOnProducer)
	}
	if e.Provenance.TransformationID != "" {
		add(evidencegraph.NodeID("model:"+e.Provenance.TransformationID), evidencegraph.DependsOnTransformation)
	}
	for _, basis := range e.DerivedFrom {
		add(evidencegraph.NodeID("evidence:"+basis), evidencegraph.DependsOnDerivation)
	}
	return out
}

// AsOf returns the subset of evidence VERIQO both KNEW about at
// transaction tick `known` and that is asserted to hold at valid tick
// `valid`. This is the bitemporal replay filter; passing the same
// (known, valid) pair always yields the same set, which is what makes
// "replay what we decided last Tuesday" honest rather than a re-run
// with today's data.
func AsOf(list []Evidence, known, valid uint64) []Evidence {
	var out []Evidence
	for _, e := range list {
		if e.KnownAt(known) && e.ValidAt(valid) {
			out = append(out, e)
		}
	}
	return Sort(out)
}

// ApplyCorrections resolves TypeCorrection records: a correction
// removes the evidence it supersedes from the effective set and takes
// its place. Historical replay is unaffected — AsOf with a `known`
// tick before the correction arrived still returns the original,
// which is exactly the PHASE 36 "identity correction -> historical
// state preserved" invariant applied to evidence.
func ApplyCorrections(list []Evidence) []Evidence {
	superseded := map[string]bool{}
	for _, e := range list {
		if e.Type == TypeCorrection && e.Supersedes != "" {
			superseded[e.Supersedes] = true
		}
	}
	var out []Evidence
	for _, e := range list {
		if superseded[e.ComputeID()] {
			continue
		}
		out = append(out, e)
	}
	return Sort(out)
}

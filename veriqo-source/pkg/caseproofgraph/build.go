package caseproofgraph

import (
	"fmt"
	"strconv"

	"veriqo/pkg/casefabric"
	"veriqo/pkg/disclosure/access"
	"veriqo/pkg/proof"
)

// Build assembles the graph from what the fabrics already hold.
//
// This is the composition point the architecture was missing: a case
// from the Case Resolution Fabric, its proof objects from pkg/proof, and
// the qualification apparatus each object carries, all becoming one
// addressable object. Build reads; it decides nothing. Every verdict in
// the graph is copied from wherever it was decided, and a verdict this
// package computed itself would be a second authority.
//
// The proof objects are supplied by the caller keyed on claim id,
// because the case holds proof hashes rather than objects — the case
// fabric deliberately does not own them.
func Build(c *casefabric.Case, proofs map[string]proof.Object, tick uint64) (*Graph, error) {
	id := c.Identity()
	g, err := New(id.CaseID, id.TenantID)
	if err != nil {
		return nil, err
	}

	scope, juris, window := c.Scope()
	caseNodeID := "case:" + id.CaseID
	if err := g.AddNode(Node{
		ID: caseNodeID, Kind: NodeCase, Label: scope.Matter,
		Attributes: map[string]string{
			"domain": id.Domain, "phase": string(c.Phase()),
			"jurisdiction": juris.Code, "forum": juris.Forum,
			"window_from": strconv.FormatUint(window.FromTick, 10),
			"window_to":   strconv.FormatUint(window.ToTick, 10),
			"mission":     c.Mission().Statement,
		},
		Classification: Classification{
			Procedural: access.P1Metadata, Content: access.C1Existence,
			RequiredRight: access.View,
		},
	}); err != nil {
		return nil, err
	}

	// Timeline, as one node carrying the chain head. The entries are not
	// copied in: the case owns them, and duplicating them here would
	// create a second history that could drift from the first.
	tl := c.Timeline()
	timelineHash := ""
	if len(tl) > 0 {
		timelineHash = tl[len(tl)-1].EntryHash
	}
	if err := g.AddNode(Node{
		ID: "timeline:" + id.CaseID, Kind: NodeTimeline, Label: "case timeline",
		ContentHash: timelineHash,
		Attributes:  map[string]string{"entries": strconv.Itoa(len(tl))},
		Classification: Classification{
			Procedural: access.P2ProcessVisible, Content: access.C1Existence,
			RequiredRight: access.View,
		},
	}); err != nil {
		return nil, err
	}
	if err := g.AddEdge(caseNodeID, "timeline:"+id.CaseID, EdgeRecordedIn); err != nil {
		return nil, err
	}

	// Actors, from the timeline. Who did what is part of the record.
	seenActor := map[string]bool{}
	for _, e := range tl {
		if e.Actor == "" || seenActor[e.Actor] {
			continue
		}
		seenActor[e.Actor] = true
		actorID := "actor:" + e.Actor
		if err := g.AddNode(Node{
			ID: actorID, Kind: NodeActor, Label: e.Actor,
			Classification: Classification{
				Procedural: access.P2ProcessVisible, Content: access.C1Existence,
				RequiredRight: access.View,
			},
		}); err != nil {
			return nil, err
		}
		if err := g.AddEdge(caseNodeID, actorID, EdgeInvolves); err != nil {
			return nil, err
		}
	}

	// Claims, and the proof object behind each.
	for _, cl := range c.Claims() {
		claimID := "claim:" + cl.ID
		if err := g.AddNode(Node{
			ID: claimID, Kind: NodeClaim, Label: cl.Proposition.Statement,
			Attributes: map[string]string{
				"proposition_id": cl.Proposition.ID,
				"material":       strconv.FormatBool(cl.Material),
				"stance":         cl.Stance.String(),
				"sufficiency":    cl.Sufficiency.String(),
			},
			Classification: Classification{
				Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
				RequiredRight: access.View,
			},
		}); err != nil {
			return nil, err
		}
		if err := g.AddEdge(caseNodeID, claimID, EdgeHasClaim); err != nil {
			return nil, err
		}

		o, ok := proofs[cl.ID]
		if !ok {
			continue
		}
		if err := addProof(g, claimID, cl.ID, o); err != nil {
			return nil, err
		}
	}

	// Outcome.
	if out, ok := c.Outcome(); ok {
		outID := "outcome:" + id.CaseID
		if err := g.AddNode(Node{
			ID: outID, Kind: NodeOutcome, Label: out.Disposition,
			Attributes: map[string]string{
				"summary":       out.Summary,
				"established":   strconv.Itoa(len(out.EstablishedClaimIDs)),
				"unestablished": strconv.Itoa(len(out.UnestablishedClaimIDs)),
				"limitations":   strconv.Itoa(len(out.Limitations)),
			},
			Classification: Classification{
				Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
				RequiredRight: access.View,
			},
		}); err != nil {
			return nil, err
		}
		if err := g.AddEdge(caseNodeID, outID, EdgeResolvedAs); err != nil {
			return nil, err
		}
	}

	if err := g.Seal(); err != nil {
		return nil, err
	}
	return g, nil
}

// addProof attaches one proof object and its nine sub-nodes.
func addProof(g *Graph, claimNodeID, claimID string, o proof.Object) error {
	proofID := "proof:" + o.CanonicalHash
	if err := g.AddNode(Node{
		ID: proofID, Kind: NodeProofObject, Label: o.Proposition.Statement,
		ContentHash: o.CanonicalHash,
		Attributes: map[string]string{
			"stance": o.Stance.String(), "sufficiency": o.Sufficiency.String(),
			"external_qualification": o.ExternalQualification.Status.String(),
			"authority":              o.Authority.AuthorityID,
			"policy_version":         o.Authority.PolicyVersion,
			"limitations":            strconv.Itoa(len(o.Limitations)),
		},
		Classification: Classification{
			Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
			RequiredRight: access.View,
		},
	}); err != nil {
		return err
	}
	if err := g.AddEdge(claimNodeID, proofID, EdgeProvenBy); err != nil {
		return err
	}

	// Evidence. The graph references versions; it never copies content,
	// and the classification is the strictest in the graph.
	for _, e := range o.EvidenceSet {
		evID := "evidence:" + e.EvidenceVersionID
		if _, exists := g.nodes[evID]; !exists {
			if err := g.AddNode(Node{
				ID: evID, Kind: NodeEvidence, Label: e.EvidenceID,
				ContentHash: e.SHA256, EvidenceVersionID: e.EvidenceVersionID,
				Attributes: map[string]string{"source_id": e.SourceID, "version_id": e.EvidenceVersionID},
				Classification: Classification{
					Procedural: access.P3PartyVisible, Content: access.C3ControlledFullView,
					RequiredRight: access.View,
				},
			}); err != nil {
				return err
			}
		}
		if err := g.AddEdge(proofID, evID, EdgeRestsOn); err != nil {
			return err
		}
	}

	sub := []struct {
		suffix string
		kind   NodeKind
		edge   EdgeKind
		label  string
		attrs  map[string]string
		class  Classification
	}{
		{"trust", NodeTrust, EdgeAssessedBy, "source trust assessment", map[string]string{
			"assessed":          strconv.FormatBool(o.Trust.Assessed),
			"effective_sources": strconv.Itoa(o.Trust.EffectiveSourceCount),
		}, Classification{access.P2ProcessVisible, access.C2Redacted, access.View}},
		{"independence", NodeIndependence, EdgeAssessedBy, "source independence", map[string]string{
			"assessments": strconv.Itoa(len(o.Independence)),
			"verdicts":    strconv.Itoa(len(o.Trust.Verdicts)),
		}, Classification{access.P2ProcessVisible, access.C2Redacted, access.View}},
		{"qualification", NodeQualification, EdgeQualifiedBy, "qualification verdict", map[string]string{
			"state": string(o.Qualification.State), "policy_version": o.Qualification.PolicyVersion,
		}, Classification{access.P2ProcessVisible, access.C2Redacted, access.View}},
		{"reverseproof", NodeReverseProof, EdgeObligatedBy, "proof obligations", map[string]string{
			"requirements": strconv.Itoa(len(o.ReverseProof.Requirements)),
			"complete":     strconv.FormatBool(o.ReverseProofGap.Complete),
			"unattempted":  strconv.Itoa(len(o.ReverseProofGap.Unattempted)),
		}, Classification{access.P2ProcessVisible, access.C2Redacted, access.View}},
		{"missing", NodeMissingEvidence, EdgeMissing, "missing evidence", map[string]string{
			"total":        strconv.Itoa(len(o.MissingEvidence)),
			"unobtainable": strconv.Itoa(len(o.UnobtainableEvidence())),
		}, Classification{access.P2ProcessVisible, access.C2Redacted, access.View}},
		{"nextbest", NodeNextBestEvidence, EdgeSuggests, "next best evidence", map[string]string{
			"candidates": strconv.Itoa(len(o.NextBestEvidence)),
		}, Classification{access.P2ProcessVisible, access.C2Redacted, access.View}},
	}
	for _, s := range sub {
		nid := "proof:" + o.CanonicalHash + ":" + s.suffix
		if err := g.AddNode(Node{
			ID: nid, Kind: s.kind, Label: s.label, Attributes: s.attrs, Classification: s.class,
		}); err != nil {
			return err
		}
		if err := g.AddEdge(proofID, nid, s.edge); err != nil {
			return err
		}
	}

	// Contradictions are individually addressable: a carried
	// contradiction needs an identity, which is why it is a canonical
	// object type in its own right.
	for _, ct := range o.Contradictions {
		cid := "contradiction:" + ct.ID
		if err := g.AddNode(Node{
			ID: cid, Kind: NodeContradiction, Label: ct.Description,
			Attributes: map[string]string{
				"material": strconv.FormatBool(ct.Material),
				"resolved": strconv.FormatBool(ct.Resolved),
				"between":  fmt.Sprintf("%d", len(ct.Between)),
			},
			Classification: Classification{
				Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
				RequiredRight: access.View,
			},
		}); err != nil {
			return err
		}
		if err := g.AddEdge(proofID, cid, EdgeContradictedBy); err != nil {
			return err
		}
	}

	// A finding node exists only where the object founds one. An
	// insufficient object has no finding, and the graph shows that by
	// absence rather than by an empty node.
	if o.Sufficiency == proof.Sufficient && o.Stance == proof.Support {
		f, err := proof.NewFinding(o, 0)
		if err != nil {
			return err
		}
		fid := "finding:" + f.Hash()
		if err := g.AddNode(Node{
			ID: fid, Kind: NodeFinding, Label: f.Statement(), ContentHash: f.Hash(),
			Attributes: map[string]string{
				"stance": f.Stance().String(), "qualification": f.Qualification(),
				"limitations": strconv.Itoa(len(f.Limitations())),
			},
			Classification: Classification{
				Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
				RequiredRight: access.View,
			},
		}); err != nil {
			return err
		}
		if err := g.AddEdge(proofID, fid, EdgeFounds); err != nil {
			return err
		}
	}
	return nil
}

// AddDecision attaches an authorized decision to the graph.
//
// It is separate from Build because a decision is downstream of the
// case: a case can be graphed before anybody decides anything, and
// forcing a decision into the constructor would imply otherwise.
func AddDecision(g *Graph, d proof.Decision) error {
	dh, ah, fh, ph := d.Lineage()
	did := "decision:" + dh
	if err := g.AddNode(Node{
		ID: did, Kind: NodeDecision, Label: d.Action(), ContentHash: dh,
		Attributes: map[string]string{
			"authorizer":      d.Authorized().AuthorizerID(),
			"authorizer_role": d.Authorized().AuthorizerRole(),
			"policy_version":  d.Authorized().PolicyVersion(),
			"rationale":       d.Rationale(),
			"authorized_hash": ah, "finding_hash": fh, "proof_hash": ph,
		},
		Classification: Classification{
			Procedural: access.P2ProcessVisible, Content: access.C2Redacted,
			RequiredRight: access.View,
		},
	}); err != nil {
		return err
	}
	// The decision links to the finding it rests on, where that finding
	// is in the graph. A decision whose finding is absent is a decision
	// nobody can trace, and the edge's absence says so.
	if _, ok := g.nodes["finding:"+fh]; ok {
		if err := g.AddEdge(did, "finding:"+fh, EdgeAuthorizedBy); err != nil {
			return err
		}
	}
	return g.Seal()
}

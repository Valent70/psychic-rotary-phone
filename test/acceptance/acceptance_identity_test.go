package acceptance

import (
	"testing"

	"veriqo/pkg/identity"
	"veriqo/pkg/lifecycle"
	"veriqo/pkg/moat/entity"
)

// ===== Category 4/5 — IDENTITY / TEMPORAL (10) ==============================

// TestAcceptance31_ThreeAliasesResolveToOneEntity asserts against
// o.Identity, not o.Entities: since P0-B, pkg/identity is the canonical
// entity authority for every alias Kind it models (IMO, CALLSIGN, NAME
// all are), and o.Entities (the legacy union-find) is no longer written
// to for this case at all -- see pkg/lifecycle's
// resolveCanonicalEntity doc comment. Checking the old union-find here
// would be asserting a fact that P0-B deliberately made false.
func TestAcceptance31_ThreeAliasesResolveToOneEntity(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("id-1",
		entity.Alias{Kind: "IMO", Value: "9111111"},
		entity.Alias{Kind: "CALLSIGN", Value: "ZZZZ1"},
		entity.Alias{Kind: "NAME", Value: "MV ACCEPTANCE"},
	)
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if string(res.EntityID) == "" {
		t.Fatalf("expected a resolved canonical entity ID on the result")
	}

	imoID, err := o.Identity.EntityIDAt(identity.Identifier{Kind: identity.KindIMO, Value: "9111111"}, in.Tick)
	if err != nil {
		t.Fatalf("EntityIDAt(IMO): %v", err)
	}
	callsignID, err := o.Identity.EntityIDAt(identity.Identifier{Kind: identity.KindCallsign, Value: "ZZZZ1"}, in.Tick)
	if err != nil {
		t.Fatalf("EntityIDAt(CALLSIGN): %v", err)
	}
	nameID, err := o.Identity.EntityIDAt(identity.Identifier{Kind: identity.KindName, Value: "MV ACCEPTANCE"}, in.Tick)
	if err != nil {
		t.Fatalf("EntityIDAt(NAME): %v", err)
	}
	if imoID != callsignID || imoID != nameID {
		t.Fatalf("expected all 3 aliases resolved to one entity by the canonical identity authority, got IMO=%s CALLSIGN=%s NAME=%s",
			imoID, callsignID, nameID)
	}
	if imoID != string(res.EntityID) {
		t.Fatalf("expected RunUnified's EntityID to equal Identity's own resolution: got %s want %s", res.EntityID, imoID)
	}
}

func TestAcceptance32_IntentIDStableForIdenticalIntent(t *testing.T) {
	in1 := baseIntent("id-2")
	in2 := baseIntent("id-2")
	if in1.ID() != in2.ID() {
		t.Fatalf("expected identical Intent field values to produce identical IntentID")
	}
}

func TestAcceptance33_IntentIDChangesWithObjective(t *testing.T) {
	in1 := baseIntent("id-3")
	in2 := baseIntent("id-3")
	in2.Objective = "different objective"
	if in1.ID() == in2.ID() {
		t.Fatalf("expected a different Objective to change IntentID")
	}
}

func TestAcceptance34_EvidencePlanHashStableForIdenticalPlan(t *testing.T) {
	in := baseIntent("id-4")
	p1 := standardPlan(in)
	p2 := standardPlan(in)
	if p1.Hash != p2.Hash {
		t.Fatalf("expected identical EvidencePlan inputs to produce identical Hash")
	}
}

func TestAcceptance35_DifferentTicksProduceDifferentIntentID(t *testing.T) {
	in1 := baseIntent("id-5")
	in1.Tick = 1
	in2 := baseIntent("id-5")
	in2.Tick = 2
	if in1.ID() == in2.ID() {
		t.Fatalf("expected different Tick values to change IntentID")
	}
}

func TestAcceptance36_EntityMergeOrderInvariantThroughLifecycle(t *testing.T) {
	imo := entity.Alias{Kind: "IMO", Value: "9222222"}
	callsign := entity.Alias{Kind: "CALLSIGN", Value: "YYYY1"}
	name := entity.Alias{Kind: "NAME", Value: "MV ORDER TEST"}

	o1 := lifecycle.NewOrchestrator(nil, nil)
	in1 := lifecycle.Intent{ActorID: "id-6a", Tenant: "acme", Objective: "x", EntityAliases: []entity.Alias{imo, callsign, name}, Tick: 1}
	res1 := mustRun(t, o1, in1, standardPlan(in1), baseCase("x"))

	o2 := lifecycle.NewOrchestrator(nil, nil)
	in2 := lifecycle.Intent{ActorID: "id-6b", Tenant: "acme", Objective: "x", EntityAliases: []entity.Alias{name, callsign, imo}, Tick: 1}
	res2 := mustRun(t, o2, in2, standardPlan(in2), baseCase("x"))

	if res1.EntityID != res2.EntityID {
		t.Fatalf("expected merge-order-invariant canonical entity ID across independent orchestrators, got %s vs %s",
			res1.EntityID, res2.EntityID)
	}
}

func TestAcceptance37_RepeatedAliasMergeIsIdempotentEntityID(t *testing.T) {
	alias := entity.Alias{Kind: "IMO", Value: "9333333"}
	o := lifecycle.NewOrchestrator(nil, nil)
	in := lifecycle.Intent{ActorID: "id-7", Tenant: "acme", Objective: "x", EntityAliases: []entity.Alias{alias, alias}, Tick: 1}
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	if res.EntityID == "" {
		t.Fatalf("expected a valid entity ID even with a repeated alias")
	}
}

func TestAcceptance38_TwoUnrelatedIntentsGetDifferentEntities(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in1 := baseIntent("id-8", entity.Alias{Kind: "IMO", Value: "9444444"})
	res1 := mustRun(t, o, in1, standardPlan(in1), baseCase("x"))
	in2 := baseIntent("id-8", entity.Alias{Kind: "IMO", Value: "9555555"})
	res2 := mustRun(t, o, in2, standardPlan(in2), baseCase("y"))
	if res1.EntityID == res2.EntityID {
		t.Fatalf("expected unrelated vessels to resolve to different entity IDs")
	}
}

func TestAcceptance39_LifecycleCertificateCarriesFullIDChain(t *testing.T) {
	o := lifecycle.NewOrchestrator(nil, nil)
	in := baseIntent("id-9")
	res := mustRun(t, o, in, standardPlan(in), baseCase("x"))
	c := res.Certificate
	if c.IntentID == "" || c.EntityID == "" || c.EvidencePlanHash == "" || c.Canonical.Hash == "" || c.ReplayID == "" {
		t.Fatalf("expected the full ID chain (Intent->Entity->Plan->Canonical->Replay) to be populated, got %+v", c)
	}
}

func TestAcceptance40_TemporalScopeParticipatesInIntentID(t *testing.T) {
	in1 := baseIntent("id-10")
	in1.TemporalScope = "last-7-days"
	in2 := baseIntent("id-10")
	in2.TemporalScope = "last-30-days"
	if in1.ID() == in2.ID() {
		t.Fatalf("expected different TemporalScope to change IntentID")
	}
}

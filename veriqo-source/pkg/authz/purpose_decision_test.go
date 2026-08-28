package authz

import (
	"errors"
	"testing"

	"veriqo/pkg/platform/audit"
)

func purposeRule(id, action, resource string, purposes ...string) Rule {
	return Rule{ID: id, Effect: Allow, Actions: []string{action}, Resources: []string{resource}, Purposes: purposes}
}

func TestPurposeBindingRequiresDeclaredPurpose(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		purposeRule("r1", "read", "case/*", "claims_investigation", "regulatory_audit"),
	})); err != nil {
		t.Fatal(err)
	}

	ex, err := e.Can(Request{Actor: "a", Action: "read", Resource: "case/1", Purpose: "claims_investigation"})
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if !ex.Allowed {
		t.Fatalf("expected allow for a bound purpose, got %v", ex)
	}

	ex2, err := e.Can(Request{Actor: "a", Action: "read", Resource: "case/1", Purpose: "marketing"})
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if ex2.Allowed {
		t.Fatal("expected deny for an out-of-scope purpose")
	}

	ex3, err := e.Can(Request{Actor: "a", Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if ex3.Allowed {
		t.Fatal("expected deny for a request with no declared purpose at all, when the rule requires one")
	}
}

func TestPurposeAgnosticRuleMatchesAnyOrNoPurpose(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		allowRule("r1", "read", "case/*", "analyst"),
	})); err != nil {
		t.Fatal(err)
	}
	// A rule with no Purposes set (every rule written before this field
	// existed) must behave identically regardless of what purpose (if
	// any) the request declares.
	for _, purpose := range []string{"", "claims_investigation", "anything_at_all"} {
		ex, err := e.Can(Request{Actor: "a", Roles: []string{"analyst"}, Action: "read", Resource: "case/1", Purpose: purpose})
		if err != nil {
			t.Fatalf("Can: %v", err)
		}
		if !ex.Allowed {
			t.Fatalf("expected a purpose-agnostic rule to allow regardless of declared purpose %q", purpose)
		}
	}
}

func TestPurposeWildcardMatching(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		purposeRule("r1", "read", "case/*", "claims_*"),
	})); err != nil {
		t.Fatal(err)
	}
	ex, err := e.Can(Request{Actor: "a", Action: "read", Resource: "case/1", Purpose: "claims_investigation"})
	if err != nil {
		t.Fatalf("Can: %v", err)
	}
	if !ex.Allowed {
		t.Fatal("expected a prefix-wildcard purpose to match")
	}
}

func TestPurposesAreCryptographicallyBoundIntoDocumentHash(t *testing.T) {
	published := doc(1, []Rule{purposeRule("r1", "read", "case/*", "claims_investigation")})
	tampered := doc(1, []Rule{purposeRule("r1", "read", "case/*", "marketing")})
	if string(tampered.canonicalBytes()) == string(published.canonicalBytes()) {
		t.Fatal("expected changing a rule's Purposes to change canonicalBytes")
	}
}

func publishAndActivate(t *testing.T, e *Engine, d Document) (Document, error) {
	t.Helper()
	published, err := e.Publish(d)
	if err != nil {
		return Document{}, err
	}
	if err := e.Activate(published.Version); err != nil {
		return Document{}, err
	}
	return published, nil
}

func TestCanRecordedProducesAConsistentPolicyDecision(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		allowRule("r1", "read", "case/*", "analyst"),
	})); err != nil {
		t.Fatal(err)
	}

	ex, dec, err := e.CanRecorded(Request{
		Actor: "alice", Roles: []string{"analyst"}, Action: "read", Resource: "case/1",
		Tenant: "tenant-a", Purpose: "claims_investigation", Tick: 10,
	})
	if err != nil {
		t.Fatalf("CanRecorded: %v", err)
	}
	if !ex.Allowed || !dec.Allowed {
		t.Fatalf("expected allow, got Explanation=%v Decision=%v", ex, dec)
	}
	if dec.ReasonCodes[0] != ReasonRuleAllow {
		t.Fatalf("expected reason code %s, got %v", ReasonRuleAllow, dec.ReasonCodes)
	}
	if dec.Tenant != "tenant-a" || dec.Purpose != "claims_investigation" {
		t.Fatalf("expected Tenant/Purpose to travel through to the decision, got %v", dec)
	}
	if dec.PolicyInputsHash == "" || dec.DecisionHash == "" || dec.DecisionID == "" {
		t.Fatalf("expected non-empty hashes and ID, got %v", dec)
	}
	if err := VerifyPolicyDecisionHash(dec); err != nil {
		t.Fatalf("VerifyPolicyDecisionHash: %v", err)
	}
}

func TestCanRecordedIsDeterministicForIdenticalInputs(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		allowRule("r1", "read", "case/*", "analyst"),
	})); err != nil {
		t.Fatal(err)
	}
	req := Request{Actor: "alice", Roles: []string{"analyst"}, Action: "read", Resource: "case/1", Tick: 10}
	_, dec1, err := e.CanRecorded(req)
	if err != nil {
		t.Fatal(err)
	}
	_, dec2, err := e.CanRecorded(req)
	if err != nil {
		t.Fatal(err)
	}
	if dec1.DecisionID != dec2.DecisionID {
		t.Fatalf("expected identical inputs at the identical tick to produce the identical DecisionID, got %s vs %s", dec1.DecisionID, dec2.DecisionID)
	}
	if dec1.DecisionHash != dec2.DecisionHash {
		t.Fatal("expected identical decisions to hash identically")
	}
}

func TestCanRecordedDiffersAcrossPolicyVersionsForSameRequest(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		allowRule("r1", "read", "case/*", "analyst"),
	})); err != nil {
		t.Fatal(err)
	}
	req := Request{Actor: "alice", Roles: []string{"analyst"}, Action: "read", Resource: "case/1", Tick: 10}
	_, dec1, err := e.CanRecorded(req)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := publishAndActivate(t, e, doc(2, []Rule{
		{ID: "r2", Effect: Deny, Actions: []string{"read"}, Resources: []string{"case/*"}},
	})); err != nil {
		t.Fatal(err)
	}
	_, dec2, err := e.CanRecorded(req)
	if err != nil {
		t.Fatal(err)
	}
	if dec1.DecisionID == dec2.DecisionID {
		t.Fatal("expected the same request evaluated under a different active policy version to produce a different DecisionID")
	}
	if dec2.Allowed {
		t.Fatal("expected the version-2 deny rule to actually deny")
	}
	if dec2.ReasonCodes[0] != ReasonRuleDeny {
		t.Fatalf("expected reason code %s, got %v", ReasonRuleDeny, dec2.ReasonCodes)
	}
}

func TestCanRecordedMirrorsToAuditStoreWhenAttached(t *testing.T) {
	e := NewEngine()
	store := audit.NewAuditStore()
	e.AttachAuditStore(store)
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		allowRule("r1", "read", "case/*", "analyst"),
	})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.CanRecorded(Request{Actor: "alice", Roles: []string{"analyst"}, Action: "read", Resource: "case/1"}); err != nil {
		t.Fatal(err)
	}
	records := store.Snapshot()
	if len(records) != 1 {
		t.Fatalf("expected exactly one audit record, got %d", len(records))
	}
	if records[0].Action != "PolicyDecision" {
		t.Fatalf("expected action PolicyDecision, got %q", records[0].Action)
	}
}

func TestCanRecordedRecordsRefusalsWithNoActivePolicy(t *testing.T) {
	e := NewEngine()
	store := audit.NewAuditStore()
	e.AttachAuditStore(store)
	ex, dec, err := e.CanRecorded(Request{Actor: "alice", Action: "read", Resource: "case/1"})
	if !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("expected ErrNoPolicy, got %v", err)
	}
	if ex.Allowed || dec.Allowed {
		t.Fatal("expected deny when there is no active policy")
	}
	if dec.ReasonCodes[0] != ReasonNoActivePolicy {
		t.Fatalf("expected reason code %s, got %v", ReasonNoActivePolicy, dec.ReasonCodes)
	}
	// A refused evaluation is still an auditable fact -- it must still
	// be mirrored to the audit ledger, not silently dropped.
	if len(store.Snapshot()) != 1 {
		t.Fatalf("expected the refusal itself to be mirrored to the audit ledger, got %d records", len(store.Snapshot()))
	}
}

func TestVerifyPolicyDecisionHashDetectsTampering(t *testing.T) {
	e := NewEngine()
	if _, err := publishAndActivate(t, e, doc(1, []Rule{
		allowRule("r1", "read", "case/*", "analyst"),
	})); err != nil {
		t.Fatal(err)
	}
	_, dec, err := e.CanRecorded(Request{Actor: "alice", Roles: []string{"analyst"}, Action: "read", Resource: "case/1"})
	if err != nil {
		t.Fatal(err)
	}
	dec.Allowed = false // tamper
	if err := VerifyPolicyDecisionHash(dec); err == nil {
		t.Fatal("expected tampering with Allowed to invalidate the decision hash")
	}
}

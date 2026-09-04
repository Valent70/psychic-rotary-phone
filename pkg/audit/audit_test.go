package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"veriqo/pkg/contract"
	"veriqo/pkg/identity"
	"veriqo/pkg/ledger"
	"veriqo/pkg/policy"
)

type signer struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func (s signer) Sign(m []byte) ([]byte, string, error) { return ed25519.Sign(s.priv, m), "k1", nil }
func (s signer) Verify(m, sig []byte, id string) error {
	if !ed25519.Verify(s.pub, m, sig) {
		return errors.New("bad signature")
	}
	return nil
}

var at0 = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

func versions() contract.VersionSet {
	return contract.VersionSet{
		Ontology:  contract.Version{Component: "maritime", Revision: 1},
		Policy:    contract.Version{Component: "baseline", Revision: 1},
		Algorithm: contract.Version{Component: "ingest", Revision: 1},
	}
}

func ctx() Context {
	return Context{TraceID: "tr-1", TenantID: "t-acme", CaseID: "case-1",
		WorkflowID: "wf-1", Versions: versions(), EvidenceVersion: "ev-1"}
}

func recorder(t *testing.T) (*Recorder, *ledger.Ledger) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	l, err := ledger.Open(t.TempDir(), "t-acme", ledger.Options{Signer: signer{priv, pub}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	r, err := NewRecorder(l, ctx())
	if err != nil {
		t.Fatal(err)
	}
	return r, l
}

func analyst() identity.Principal {
	return identity.Principal{ID: "human:analyst-1", Kind: identity.Human, TenantID: "t-acme"}
}

func permitted() policy.Decision {
	return policy.Decision{Effect: policy.Permit, Rule: "baseline/clearance",
		Obligations: []policy.Obligation{{Kind: policy.ObligationAuditElevated}}}
}

func op() Operation {
	return Operation{Action: "evidence.ingested", Subject: "evidence:e1",
		Purpose: policy.CaseInvestigation, Decision: permitted(), Severity: Routine}
}

// TestAFailureIsAuditedExactlyAsASuccess. A system that audits only
// what worked has an audit trail of what worked.
func TestAFailureIsAuditedExactlyAsASuccess(t *testing.T) {
	r, l := recorder(t)
	_, err := r.Guard(analyst(), op(), at0, func() (string, contract.Outcome, error) {
		return "", contract.Failed, errors.New("the parser blew up")
	})
	if err == nil {
		t.Fatal("Guard swallowed the operation's error")
	}
	recs, rerr := l.Records()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(recs) != 1 {
		t.Fatalf("%d records written for a failed operation, want 1", len(recs))
	}
	if recs[0].Event.Outcome != contract.Failed {
		t.Fatalf("the failure was recorded as %s", recs[0].Event.Outcome)
	}
}

// TestADeniedOperationIsRecordedAsRefusedAndDoesNotRun. The half that
// gets left out: the work must not happen, AND the denial must appear
// in the chain.
func TestADeniedOperationIsRecordedAsRefusedAndDoesNotRun(t *testing.T) {
	r, l := recorder(t)
	o := op()
	o.Decision = policy.Decision{Effect: policy.Deny, Rule: "core/tenant-isolation",
		Reason: "cross-tenant"}

	ran := false
	_, err := r.Guard(analyst(), o, at0, func() (string, contract.Outcome, error) {
		ran = true
		return "", contract.Succeeded, nil
	})
	if ran {
		t.Fatal("THE WORK RAN AFTER A DENIAL")
	}
	if !errors.Is(err, policy.ErrDenied) {
		t.Fatalf("Guard returned %v, want a denial", err)
	}
	recs, _ := l.Records()
	if len(recs) != 1 || recs[0].Event.Outcome != contract.Refused {
		t.Fatalf("the denial was not recorded as REFUSED: %+v", recs)
	}
	if !strings.Contains(recs[0].Event.PolicyDecision, "core/tenant-isolation") {
		t.Fatalf("the record does not name the denying rule: %q", recs[0].Event.PolicyDecision)
	}
}

// TestRefusedIsNotFailed in the audit trail. This is the mutation the
// suite attacks; it is pinned here at the point the outcome is chosen.
func TestRefusedIsNotFailed(t *testing.T) {
	r, l := recorder(t)
	_, err := r.Guard(analyst(), op(), at0, func() (string, contract.Outcome, error) {
		return "", contract.Refused, nil
	})
	if err != nil {
		t.Fatalf("a designed refusal was reported as an error: %v", err)
	}
	recs, _ := l.Records()
	if recs[0].Event.Outcome != contract.Refused {
		t.Fatalf("a refusal was recorded as %s", recs[0].Event.Outcome)
	}
	if recs[0].Event.Outcome.IsDefect() {
		t.Fatal("the recorded refusal classifies as a defect")
	}
}

// TestSuccessWithAnErrorIsRecordedAsFailed. A caller returning both is
// confused; recording it as a success would put the confusion in the
// ledger.
func TestSuccessWithAnErrorIsRecordedAsFailed(t *testing.T) {
	r, l := recorder(t)
	_, err := r.Guard(analyst(), op(), at0, func() (string, contract.Outcome, error) {
		return "out", contract.Succeeded, errors.New("but also this")
	})
	if err == nil {
		t.Fatal("the error was dropped")
	}
	recs, _ := l.Records()
	if recs[0].Event.Outcome != contract.Failed {
		t.Fatalf("recorded as %s despite an error", recs[0].Event.Outcome)
	}
}

// TestTheRecordCarriesTheVersions: "what happened" includes "under
// which rules".
func TestTheRecordCarriesTheVersions(t *testing.T) {
	r, l := recorder(t)
	if _, err := r.Record(analyst(), op(), contract.Succeeded, at0); err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Records()
	v := recs[0].Event.Versions
	if !v.Complete() {
		t.Fatalf("the record is missing versions: %v", v.Missing())
	}
	if v.Policy.Component != "baseline" {
		t.Fatalf("policy version = %s", v.Policy)
	}
}

// TestTheRecordCarriesTheExecutionContext.
func TestTheRecordCarriesTheExecutionContext(t *testing.T) {
	r, l := recorder(t)
	r.Record(analyst(), op(), contract.Succeeded, at0)
	recs, _ := l.Records()
	res := recs[0].Event.Result
	for _, want := range []string{"trace=tr-1", "case=case-1", "workflow=wf-1", "evidence=ev-1"} {
		if !strings.Contains(res, want) {
			t.Errorf("the record does not carry %s: %q", want, res)
		}
	}
}

// TestObligationsAppearInTheRecord. A permit whose obligations are not
// recorded cannot be checked afterwards.
func TestObligationsAppearInTheRecord(t *testing.T) {
	r, l := recorder(t)
	r.Record(analyst(), op(), contract.Succeeded, at0)
	recs, _ := l.Records()
	if !strings.Contains(recs[0].Event.PolicyDecision, policy.ObligationAuditElevated) {
		t.Fatalf("obligations absent from the record: %q", recs[0].Event.PolicyDecision)
	}
}

// TestAnAgentsActionIsAttributedToTheAgent, with the human it acts for
// recorded separately. Merging them makes the tool firewall
// unauditable.
func TestAnAgentsActionIsAttributedToTheAgent(t *testing.T) {
	r, l := recorder(t)
	principal := contract.ID("human:analyst-1")
	a := identity.Principal{ID: "agent:research", Kind: identity.Agent,
		TenantID: "t-acme", OnBehalfOf: &principal}
	if _, err := r.Record(a, op(), contract.Succeeded, at0); err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Records()
	if recs[0].Event.Actor != "agent:research" {
		t.Fatalf("actor = %s, want the agent", recs[0].Event.Actor)
	}
	if recs[0].Event.OnBehalf != "human:analyst-1" {
		t.Fatalf("on_behalf = %q", recs[0].Event.OnBehalf)
	}
}

// --- Context requirements -----------------------------------------------

func TestARecorderWithNoTraceIsRefused(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	l, _ := ledger.Open(t.TempDir(), "t-acme", ledger.Options{Signer: signer{priv, pub}})
	defer l.Close()
	c := ctx()
	c.TraceID = ""
	if _, err := NewRecorder(l, c); !errors.Is(err, ErrNoTrace) {
		t.Fatalf("a recorder with no trace id was built: %v", err)
	}
}

func TestARecorderWithIncompleteVersionsIsRefused(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	l, _ := ledger.Open(t.TempDir(), "t-acme", ledger.Options{Signer: signer{priv, pub}})
	defer l.Close()
	c := ctx()
	c.Versions = contract.VersionSet{}
	if _, err := NewRecorder(l, c); !errors.Is(err, contract.ErrUnversioned) {
		t.Fatalf("an unversioned context was accepted: %v", err)
	}
}

// TestMissingReportsTheOptionalFieldsThatWereNotSet, so coverage can
// be measured rather than assumed.
func TestMissingReportsTheOptionalFieldsThatWereNotSet(t *testing.T) {
	c := Context{TraceID: "tr", TenantID: "t-acme", Versions: versions()}
	if err := c.Validate(); err != nil {
		t.Fatalf("a minimal context was refused: %v", err)
	}
	m := c.Missing()
	if len(m) != 3 {
		t.Fatalf("Missing() = %v, want case_id, evidence_version, workflow_id", m)
	}
	if ctxMissing := ctx().Missing(); len(ctxMissing) != 0 {
		t.Fatalf("a complete context reported %v missing", ctxMissing)
	}
}

// TestASecurityEventNeedsAStatedReason.
func TestASecurityEventNeedsAStatedReason(t *testing.T) {
	r, _ := recorder(t)
	o := op()
	o.Severity = Security
	if _, err := r.Record(analyst(), o, contract.Succeeded, at0); !errors.Is(err, ErrNoContext) {
		t.Fatalf("a SECURITY event with no reason was recorded: %v", err)
	}
	o.Reason = "credential rotated after suspected compromise"
	if _, err := r.Record(analyst(), o, contract.Succeeded, at0); err != nil {
		t.Fatalf("a justified SECURITY event was refused: %v", err)
	}
}

func TestACrossTenantPrincipalIsRefused(t *testing.T) {
	r, _ := recorder(t)
	p := analyst()
	p.TenantID = "t-beta"
	if _, err := r.Record(p, op(), contract.Succeeded, at0); !errors.Is(err, contract.ErrCrossTenant) {
		t.Fatalf("a cross-tenant principal was audited into this tenant: %v", err)
	}
}

// TestAnUnrecordableOperationFailsLoudly. If the ledger cannot accept
// the record, the caller must learn that the operation is unaudited
// rather than receiving a plain success.
func TestAnUnrecordableOperationFailsLoudly(t *testing.T) {
	r, l := recorder(t)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := r.Guard(analyst(), op(), at0, func() (string, contract.Outcome, error) {
		return "out", contract.Succeeded, nil
	})
	if !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("an unrecorded operation returned %v", err)
	}
}

func TestInvalidSeverityAndOutcomeAreRefused(t *testing.T) {
	r, _ := recorder(t)
	o := op()
	o.Severity = "INFO"
	if _, err := r.Record(analyst(), o, contract.Succeeded, at0); err == nil {
		t.Fatal("an unknown severity was accepted")
	}
	if _, err := r.Record(analyst(), op(), contract.Outcome("OK"), at0); err == nil {
		t.Fatal("an unknown outcome was accepted")
	}
}

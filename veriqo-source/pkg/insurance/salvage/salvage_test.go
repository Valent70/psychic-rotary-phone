package salvage

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"veriqo/pkg/insurance/party"
	"veriqo/pkg/insurance/quantum"
)

func TestNewProducesIdentifiedWithNoDisposal(t *testing.T) {
	o, err := New("SLV-1", "CASE-1", "CLM-1", "40ft reefer container CTNR-4471")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if o.Status != StatusIdentified {
		t.Fatalf("expected StatusIdentified, got %s", o.Status)
	}
	if o.DisposalMethod != DisposalNotYetDisposed {
		t.Fatalf("expected no disposal method, got %s", o.DisposalMethod)
	}
	if o.NetValue().Amount != 0 || len(o.NetValue().EvidenceIDs) != 0 {
		t.Fatalf("expected zero NetValue before disposal, got %+v", o.NetValue())
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name string
		o    Operation
		want error
	}{
		{"empty id", Operation{CaseID: "C", ClaimID: "CLM", AssetDescription: "x", Status: StatusIdentified}, ErrEmptyOperationID},
		{"empty case", Operation{OperationID: "S", ClaimID: "CLM", AssetDescription: "x", Status: StatusIdentified}, ErrEmptyCaseID},
		{"empty claim", Operation{OperationID: "S", CaseID: "C", AssetDescription: "x", Status: StatusIdentified}, ErrEmptyClaimID},
		{"empty asset", Operation{OperationID: "S", CaseID: "C", ClaimID: "CLM", Status: StatusIdentified}, ErrEmptyAssetDesc},
	}
	for _, c := range cases {
		if err := c.o.Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}
}

func TestDisposedRequiresMethod(t *testing.T) {
	o, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo")
	o.Status = StatusDisposed
	if err := o.Validate(); !errors.Is(err, ErrDisposedWithNoMethod) {
		t.Fatalf("expected ErrDisposedWithNoMethod, got %v", err)
	}
}

func TestDisposedProceedsRequireEvidence(t *testing.T) {
	o, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo")
	o.Status = StatusDisposed
	o.DisposalMethod = DisposalSold
	o.DisposalProceeds = quantum.NewEvidenceBackedAmount(quantum.MajorUnits(100))
	if err := o.Validate(); !errors.Is(err, ErrDisposedWithNoProceedsEvidence) {
		t.Fatalf("expected ErrDisposedWithNoProceedsEvidence, got %v", err)
	}
	// Zero proceeds legitimately needs no evidence (nothing was realised).
	o.DisposalProceeds = quantum.EvidenceBackedAmount{}
	if err := o.Validate(); err != nil {
		t.Fatalf("zero disposal proceeds should validate: %v", err)
	}
}

func TestNetValueComputesRealArithmeticOnlyAfterDisposal(t *testing.T) {
	o, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo")
	proceeds := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(10_000), "EV-SALE")
	expenses := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1_500), "EV-CONTRACTOR-INVOICE")

	o.DisposalMethod = DisposalSold
	o.DisposalProceeds = proceeds
	o.Expenses = expenses
	// Still not disposed: NetValue must stay zero.
	if nv := o.NetValue(); nv.Amount != 0 {
		t.Fatalf("expected zero NetValue before Status==DISPOSED, got %s", nv.Amount)
	}

	o.Status = StatusDisposed
	nv := o.NetValue()
	want := quantum.MajorUnits(10_000 - 1_500)
	if nv.Amount != want {
		t.Fatalf("expected net value %s, got %s", want, nv.Amount)
	}
	if len(nv.EvidenceIDs) != 2 {
		t.Fatalf("expected 2 evidence IDs (union of proceeds+expenses), got %v", nv.EvidenceIDs)
	}
}

func TestRegistryFullLifecycle(t *testing.T) {
	reg, err := NewRegistry("CASE-1")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	o, err := New("SLV-1", "CASE-1", "CLM-1", "damaged reefer cargo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := reg.Register(o); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := reg.RecordAssessment("SLV-1", quantum.NewEvidenceBackedAmount(quantum.MajorUnits(12_000), "EV-SURVEY")); err != nil {
		t.Fatalf("RecordAssessment: %v", err)
	}
	got, _ := reg.Get("SLV-1")
	if got.Status != StatusAssessed {
		t.Fatalf("expected StatusAssessed after assessment, got %s", got.Status)
	}

	if err := reg.EngageContractor("SLV-1", party.PartyID("PTY-SALVOR"), 500); err != nil {
		t.Fatalf("EngageContractor: %v", err)
	}
	got, _ = reg.Get("SLV-1")
	if got.Status != StatusContracted {
		t.Fatalf("expected StatusContracted after engaging contractor, got %s", got.Status)
	}
	if got.Contractor != "PTY-SALVOR" {
		t.Fatalf("contractor not recorded")
	}

	proceeds := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(9_000), "EV-SALE-DOC")
	expenses := quantum.NewEvidenceBackedAmount(quantum.MajorUnits(1_000), "EV-INVOICE")
	if err := reg.RecordDisposal("SLV-1", DisposalSold, proceeds, expenses, 900); err != nil {
		t.Fatalf("RecordDisposal: %v", err)
	}
	got, _ = reg.Get("SLV-1")
	if got.Status != StatusDisposed {
		t.Fatalf("expected StatusDisposed, got %s", got.Status)
	}

	total := reg.TotalNetValueForClaim("CLM-1")
	want := quantum.MajorUnits(9_000 - 1_000)
	if total.Amount != want {
		t.Fatalf("expected total net value %s, got %s", want, total.Amount)
	}

	if err := reg.MarkAllocated("SLV-1"); err != nil {
		t.Fatalf("MarkAllocated: %v", err)
	}
	// Allocated operations drop out of TotalNetValueForClaim — prevents
	// double-counting across repeated quantum recomputation.
	if after := reg.TotalNetValueForClaim("CLM-1"); after.Amount != 0 {
		t.Fatalf("expected zero total after allocation (no double count), got %s", after.Amount)
	}
	if err := reg.MarkAllocated("SLV-1"); !errors.Is(err, ErrAlreadyAllocated) {
		t.Fatalf("expected ErrAlreadyAllocated on second call, got %v", err)
	}
}

func TestMarkAllocatedRefusesBeforeDisposal(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	o, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo")
	_ = reg.Register(o)
	if err := reg.MarkAllocated("SLV-1"); !errors.Is(err, ErrNotYetDisposed) {
		t.Fatalf("expected ErrNotYetDisposed, got %v", err)
	}
}

func TestRegisterRejectsCaseIDMismatch(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	o, _ := New("SLV-1", "CASE-2", "CLM-1", "cargo")
	if err := reg.Register(o); !errors.Is(err, ErrCaseIDMismatch) {
		t.Fatalf("expected ErrCaseIDMismatch, got %v", err)
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	o, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo")
	if err := reg.Register(o); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(o); !errors.Is(err, ErrDuplicateOperation) {
		t.Fatalf("expected ErrDuplicateOperation, got %v", err)
	}
}

func TestByClaimAndByStatus(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	o1, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo A")
	o2, _ := New("SLV-2", "CASE-1", "CLM-2", "cargo B")
	_ = reg.Register(o1)
	_ = reg.Register(o2)

	if got := reg.ByClaim("CLM-1"); len(got) != 1 || got[0].OperationID != "SLV-1" {
		t.Fatalf("ByClaim(CLM-1): unexpected result %+v", got)
	}
	if got := reg.ByStatus(StatusIdentified); len(got) != 2 {
		t.Fatalf("expected 2 identified operations, got %d", len(got))
	}
	if got := reg.ByStatus(StatusDisposed); len(got) != 0 {
		t.Fatalf("expected 0 disposed operations, got %d", len(got))
	}
}

func TestAddEvidenceRejectsEmpty(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	o, _ := New("SLV-1", "CASE-1", "CLM-1", "cargo")
	_ = reg.Register(o)
	if err := reg.AddEvidence("SLV-1", ""); !errors.Is(err, ErrEmptyEvidenceID) {
		t.Fatalf("expected ErrEmptyEvidenceID, got %v", err)
	}
	if err := reg.AddEvidence("SLV-1", "EV-1"); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	got, _ := reg.Get("SLV-1")
	if len(got.EvidenceIDs) != 1 || got.EvidenceIDs[0] != "EV-1" {
		t.Fatalf("evidence not recorded: %+v", got.EvidenceIDs)
	}
}

func TestOperationNotFoundOnEveryMutator(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	if err := reg.SetStatus("NOPE", StatusAssessed); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("SetStatus: expected ErrOperationNotFound, got %v", err)
	}
	if err := reg.EngageContractor("NOPE", "PTY", 1); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("EngageContractor: expected ErrOperationNotFound, got %v", err)
	}
	if err := reg.RecordAssessment("NOPE", quantum.EvidenceBackedAmount{}); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("RecordAssessment: expected ErrOperationNotFound, got %v", err)
	}
	if err := reg.RecordDisposal("NOPE", DisposalSold, quantum.EvidenceBackedAmount{}, quantum.EvidenceBackedAmount{}, 1); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("RecordDisposal: expected ErrOperationNotFound, got %v", err)
	}
	if err := reg.AddEvidence("NOPE", "EV-1"); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("AddEvidence: expected ErrOperationNotFound, got %v", err)
	}
	if err := reg.MarkAllocated("NOPE"); !errors.Is(err, ErrOperationNotFound) {
		t.Fatalf("MarkAllocated: expected ErrOperationNotFound, got %v", err)
	}
}

// TestConcurrentRegistryAccess proves Registry is safe under -race:
// concurrent registration, mutation and reads over disjoint operations.
func TestConcurrentRegistryAccess(t *testing.T) {
	reg, _ := NewRegistry("CASE-1")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := "SLV-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
			o, err := New(id, "CASE-1", "CLM-1", "cargo item")
			if err != nil {
				t.Errorf("New: %v", err)
				return
			}
			if err := reg.Register(o); err != nil {
				t.Errorf("Register: %v", err)
				return
			}
			_ = reg.EngageContractor(id, "PTY-SALVOR", 1)
			_, _ = reg.Get(id)
			_ = reg.All()
			_ = reg.Count()
		}()
	}
	wg.Wait()
	if reg.Count() != 50 {
		t.Fatalf("expected 50 operations, got %d", reg.Count())
	}
}

// ---- Guardrail-style: no liability/coverage/settlement field anywhere ----

var forbiddenFieldSubstrings = []string{
	"liable", "liability", "guilt", "guilty", "coverage", "covered",
	"settlement", "verdict", "fault", "responsible",
}

// TestNoLiabilityOrCoverageField mirrors
// recovery.TestNoLiabilityDeterminationField: reflects over every
// exported struct in this package and refuses any field name that could
// be read as a liability, guilt, coverage, or settlement determination.
// "Contractor" and "Responsible" would collide on a naive substring
// scan of "respons" only if mis-scoped — this checks whole recognisable
// stems, and Contractor does not contain "responsible".
func TestNoLiabilityOrCoverageField(t *testing.T) {
	types := []any{Operation{}}
	for _, v := range types {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			name := strings.ToLower(rt.Field(i).Name)
			for _, bad := range forbiddenFieldSubstrings {
				if strings.Contains(name, bad) {
					t.Errorf("%s.%s: field name contains forbidden substring %q — this package must never "+
						"carry a liability/coverage/settlement determination field", rt.Name(), rt.Field(i).Name, bad)
				}
			}
		}
	}
}

// TestNoOpaqueConfidenceField reflects over Operation and refuses any
// float field — matching the Final Design §39 forbidden-list rule
// enforced tree-wide by pkg/insurance/guardrails: no single opaque
// confidence score anywhere in the insurance domain.
func TestNoOpaqueConfidenceField(t *testing.T) {
	rt := reflect.TypeOf(Operation{})
	for i := 0; i < rt.NumField(); i++ {
		k := rt.Field(i).Type.Kind()
		if k == reflect.Float32 || k == reflect.Float64 {
			t.Errorf("Operation.%s is a float field — this domain never carries an opaque confidence score",
				rt.Field(i).Name)
		}
	}
}

func TestKnownStatusesAndDisposalMethodsAreExhaustive(t *testing.T) {
	if len(KnownStatuses()) != 7 {
		t.Fatalf("expected 7 known statuses, got %d: %v", len(KnownStatuses()), KnownStatuses())
	}
	if len(KnownDisposalMethods()) != 6 {
		t.Fatalf("expected 6 known disposal methods, got %d: %v", len(KnownDisposalMethods()), KnownDisposalMethods())
	}
	if IsKnownDisposalMethod(DisposalNotYetDisposed) {
		t.Fatal("DisposalNotYetDisposed must NOT be a known (real) disposal method")
	}
}

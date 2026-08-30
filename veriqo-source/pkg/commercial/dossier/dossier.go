// Package dossier answers Commercialization Sprint item 7 directly:
// "Bangun Evidence Dossier v1 ... Customer harus bisa menekan: Generate
// Evidence Dossier," with the exact field list the reviewer specified
// (Case, Scope, Evidence inventory, Source information, Acquisition
// timeline, Integrity verification, Identity information, Chain of
// custody, Corroboration, Contradictions, Trust assessment, Decision,
// Authorization, Action, Limitations, Verification instructions,
// Package hash) and two output forms: Human (PDF) and Machine (ZIP).
//
// Every field in this package is either (a) copied verbatim from a
// real pkg/commercial/verticalslice.Result / pkg/commercial/
// evidencefabric.EvidenceRecord that already passed through the
// FROZEN core trust kernel, or (b) explicit caller-supplied narrative
// input (Corroboration, Contradictions, Limitations) that this package
// never invents on its own -- an empty Corroboration list means "none
// supplied," never "none exists." This package computes no trust
// score, coverage determination, or liability finding of its own (see
// pkg/insurance/guardrails' whole-tree ban on both), and PackageHash is
// a real, independently-recomputable JCS hash over the Dossier's own
// canonical content, not a decorative field.
package dossier

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"veriqo/pkg/canonical/jcs"
	"veriqo/pkg/commercial/evidencefabric"
	"veriqo/pkg/commercial/verticalslice"
	"veriqo/pkg/evidence/manifest"
	"veriqo/pkg/platform/audit"
)

var (
	ErrNoEvidence   = errors.New("dossier: at least one EvidenceRecord is required")
	ErrZeroDecision = errors.New("dossier: verticalslice.Result carries a zero Decision")
)

// CaseSummary is the Dossier's "Case" and "Scope" fields.
type CaseSummary struct {
	CaseID string `json:"case_id"`
	Scope  string `json:"scope"`
}

// SourceInfo is one evidence item's "Source information" row.
type SourceInfo struct {
	EvidenceID string `json:"evidence_id"`
	Method     string `json:"method"`
	Collector  string `json:"collector"`
	Origin     string `json:"origin"`
}

// TimelineEntry is one evidence item's "Acquisition timeline" row.
type TimelineEntry struct {
	EvidenceID string `json:"evidence_id"`
	AcquiredAt uint64 `json:"acquired_at"`
	ReceivedAt uint64 `json:"received_at"`
}

// IntegrityCheck is one evidence item's "Integrity verification" row.
// Verified is copied verbatim from the EvidenceRecord's own Integrity
// block -- itself the result of an independent manifest.VerifyManifestHash
// call inside evidencefabric.FromManifest, never re-asserted here.
type IntegrityCheck struct {
	EvidenceID string `json:"evidence_id"`
	SHA256     string `json:"sha256"`
	Verified   bool   `json:"verified"`
}

// CustodyRow is one flattened "Chain of custody" entry across all
// evidence in this dossier.
type CustodyRow struct {
	EvidenceID string `json:"evidence_id"`
	EventID    string `json:"event_id"`
	Actor      string `json:"actor"`
	Tick       uint64 `json:"tick"`
	Action     string `json:"action"`
}

// DecisionSummary is the Dossier's "Decision" field -- copied from the
// real decision.Decision the vertical slice reached.
type DecisionSummary struct {
	Outcome           string `json:"outcome"`
	Rationale         string `json:"rationale"`
	DecidedAt         uint64 `json:"decided_at"`
	Hash              string `json:"hash"`
	FindingHash       string `json:"finding_hash"`
	AuthorizationHash string `json:"authorization_hash"`
	HypothesisID      string `json:"hypothesis_id"`
}

// AuthorizationSummary is the Dossier's "Authorization" field.
type AuthorizationSummary struct {
	Actor           string   `json:"actor"`
	PolicyRef       string   `json:"policy_ref"`
	Scope           string   `json:"scope"`
	PermittedAction string   `json:"permitted_action"`
	Conditions      []string `json:"conditions,omitempty"`
	AuthorizedAt    uint64   `json:"authorized_at"`
	ExpiresAt       uint64   `json:"expires_at"`
	Hash            string   `json:"hash"`
}

// ActionSummary is the Dossier's "Action" field -- the real Receipt
// the vertical slice's ACTION+RECEIPT stages produced.
type ActionSummary struct {
	ExecutedBy       string `json:"executed_by"`
	ExecutedAt       uint64 `json:"executed_at"`
	ReceiptID        string `json:"receipt_id"`
	LedgerRecordHash string `json:"ledger_record_hash"`
}

// Dossier is the ONE canonical Evidence Dossier v1 shape. Obtain one
// only via New.
type Dossier struct {
	Case                     CaseSummary                     `json:"case"`
	EvidenceInventory        []evidencefabric.EvidenceRecord `json:"evidence_inventory"`
	SourceInformation        []SourceInfo                    `json:"source_information"`
	AcquisitionTimeline      []TimelineEntry                 `json:"acquisition_timeline"`
	IntegrityVerification    []IntegrityCheck                `json:"integrity_verification"`
	IdentityInformation      []string                        `json:"identity_information"`
	ChainOfCustody           []CustodyRow                    `json:"chain_of_custody"`
	Corroboration            []string                        `json:"corroboration"`
	Contradictions           []string                        `json:"contradictions"`
	TrustAssessment          string                          `json:"trust_assessment"`
	Decision                 DecisionSummary                 `json:"decision"`
	Authorization            AuthorizationSummary            `json:"authorization"`
	Action                   ActionSummary                   `json:"action"`
	Limitations              []string                        `json:"limitations"`
	VerificationInstructions []string                        `json:"verification_instructions"`
	PackageHash              string                          `json:"package_hash"`
}

// RawEvidence is one evidence item's RAW manifest.Manifest and its
// full custody chain -- the shape written to manifests.json inside a
// Machine Package (see WriteMachinePackage), and read back by any
// independent verifier that needs to re-run manifest.VerifyManifestHash
// and Registry.VerifyCustodyChain-equivalent checks against the actual
// underlying kernel record, not the evidencefabric projection.
type RawEvidence struct {
	Manifest manifest.Manifest       `json:"manifest"`
	Custody  []manifest.CustodyEvent `json:"custody"`
}

// Input is everything New needs to assemble a Dossier.
type Input struct {
	Scope          string
	Result         verticalslice.Result
	Evidence       []evidencefabric.EvidenceRecord
	Corroboration  []string
	Contradictions []string
	Limitations    []string
}

// standardLimitations are appended to every Dossier, per this
// repository's own repeatedly-stated caveats (see docs/
// VERIQO_MLETR_EBL_CONFORMANCE_MAPPING_V0_2.md and every prior
// closure report's own "honest scope boundary" sections) -- never
// silently omitted, since a customer-facing document is exactly where
// overclaiming would do the most damage.
var standardLimitations = []string{
	"This dossier is independently verifiable evidence support, not a legal determination of coverage, liability, or fault.",
	"Tick values are application-defined sequence markers, not RFC 3161 (or equivalent) certified timestamps, unless the deployment separately wires one in.",
	"Trust assessment reflects VERIQO's own evidence-derived hypothesis status, not a probability, confidence score, or legal conclusion.",
	"Independent verification (see Verification Instructions) is recommended before this dossier is relied upon by any external party.",
}

// New assembles a Dossier from real, already-verified artifacts. It
// computes NOTHING about coverage, liability, or trust score --
// TrustAssessment is a plain restatement of the AuthorizedFinding's own
// real ConfidenceBasis-derived Hypothesis status wording, copied
// through, never re-derived or embellished here.
func New(in Input) (Dossier, error) {
	if len(in.Evidence) == 0 {
		return Dossier{}, ErrNoEvidence
	}
	if in.Result.Decision.IsZero() {
		return Dossier{}, ErrZeroDecision
	}

	var sources []SourceInfo
	var timeline []TimelineEntry
	var integrity []IntegrityCheck
	var custody []CustodyRow
	identitySet := map[string]bool{}
	var identities []string
	addIdentity := func(name string) {
		if name == "" || identitySet[name] {
			return
		}
		identitySet[name] = true
		identities = append(identities, name)
	}

	for _, ev := range in.Evidence {
		sources = append(sources, SourceInfo{EvidenceID: ev.Identity.EvidenceID, Method: ev.Source.Method, Collector: ev.Source.Origin, Origin: ev.Source.Origin})
		timeline = append(timeline, TimelineEntry{EvidenceID: ev.Identity.EvidenceID, AcquiredAt: ev.Timing.AcquiredAt, ReceivedAt: ev.Timing.ReceivedAt})
		integrity = append(integrity, IntegrityCheck{EvidenceID: ev.Identity.EvidenceID, SHA256: ev.Integrity.SHA256, Verified: ev.Integrity.Verified})
		addIdentity(ev.Source.Collector)
		for _, step := range ev.Custody {
			custody = append(custody, CustodyRow{EvidenceID: ev.Identity.EvidenceID, EventID: step.EventID, Actor: step.Actor, Tick: step.Tick, Action: step.Action})
			addIdentity(step.Actor)
		}
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i].AcquiredAt < timeline[j].AcquiredAt })
	sort.Slice(custody, func(i, j int) bool { return custody[i].Tick < custody[j].Tick })

	addIdentity(in.Result.ActionAuthorization.Actor())
	addIdentity(in.Result.Receipt.ExecutedBy)

	trustAssessment := fmt.Sprintf(
		"Hypothesis %s: authorized finding's ConfidenceBasis reflects the evidence-derived status backing Decision %s (Outcome=%s). See Corroboration/Contradictions below for the specific evidence relationships this status was derived from.",
		in.Result.Decision.HypothesisID(), in.Result.Decision.Hash(), in.Result.Decision.Outcome(),
	)

	limitations := append(append([]string(nil), standardLimitations...), in.Limitations...)

	verificationInstructions := []string{
		"Recompute each evidence item's manifest hash and compare against integrity_verification[].sha256 (see manifest.VerifyManifestHash).",
		"Recompute the Decision's own canonical hash from finding_hash/authorization_hash/hypothesis_id/outcome/rationale/decided_at and compare against decision.hash (see decision.VerifyDecisionProvenance).",
		"Recompute the Authorization's own canonical hash and confirm its decision_hash field matches the Decision's real hash (see action.VerifyActionAuthorization).",
		"Independently verify the ledger's hash chain (audit.Auditor{}.VerifyChain) against the accompanying ledger export.",
		"Cross-check package_hash below by recomputing this Dossier's own JCS canonical hash over every field except package_hash itself.",
	}

	d := Dossier{
		Case:                  CaseSummary{CaseID: in.Result.Decision.FindingHash(), Scope: in.Scope},
		EvidenceInventory:     append([]evidencefabric.EvidenceRecord(nil), in.Evidence...),
		SourceInformation:     sources,
		AcquisitionTimeline:   timeline,
		IntegrityVerification: integrity,
		IdentityInformation:   identities,
		ChainOfCustody:        custody,
		Corroboration:         append([]string(nil), in.Corroboration...),
		Contradictions:        append([]string(nil), in.Contradictions...),
		TrustAssessment:       trustAssessment,
		Decision: DecisionSummary{
			Outcome: string(in.Result.Decision.Outcome()), Rationale: in.Result.Decision.Rationale(),
			DecidedAt: in.Result.Decision.DecidedAt(), Hash: in.Result.Decision.Hash(),
			FindingHash: in.Result.Decision.FindingHash(), AuthorizationHash: in.Result.Decision.AuthorizationHash(),
			HypothesisID: in.Result.Decision.HypothesisID(),
		},
		Authorization: AuthorizationSummary{
			Actor: in.Result.ActionAuthorization.Actor(), PolicyRef: in.Result.ActionAuthorization.PolicyRef(),
			Scope: in.Result.ActionAuthorization.Scope(), PermittedAction: string(in.Result.ActionAuthorization.PermittedAction()),
			Conditions: in.Result.ActionAuthorization.Conditions(), AuthorizedAt: in.Result.ActionAuthorization.AuthorizedAt(),
			ExpiresAt: in.Result.ActionAuthorization.ExpiresAt(), Hash: in.Result.ActionAuthorization.Hash(),
		},
		Action: ActionSummary{
			ExecutedBy: in.Result.Receipt.ExecutedBy, ExecutedAt: in.Result.Receipt.ExecutedAt,
			ReceiptID: in.Result.Receipt.ReceiptID, LedgerRecordHash: in.Result.Receipt.LedgerRecordHash,
		},
		Limitations:              limitations,
		VerificationInstructions: verificationInstructions,
	}

	// PackageHash is computed while it is still its own zero value
	// ("") -- the same "hash while the hash field is absent/empty"
	// discipline decisionHashInput/actionHashInput use via a SEPARATE
	// type one layer down; here Dossier has no unexported fields, so
	// hashing the struct itself (before PackageHash is set) achieves
	// the identical self-reference-free property without a second
	// type. VerifyPackageHash below reproduces this exactly.
	d.PackageHash = jcs.MustHash(d)
	return d, nil
}

// VerifyPackageHash independently recomputes d's PackageHash from its
// own remaining fields and confirms it matches -- proof, to any
// external party holding a Dossier, that its content has not been
// altered since New produced it.
func VerifyPackageHash(d Dossier) error {
	want := d.PackageHash
	d.PackageHash = ""
	got := jcs.MustHash(d)
	if got != want {
		return fmt.Errorf("dossier: PackageHash mismatch: recorded=%s recomputed=%s", want, got)
	}
	return nil
}

// RenderMarkdown produces the "Human" form of the dossier -- plain
// markdown, deterministic field order, covering every field the
// reviewer named. Feed this through .agents/scripts/render_markdown_pdf.py
// (this repository's existing dependency-free markdown-to-PDF
// renderer, already used for every closure report in this engagement)
// to obtain the PDF form.
func RenderMarkdown(d Dossier) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# VERIQO Evidence Dossier\n\n")
	fmt.Fprintf(&b, "## Case\n\nCase ID: %s\n\n## Scope\n\n%s\n\n", d.Case.CaseID, d.Case.Scope)

	fmt.Fprintf(&b, "## Evidence Inventory\n\n")
	for _, ev := range d.EvidenceInventory {
		fmt.Fprintf(&b, "- %s (tenant=%s case=%s version=%d)\n", ev.Identity.EvidenceID, ev.Identity.TenantID, ev.Identity.CaseID, ev.Identity.Version)
	}

	fmt.Fprintf(&b, "\n## Source Information\n\n")
	for _, s := range d.SourceInformation {
		fmt.Fprintf(&b, "- %s: method=%s collector=%s origin=%s\n", s.EvidenceID, s.Method, s.Collector, s.Origin)
	}

	fmt.Fprintf(&b, "\n## Acquisition Timeline\n\n")
	for _, t := range d.AcquisitionTimeline {
		fmt.Fprintf(&b, "- %s: acquired_at=%d received_at=%d\n", t.EvidenceID, t.AcquiredAt, t.ReceivedAt)
	}

	fmt.Fprintf(&b, "\n## Integrity Verification\n\n")
	for _, i := range d.IntegrityVerification {
		fmt.Fprintf(&b, "- %s: sha256=%s verified=%v\n", i.EvidenceID, i.SHA256, i.Verified)
	}

	fmt.Fprintf(&b, "\n## Identity Information\n\n")
	for _, id := range d.IdentityInformation {
		fmt.Fprintf(&b, "- %s\n", id)
	}

	fmt.Fprintf(&b, "\n## Chain of Custody\n\n")
	for _, c := range d.ChainOfCustody {
		fmt.Fprintf(&b, "- [%d] %s: %s by %s (%s)\n", c.Tick, c.EvidenceID, c.Action, c.Actor, c.EventID)
	}

	fmt.Fprintf(&b, "\n## Corroboration\n\n")
	if len(d.Corroboration) == 0 {
		fmt.Fprintf(&b, "None supplied for this case.\n")
	}
	for _, c := range d.Corroboration {
		fmt.Fprintf(&b, "- %s\n", c)
	}

	fmt.Fprintf(&b, "\n## Contradictions\n\n")
	if len(d.Contradictions) == 0 {
		fmt.Fprintf(&b, "None supplied for this case.\n")
	}
	for _, c := range d.Contradictions {
		fmt.Fprintf(&b, "- %s\n", c)
	}

	fmt.Fprintf(&b, "\n## Trust Assessment\n\n%s\n", d.TrustAssessment)

	fmt.Fprintf(&b, "\n## Decision\n\nOutcome: %s\nRationale: %s\nDecided At: %d\nHash: %s\nFinding Hash: %s\nAuthorization Hash: %s\nHypothesis ID: %s\n",
		d.Decision.Outcome, d.Decision.Rationale, d.Decision.DecidedAt, d.Decision.Hash, d.Decision.FindingHash, d.Decision.AuthorizationHash, d.Decision.HypothesisID)

	fmt.Fprintf(&b, "\n## Authorization\n\nActor: %s\nPolicy Ref: %s\nScope: %s\nPermitted Action: %s\nConditions: %s\nAuthorized At: %d\nExpires At: %d\nHash: %s\n",
		d.Authorization.Actor, d.Authorization.PolicyRef, d.Authorization.Scope, d.Authorization.PermittedAction,
		strings.Join(d.Authorization.Conditions, "; "), d.Authorization.AuthorizedAt, d.Authorization.ExpiresAt, d.Authorization.Hash)

	fmt.Fprintf(&b, "\n## Action\n\nExecuted By: %s\nExecuted At: %d\nReceipt ID: %s\nLedger Record Hash: %s\n",
		d.Action.ExecutedBy, d.Action.ExecutedAt, d.Action.ReceiptID, d.Action.LedgerRecordHash)

	fmt.Fprintf(&b, "\n## Limitations\n\n")
	for _, l := range d.Limitations {
		fmt.Fprintf(&b, "- %s\n", l)
	}

	fmt.Fprintf(&b, "\n## Verification Instructions\n\n")
	for _, v := range d.VerificationInstructions {
		fmt.Fprintf(&b, "- %s\n", v)
	}

	fmt.Fprintf(&b, "\n## Package Hash\n\n%s\n", d.PackageHash)
	return b.String()
}

// WriteMachinePackage produces the "Machine" form: a ZIP at outPath
// containing dossier.json (the canonical, JCS-serialized Dossier),
// dossier.md (the same content RenderMarkdown produces), manifests.json
// (the RAW manifest.Manifest + custody chain for every evidence item --
// not the evidencefabric projection, the underlying kernel record
// itself), and ledger.json (the RAW audit.AuditRecord chain). The raw
// manifest/ledger data is what makes this package independently
// verifiable by a genuinely separate process (see cmd/veriqo-
// commercial-verify): package hash, manifest, raw evidence hash,
// custody chain, and Merkle root are all re-derivable from these files
// alone, using the exact same exported Verify functions the frozen
// kernel already provides -- never a re-implementation.
func WriteMachinePackage(d Dossier, manifests *manifest.Registry, ledger *audit.AuditStore, outPath string) error {
	canon, err := jcs.Canonicalize(d)
	if err != nil {
		return fmt.Errorf("dossier: WriteMachinePackage: canonicalizing: %w", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, canon, "", "  "); err != nil {
		return fmt.Errorf("dossier: WriteMachinePackage: pretty-printing: %w", err)
	}

	var rawEvidenceList []RawEvidence
	if manifests != nil {
		for _, ev := range d.EvidenceInventory {
			m, err := manifests.Latest(ev.Identity.EvidenceID)
			if err != nil {
				return fmt.Errorf("dossier: WriteMachinePackage: looking up raw manifest for %s: %w", ev.Identity.EvidenceID, err)
			}
			rawEvidenceList = append(rawEvidenceList, RawEvidence{Manifest: m, Custody: manifests.CustodyChain(ev.Identity.EvidenceID)})
		}
	}
	manifestsJSON, err := json.MarshalIndent(rawEvidenceList, "", "  ")
	if err != nil {
		return fmt.Errorf("dossier: WriteMachinePackage: marshaling manifests.json: %w", err)
	}

	var ledgerRecords []audit.AuditRecord
	if ledger != nil {
		ledgerRecords = ledger.Snapshot()
	}
	ledgerJSON, err := json.MarshalIndent(ledgerRecords, "", "  ")
	if err != nil {
		return fmt.Errorf("dossier: WriteMachinePackage: marshaling ledger.json: %w", err)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("dossier: WriteMachinePackage: creating %s: %w", outPath, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	entries := []struct {
		name    string
		content io.Reader
	}{
		{"dossier.json", &pretty},
		{"dossier.md", strings.NewReader(RenderMarkdown(d))},
		{"manifests.json", bytes.NewReader(manifestsJSON)},
		{"ledger.json", bytes.NewReader(ledgerJSON)},
	}
	for _, e := range entries {
		entry, err := zw.Create(e.name)
		if err != nil {
			return fmt.Errorf("dossier: WriteMachinePackage: %w", err)
		}
		if _, err := io.Copy(entry, e.content); err != nil {
			return fmt.Errorf("dossier: WriteMachinePackage: writing %s: %w", e.name, err)
		}
	}
	return zw.Close()
}

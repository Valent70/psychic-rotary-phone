package capsule

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"veriqo/pkg/verification"
)

// The worked case exists because a capsule without one leaves an
// assessor with nothing to verify.
//
// Six of the eight steps in the Independent Verification Kit --
// artefact hashes, signature, provenance, ledger lineage, replay,
// revocation -- need actual case artefacts. A capsule carrying only
// registers and manifests is a capsule an assessor can read and cannot
// CHECK, which puts them back where they started: believing a
// document.
//
// So the capsule carries a complete, small, deterministic case. It is
// synthetic, and the capsule says so in three places, because a
// synthetic case establishes that the machinery works and establishes
// nothing whatever about real data.

// caseSeed is a fixed 32-byte seed for the case's signing key.
//
// A FIXED KEY IN SOURCE. That is normally a serious defect, and here
// it is the correct choice for one reason: the capsule must be
// byte-identical between builds, or an assessor cannot tell a change
// from a rebuild. The key signs one synthetic passport in a
// demonstration bundle and nothing else. It is not a VERIQO key, it
// protects nothing, and the capsule's own README says so.
var caseSeed = []byte{
	0x56, 0x45, 0x52, 0x49, 0x51, 0x4f, 0x2d, 0x44, 0x45, 0x4d, 0x4f, 0x2d, 0x4b, 0x45, 0x59, 0x2d,
	0x4e, 0x4f, 0x54, 0x2d, 0x53, 0x45, 0x43, 0x52, 0x45, 0x54, 0x2d, 0x30, 0x30, 0x30, 0x30, 0x31,
}

const caseKeyID = "veriqo-demonstration-key-not-a-production-key"

// caseAt is the case's fixed instant, for the same determinism reason.
var caseAt = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// addWorkedCase puts a complete verifiable case into the capsule.
func addWorkedCase(b *verification.Builder) error {
	priv := ed25519.NewKeyFromSeed(caseSeed)
	pub := priv.Public().(ed25519.PublicKey)

	if err := b.Add("case/README.txt", []byte(caseReadme)); err != nil {
		return err
	}

	// --- the raw artefacts -------------------------------------------
	loading := []byte("LOADING SURVEY -- SYNTHETIC\n" +
		"vessel: DEMO CARRIER\nquantity: 60,000.000 MT\n" +
		"basis: shore tank, 15C, in air, API MPMS Ch.12\n")
	discharge := []byte("DISCHARGE SURVEY -- SYNTHETIC\n" +
		"vessel: DEMO CARRIER\nquantity: 58,200.000 MT\n" +
		"basis: shore tank, 15C, in air, API MPMS Ch.12\n")
	if err := b.Add("case/artefacts/e1v1.txt", loading); err != nil {
		return err
	}
	if err := b.Add("case/artefacts/e2v1.txt", discharge); err != nil {
		return err
	}

	lh, dh := sha256sum(loading), sha256sum(discharge)

	if err := b.AddJSON("evidence/versions.json", []map[string]any{
		{"id": "evidenceversion:e1v1", "sha256": lh,
			"artefact_path": "case/artefacts/e1v1.txt", "size_bytes": len(loading)},
		{"id": "evidenceversion:e2v1", "sha256": dh,
			"artefact_path": "case/artefacts/e2v1.txt", "size_bytes": len(discharge)},
	}); err != nil {
		return err
	}

	// --- provenance: two producers, deliberately -----------------------
	if err := b.AddJSON("provenance/records.json", []map[string]any{
		{"id": "provenance:p1", "source_content_hash": lh, "path": []map[string]any{
			{"party_id": "load-terminal", "role": "OBSERVER", "at": caseAt},
			{"party_id": "inspector-a", "role": "PRODUCER", "at": caseAt.Add(time.Hour)},
			{"party_id": "veriqo", "role": "RECIPIENT", "at": caseAt.Add(2 * time.Hour)},
		}},
		{"id": "provenance:p2", "source_content_hash": dh, "path": []map[string]any{
			{"party_id": "discharge-terminal", "role": "OBSERVER", "at": caseAt.AddDate(0, 1, 0)},
			{"party_id": "inspector-b", "role": "PRODUCER",
				"at": caseAt.AddDate(0, 1, 0).Add(time.Hour)},
			{"party_id": "veriqo", "role": "RECIPIENT",
				"at": caseAt.AddDate(0, 1, 0).Add(2 * time.Hour)},
		}},
	}); err != nil {
		return err
	}

	// --- the ledger, chained the way pkg/ledger does it ---------------
	events := []map[string]any{
		{"actor": "service:ingest", "tenant_id": "t-demo", "action": "EVIDENCE_ACQUIRED",
			"subject": "evidenceversion:e1v1", "outcome": "SUCCEEDED",
			"purpose": "CASE_INVESTIGATION", "policy_decision": "PERMIT baseline/ingest"},
		{"actor": "service:ingest", "tenant_id": "t-demo", "action": "EVIDENCE_ACQUIRED",
			"subject": "evidenceversion:e2v1", "outcome": "SUCCEEDED",
			"purpose": "CASE_INVESTIGATION", "policy_decision": "PERMIT baseline/ingest"},
		{"actor": "human:analyst-1", "tenant_id": "t-demo", "action": "CLAIM_PROPOSED",
			"subject": "claim:c1", "outcome": "SUCCEEDED",
			"purpose": "CASE_INVESTIGATION", "policy_decision": "PERMIT baseline/propose"},
		{"actor": "human:reviewer-1", "tenant_id": "t-demo", "action": "FINDING_APPROVED",
			"subject": "finding:f1", "outcome": "SUCCEEDED",
			"purpose": "CASE_INVESTIGATION", "policy_decision": "PERMIT baseline/approve"},
	}
	var recs []map[string]any
	prev := "veriqo-ledger-genesis-v1"
	for i, ev := range events {
		h, err := verification.RecordDigest(uint64(i), prev, ev)
		if err != nil {
			return err
		}
		recs = append(recs, map[string]any{
			"height": i, "prev_hash": prev, "event": ev, "hash": h})
		prev = h
	}
	if err := b.AddJSON("ledger/records.json", recs); err != nil {
		return err
	}

	// --- the replay record --------------------------------------------
	//
	// One step is DETERMINISTIC and re-executable; one is RECORDED.
	// A capsule carrying only deterministic steps would misrepresent
	// what replay establishes in a real case.
	quantum := map[string]any{
		"quantity_difference": -1800.0, "unit": "MT",
		"tolerance_allowance": 300.0, "excess_over_tolerance": 1500.0,
		"within_tolerance": false,
	}
	qh, err := hashCanonical(quantum)
	if err != nil {
		return err
	}
	if err := b.AddJSON("replay/steps.json", []map[string]any{
		{"name": "quantum.Compute", "kind": "DETERMINISTIC",
			"input": map[string]any{"loaded": 60000.0, "discharged": 58200.0,
				"contract_quantity": 60000.0, "tolerance_percent": 0.5},
			"output": quantum, "output_hash": qh},
		{"name": "document-extraction", "kind": "RECORDED",
			"input":       map[string]any{"evidence": "evidenceversion:e1v1"},
			"output_hash": "recorded: this step calls a model and is replayed from its recorded output"},
	}); err != nil {
		return err
	}

	// --- the passport --------------------------------------------------
	payload := map[string]any{
		"schema": "veriqo.passport/v1", "finding_id": "finding:f1", "case_id": "case-demo-1",
		"tenant_id": "t-demo",
		"statement": "the cargo discharged was 1,500 MT short of the contractual entitlement " +
			"after the 0.5% tolerance",
		"scope":         "the loading and discharge surveys only; no third measurement exists",
		"qualification": "ASSURED",
		"limitations": []any{
			"THIS IS A SYNTHETIC CASE. It establishes that the machinery works and " +
				"establishes nothing about real data",
			"the two surveys are the only measurements; a third would change the picture",
			"evidence completeness confidence is LOW",
		},
		"independently_validated": false,
		"proposed_by":             "human:analyst-1",
		"approved_by":             "human:reviewer-1",
		"approved_at":             caseAt.AddDate(0, 2, 0),
		"issued_at":               caseAt.AddDate(0, 2, 0),
	}
	digest, err := hashCanonical(payload)
	if err != nil {
		return err
	}
	if err := b.AddJSON("passport.json", map[string]any{
		"payload": payload, "digest": digest,
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(digest))),
		"key_id":    caseKeyID,
	}); err != nil {
		return err
	}
	return b.AddPublicKey(caseKeyID, pub)
}

func sha256sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func hashCanonical(v any) (string, error) {
	canon, err := verification.DefaultCanonicalizer().Canonicalize(v)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(canon)
	return hex.EncodeToString(s[:]), nil
}

var caseReadme = fmt.Sprintf(`THE WORKED CASE IN THIS CAPSULE IS SYNTHETIC.

It exists so that you can run the Independent Verification Kit against
something rather than against nothing. Without it, six of the kit's
eight steps report UNVERIFIABLE and you are back to reading a document.

What it DOES establish, if it verifies:

  - artefact digests are recomputable from the bytes
  - the ledger chains from genesis and every record rehashes
  - the passport digest is derived from its payload, not asserted
  - a deterministic pipeline step reproduces its recorded output
  - a step that calls a model is marked RECORDED and is NOT claimed to
    have been re-executed

What it establishes about VERIQO's behaviour on REAL data: nothing.
Every byte of it was written by VERIQO to be verified by you. That is
evidence debt ED-007, and it is the reason a real case cannot be put
here yet.

The signing key is fixed in source, at pkg/assurance/capsule. That
would be a serious defect for a production key and is the correct
choice for this one: the capsule must be byte-identical between builds
or you cannot tell a change from a rebuild. The key id says what it is:

  %s

It signs one synthetic passport and protects nothing.
`, caseKeyID)

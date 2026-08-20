package maritime

import (
	"context"
	"fmt"
)

// CustomerClaimsAdapter is a MaritimeEvidenceSource for
// customer/counterparty transaction claims -- broker-asserted
// statements about a deal (documentation claimed to exist, transaction
// sequence steps) that are explicitly NOT independently verified facts.
// Fixture mode returns records built directly from RWC-002's own
// corpus (pkg/rwc.RWC002Corpus.TransactionClaims): the literal
// "consignee listed on documentation is seller" claim, one of the
// three named red-flag phrases pkg/rwc/rwc002.go's own
// rwc002RedFlagPhrases checks for -- reused here verbatim, not
// reinvented.
//
// This package does not import pkg/rwc (see ais.go's doc comment for
// why); the literal values below are kept in hand-sync with
// pkg/rwc.RWC002Corpus.TransactionClaims.
//
// Real-file/real-API mode: a real integration would ingest a delivered
// transaction-document set (CI/CIS/KYC/MT103 etc, per
// pkg/rwc.RWC002Corpus.TransactionClaims's own document-name list) from
// a counterparty or broker portal, mapping each claimed step onto
// Evidence the same way Fetch does below -- still a CLAIM, never
// promoted to a verified fact by this adapter (see
// pkg/rwc.ClassifyProvenance for where that promotion decision
// actually belongs, computed from independent corroboration, not
// asserted here). Not implemented in this sandbox for the same
// no-egress reason documented in ais.go.
type CustomerClaimsAdapter struct {
	mode string
}

// NewCustomerClaimsAdapterFixture returns a CustomerClaimsAdapter whose
// Fetch serves the built-in claims fixture.
func NewCustomerClaimsAdapterFixture() *CustomerClaimsAdapter {
	return &CustomerClaimsAdapter{mode: "SIMULATED"}
}

func (a *CustomerClaimsAdapter) SourceID(ctx context.Context) (string, error) {
	return "CUSTOMER_CLAIMS_FIXTURE", nil
}

func (a *CustomerClaimsAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	return Capabilities{SupportsFixture: true}, nil
}

const claimsFixtureVesselID = "GUNUNG KEMALA / PERTAMINA 8003" // pkg/rwc.RWC002Corpus.VesselName

// claimsFixtureRecords: a subset of pkg/rwc.RWC002Corpus
// .TransactionClaims, literal strings, kept small and deliberately
// including the one entry rwc002RedFlagPhrases matches
// ("consignee listed on documentation is seller") so a caller
// exercising this adapter sees the same real red-flag text RWC-002's
// own pattern-score computation keys on.
var claimsFixtureRecords = []string{
	"consignee listed on documentation is seller",
	"TTO contract",
	"balance payment after inspection",
}

func (a *CustomerClaimsAdapter) Fetch(ctx context.Context, req FetchRequest) ([]Evidence, error) {
	if a.mode != "SIMULATED" {
		return nil, newRealModeNotImplemented("CustomerClaimsAdapter", a.mode)
	}
	if req.VesselID != "" && req.VesselID != claimsFixtureVesselID {
		return nil, nil
	}
	var out []Evidence
	for i, claim := range claimsFixtureRecords {
		ts := aisArrivalTick + 300 + uint64(i)
		if ts < req.Since || (req.Until != 0 && ts > req.Until) {
			continue
		}
		payload := []byte(fmt.Sprintf("CLAIM|vessel=%s|claim=%s", claimsFixtureVesselID, claim))
		out = append(out, Evidence{
			EventType:  "claim",
			Timestamp:  ts,
			Location:   "",
			DeliveryID: fmt.Sprintf("%s#CLAIM%d", claimsFixtureVesselID, i),
			Sequence:   i,
			RawPayload: payload,
			SourceID:   "CUSTOMER_CLAIMS_FIXTURE",
			Provider:   "BROKER_TRANSACTION_DOCS", // matches pkg/rwc.RWC002Corpus's own provider naming: a claim, not a verified fact
			Hash:       hashEvidence("claim", "claim", claimsFixtureVesselID, ts, i, payload),
		})
	}
	return out, nil
}

var _ MaritimeEvidenceSource = (*CustomerClaimsAdapter)(nil)

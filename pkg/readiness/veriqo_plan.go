package readiness

// VeriqoPlan is VERIQO's own procurement graph.
//
// Every blocker corresponds to an evidence debt in the assurance
// register. The register says what is missing; this says who sells it,
// what they must hand back, and what has to be true before they can
// start.
//
// The dependency edges are the part worth arguing with. A penetration
// test before a release-candidate freeze assesses code that will
// change, and a report against code that no longer exists is a report
// about nothing -- so the security engagements depend on the freeze.
func VeriqoPlan() (*Plan, error) {
	return NewPlan(
		Blocker{ID: "B-FREEZE", Dimension: Implementation,
			Owner:     "Head of Engineering",
			Validator: ReleaseAuthorityType,
			ExpectedEvidence: "a release-candidate tag, a frozen dependency set and a " +
				"reproducible build, so that every external report names a version that " +
				"still exists when it arrives",
			LeadTime: "2 weeks", Cost: CostInternal},

		Blocker{ID: "B-PENTEST", Dimension: Security,
			Owner:     "Head of Engineering",
			Validator: SecurityAssessor,
			ValidatorQualification: "CREST-equivalent, with source-available assessment " +
				"experience",
			ExpectedEvidence: "a scoped report naming what was and was not assessed, plus a " +
				"retest against the remediation. A report with no retest establishes that " +
				"findings existed, not that they were fixed",
			DependsOn: []string{"B-FREEZE"},
			LeadTime:  "6-10 weeks once engaged", Cost: CostHigh,
			Debts: []string{"ED-001"}},

		Blocker{ID: "B-CRYPTO", Dimension: Cryptography,
			Owner:     "Head of Engineering",
			Validator: Cryptographer,
			ValidatorQualification: "review of key derivation, canonical form and signature " +
				"construction -- a different specialist from a penetration tester",
			ExpectedEvidence: "a written opinion on the per-surface derivation, the RFC 8785 " +
				"implementation and the passport signing construction, naming any " +
				"divergence from the standard",
			DependsOn: []string{"B-FREEZE"},
			LeadTime:  "3-5 weeks once engaged", Cost: CostMedium,
			Debts: []string{"ED-011"}},

		Blocker{ID: "B-REDTEAM", Dimension: Security,
			Owner:     "Head of Engineering",
			Validator: RedTeam,
			ValidatorQualification: "adversarial testing with AI-agent experience; a network " +
				"red team is the wrong specialist for an injection defence",
			ExpectedEvidence: "an attempt to widen an agent grant from inside a document, " +
				"with a model in the loop, and a statement of what was tried and failed",
			DependsOn: []string{"B-FREEZE"},
			LeadTime:  "4-8 weeks once engaged", Cost: CostHigh,
			Debts: []string{"ED-005"}},

		Blocker{ID: "B-HSM", Dimension: Cryptography,
			Owner:     "Head of Platform",
			Validator: InfrastructureProvider,
			ValidatorQualification: "an HSM vendor or cloud KMS with an attestation " +
				"capability",
			ExpectedEvidence: "a provisioned key root with an attestation that the private " +
				"material has never left the module",
			LeadTime: "4-6 weeks", Cost: CostMedium,
			Debts: []string{"ED-002"}},

		Blocker{ID: "B-ANCHOR", Dimension: Cryptography,
			Owner:     "Head of Engineering",
			Validator: TimestampAuthority,
			ExpectedEvidence: "a countersigned ledger checkpoint, so that the chain's " +
				"existence at a time rests on somebody who is not VERIQO",
			LeadTime: "2-4 weeks", Cost: CostLow,
			Debts: []string{"ED-003"}},

		Blocker{ID: "B-SBOM", Dimension: Implementation,
			Owner:                  "Head of Platform",
			Validator:              InfrastructureProvider,
			ValidatorQualification: "a signing authority and an attestation service",
			ExpectedEvidence: "a signed bill of materials and an artefact signature a " +
				"consumer can verify without trusting VERIQO's own statement of its inputs",
			DependsOn: []string{"B-FREEZE"},
			LeadTime:  "3-5 weeks", Cost: CostMedium,
			Debts: []string{"ED-008"}},

		Blocker{ID: "B-LEGAL-SG", Dimension: Legal,
			Owner:                  "General Counsel",
			Validator:              Counsel,
			ValidatorQualification: "admitted in Singapore, maritime and data protection",
			ExpectedEvidence: "a scoped opinion on acquisition, retention and downstream use " +
				"for the restricted source classes, naming the purposes it covers",
			LeadTime: "6-12 weeks", Cost: CostMedium,
			Debts: []string{"ED-010"}},

		Blocker{ID: "B-LEGAL-EW", Dimension: Legal,
			Owner:     "General Counsel",
			Validator: Counsel,
			ValidatorQualification: "admitted in England and Wales, maritime and data " +
				"protection",
			ExpectedEvidence: "the same opinion for the second jurisdiction. An opinion for " +
				"one says nothing about another, which is why this is a separate engagement",
			LeadTime: "6-12 weeks", Cost: CostMedium,
			Debts: []string{"ED-010"}},

		Blocker{ID: "B-AIS", Dimension: DataRights,
			Owner:                  "Head of Product",
			Validator:              DataPartner,
			ValidatorQualification: "a commercial AIS provider with satellite coverage",
			ExpectedEvidence: "a signed data licence, the acquisition terms as written, and " +
				"a feed against which the resolution thresholds can be measured rather " +
				"than stated",
			LeadTime: "unknown -- commercial negotiation", Cost: CostHigh,
			Debts: []string{"ED-007"}},

		Blocker{ID: "B-CORPUS", Dimension: DataRights,
			Owner:     "Head of Evidence Engineering",
			Validator: CorpusPartner,
			ExpectedEvidence: "real documents under an agreement permitting redaction " +
				"testing, and an adversarial recovery attempt against the derivatives",
			DependsOn: []string{"B-LEGAL-SG"},
			LeadTime:  "unknown -- depends on a data agreement", Cost: CostUnknown,
			Debts: []string{"ED-004"},
			NotStartableBecause: "handling a customer's real documents needs the legal " +
				"position settled first"},

		Blocker{ID: "B-EVALSET", Dimension: Implementation,
			Owner:     "Head of Product",
			Validator: EvaluationPartner,
			ExpectedEvidence: "an evaluation set VERIQO did not construct, against which a " +
				"model can be qualified or fail to be",
			LeadTime: "unknown", Cost: CostUnknown,
			Debts: []string{"ED-009"}},

		Blocker{ID: "B-INFRA", Dimension: Operations,
			Owner:     "Head of Platform",
			Validator: InfrastructureProvider,
			ValidatorQualification: "a hosting provider with multi-region capacity and a " +
				"backup target that can be restored from, not merely written to",
			ExpectedEvidence: "a multi-host, multi-region deployment with a backup target, " +
				"on which a timed recovery and a 72-hour soak can actually be run",
			DependsOn: []string{"B-FREEZE", "B-HSM"},
			LeadTime:  "8-12 weeks", Cost: CostHigh,
			Debts: []string{"ED-006"}},

		Blocker{ID: "B-SOAK", Dimension: Operations,
			Owner:     "Head of Platform",
			Validator: ReleaseAuthorityType,
			ExpectedEvidence: "72 hours under real load with its incidents recorded, a " +
				"measured recovery time, and a restore-and-replay comparison",
			DependsOn: []string{"B-INFRA"},
			LeadTime:  "2 weeks after the environment exists", Cost: CostInternal,
			Debts: []string{"ED-006"}},

		Blocker{ID: "B-RELEASE", Dimension: Production,
			Owner:     "Release Authority",
			Validator: ReleaseAuthorityType,
			ExpectedEvidence: "a recorded decision by a named authority, on a scorecard with " +
				"no RED and every mandatory gate satisfied",
			DependsOn: []string{"B-PENTEST", "B-CRYPTO", "B-REDTEAM", "B-ANCHOR",
				"B-SBOM", "B-LEGAL-SG", "B-AIS", "B-CORPUS", "B-EVALSET", "B-SOAK"},
			LeadTime: "immediate once its dependencies clear", Cost: CostInternal},
	)
}

// Package capsule builds the Auditor Capsule.
//
// # What it replaces
//
// The normal way to give an outside party assurance is to hand them a
// document. The document says the tests pass, the controls exist, the
// coverage is 88%. Everything in it is an assertion by the party being
// assessed, and the assessor's only options are to believe it or to
// ask for a demonstration that the assessed party also runs.
//
// A capsule replaces "we say so" with "run it yourself". It carries
// the things an assessor needs to reach their own conclusions:
//
//	reproducible build      the exact commit and toolchain
//	version manifest        what every component is
//	dependency manifest     what came from outside, and from where
//	policy manifest         the rules, as data, not as prose
//	authority matrix        who may do what, as a table
//	test vectors            inputs and expected outputs they can rerun
//	evidence fixtures       the material the tests actually use
//	replay bundle           a decision, with every step
//	ledger verifier         a program that rehashes the chain
//	API contracts           every endpoint and its guarantees
//	threat model            what was considered, and what was not
//	assurance register      claims, debts, gates, and what is missing
//
// # The property that makes it worth building
//
// Nothing in a capsule asks the assessor to trust VERIQO. Every figure
// in it is either recomputable from the capsule's own contents or
// labelled as a judgement. The capsule states what it does NOT contain
// as prominently as what it does, because an assessor who does not
// know what was withheld cannot scope their assessment.
package capsule

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"veriqo/pkg/api"
	"veriqo/pkg/assurance/failureclass"
	"veriqo/pkg/assurance/register"
	"veriqo/pkg/assurance/selfdoubt"
	"veriqo/pkg/assurance/state"
	"veriqo/pkg/authority"
	"veriqo/pkg/evidence/redaction/corpus"
	"veriqo/pkg/gates"
	"veriqo/pkg/policy"
	"veriqo/pkg/scorecard"
	"veriqo/pkg/verification"
)

// Build describes how the capsule's subject was produced.
type Build struct {
	Module    string `json:"module"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
	// Reproduce is the exact command an assessor runs to rebuild it.
	Reproduce []string `json:"reproduce"`
	// Verified says whether VERIQO checked that the rebuild is
	// byte-identical. It is false here, and saying so is the point:
	// claiming reproducibility without having checked is the kind of
	// statement this whole package exists to stop.
	ReproducibilityVerified bool   `json:"reproducibility_verified"`
	Note                    string `json:"note"`
}

// Dependency is one thing that came from outside.
type Dependency struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Sum     string `json:"sum,omitempty"`
	// Direct distinguishes what VERIQO chose from what those choices
	// dragged in.
	Direct bool `json:"direct"`
}

// ThreatModel is what was considered.
//
// It carries NotConsidered as a required field, because a threat model
// that lists only what was thought about reads as complete.
type ThreatModel struct {
	Considered    []Threat `json:"considered"`
	NotConsidered []string `json:"not_considered"`
	Assumptions   []string `json:"assumptions"`
}

// Threat is one adversary and what stops them.
type Threat struct {
	ID    string `json:"id"`
	Actor string `json:"actor"`
	Goal  string `json:"goal"`
	// Control names what is supposed to stop it.
	Control string `json:"control"`
	// Tested says how it was tested, and by whom. "By VERIQO" is the
	// answer everywhere today.
	Tested string `json:"tested"`
	// Residual is what remains after the control.
	Residual string `json:"residual"`
}

// Contents is the capsule's own index, in words.
type Contents struct {
	Subject string    `json:"subject"`
	BuiltAt time.Time `json:"built_at"`
	// Includes and Excludes are both required. An assessor who does
	// not know what was withheld cannot scope their assessment.
	Includes []string `json:"includes"`
	Excludes []string `json:"excludes"`
	// HowToStart is the first thing an assessor should do.
	HowToStart []string `json:"how_to_start"`
}

// Options configure a capsule build.
type Options struct {
	Commit string
	At     time.Time
	// Deps are the module's dependencies. Passing them in rather than
	// shelling out keeps this package testable and deterministic.
	Deps []Dependency
}

// Build assembles the capsule into a verification.Builder, which the
// caller writes to disk.
//
// The capsule IS a verification bundle: an assessor runs veriqo-verify
// over it, and everything else in it is there for the questions the
// verifier cannot answer on its own.
func BuildCapsule(opts Options) (*verification.Builder, error) {
	at := opts.At
	if at.IsZero() {
		at = register.AssessedAt()
	}
	commit := opts.Commit
	if commit == "" {
		commit = "unknown -- the capsule was built outside a git checkout"
	}

	// The claim is INTERNALLY_ASSURED and not a word more. An assessor
	// who finds the capsule claims less than it can support will be
	// pleasantly surprised; the reverse costs the engagement.
	b, err := verification.NewBuilder("VERIQO", "INTERNALLY_ASSURED", at)
	if err != nil {
		return nil, err
	}

	if err := b.Add("README.txt", []byte(readme)); err != nil {
		return nil, err
	}
	if err := b.Add("VERIFY.txt", []byte(verification.README)); err != nil {
		return nil, err
	}

	contents := Contents{
		Subject: "VERIQO Evidence-Qualified Intelligence OS", BuiltAt: at.UTC(),
		Includes: []string{
			"the assurance register: every claim, its level, its evidence and its debts",
			"the twenty production gates and the controls each rests on",
			"the failure-class register with every cited test name",
			"the self-doubt register, including counterexamples already closed",
			"the policy rules and the authority matrix, as data",
			"the API surface with each endpoint's declared guarantees",
			"the redaction corpus taxonomy and both coverage figures with their status",
			"a threat model that states what was NOT considered",
			"the dependency manifest",
		},
		Excludes: []string{
			"customer data of any kind: none exists, because no customer data has ever " +
				"been processed",
			"production keys: there are none. The key root is a software test double",
			"an external anchor: VERIQO deliberately does not implement one",
			"any evidence produced by a party other than VERIQO: there is none",
			"real-world documents: every redaction fixture was built by VERIQO",
			"operational telemetry: nothing has run in production",
		},
		HowToStart: []string{
			"run veriqo-verify over this directory; it recomputes rather than reads",
			"read assurance/register.txt: it is the shortest honest summary of what is " +
				"and is not established",
			"read threat-model.json, and specifically its not_considered list",
			"pick any claim in assurance/claims.json and follow its disproof path yourself",
		},
	}
	if err := b.AddJSON("CONTENTS.json", contents); err != nil {
		return nil, err
	}

	if err := b.AddJSON("build.json", Build{
		Module: "veriqo", Commit: commit, GoVersion: runtime.Version(),
		Reproduce: []string{
			"git checkout " + commit,
			"go build ./...",
			"go test ./...",
			"./scripts/verify.sh",
			"go run ./cmd/veriqoctl all",
		},
		ReproducibilityVerified: false,
		Note: "VERIQO has NOT verified that a rebuild is byte-identical. The build is " +
			"reproducible in the ordinary sense -- the same source produces working " +
			"software -- and no bit-for-bit comparison has been performed. Gate G19 " +
			"covers this",
	}); err != nil {
		return nil, err
	}

	deps := append([]Dependency(nil), opts.Deps...)
	sort.Slice(deps, func(i, j int) bool { return deps[i].Path < deps[j].Path })
	if err := b.AddJSON("dependencies.json", map[string]any{
		"dependencies": deps,
		"note": "VERIQO's runtime depends only on the Go standard library. A dependency " +
			"set this small is a deliberate choice: every external package is a supply " +
			"chain nobody here audits. No vulnerability scan has been run (gate G8)",
	}); err != nil {
		return nil, err
	}

	// --- the assurance material -------------------------------------
	g, err := register.VeriqoGraph()
	if err != nil {
		return nil, err
	}
	if err := b.Add("assurance/register.txt", []byte(g.Report(at))); err != nil {
		return nil, err
	}
	if err := b.AddJSON("assurance/claims.json", g.Claims()); err != nil {
		return nil, err
	}
	if err := b.AddJSON("assurance/debts.json", g.Debts()); err != nil {
		return nil, err
	}
	if err := b.AddJSON("assurance/controls.json", g.Controls()); err != nil {
		return nil, err
	}
	if err := b.AddJSON("assurance/gates.json", g.Gates()); err != nil {
		return nil, err
	}

	// The evidence file the verifier reads to decide whether any
	// independent party has contributed. Today it is entirely
	// internal, and the verifier will derive INTERNALLY_ASSURED from
	// it -- which is the correct answer.
	var ev []state.Evidence
	for _, c := range g.Claims() {
		ev = append(ev, c.Evidence...)
	}
	if err := b.AddJSON("assurance/evidence.json", ev); err != nil {
		return nil, err
	}

	if err := b.Add("assurance/ladder.txt", []byte(ladderText())); err != nil {
		return nil, err
	}

	// --- the existing registers -------------------------------------
	gr, err := gates.VeriqoRegister()
	if err != nil {
		return nil, err
	}
	if err := b.Add("gates/report.txt", []byte(gr.Report())); err != nil {
		return nil, err
	}

	sc, err := scorecard.Veriqo()
	if err != nil {
		return nil, err
	}
	if err := b.Add("scorecard/report.txt", []byte(sc.Report())); err != nil {
		return nil, err
	}

	fr, err := failureclass.NewRegister(failureclass.Closed...)
	if err != nil {
		return nil, err
	}
	if err := b.Add("failure-classes/report.txt", []byte(fr.Report())); err != nil {
		return nil, err
	}
	if err := b.AddJSON("failure-classes/cited-tests.json", fr.CitedTests()); err != nil {
		return nil, err
	}

	sd, err := selfdoubt.NewRegister(selfdoubt.Claims...)
	if err != nil {
		return nil, err
	}
	if err := b.Add("self-doubt/report.txt", []byte(sd.Report())); err != nil {
		return nil, err
	}

	// --- policy and authority, as data ------------------------------
	//
	// A rule's condition is a Go closure and cannot be exported as
	// data an assessor could evaluate. What CAN be exported is the
	// rule set's shape and order, which is what decides the outcome
	// under deny-overrides -- and the limitation is stated in the file
	// rather than left for the assessor to discover.
	if err := b.AddJSON("policy/rules.json", policyRules()); err != nil {
		return nil, err
	}
	if err := b.AddJSON("policy/authority-matrix.json", authorityMatrix()); err != nil {
		return nil, err
	}

	// --- API contracts ----------------------------------------------
	//
	// The endpoints go in as DATA rather than as the router's rendered
	// description, so an assessor can check each declared guarantee
	// against the code themselves instead of reading a summary.
	if err := b.AddJSON("api/endpoints.json", api.Endpoints()); err != nil {
		return nil, err
	}

	// --- redaction corpus -------------------------------------------
	if err := b.AddJSON("redaction/variants.json", corpus.Variants); err != nil {
		return nil, err
	}
	outcomes, cov, err := corpus.Run()
	if err != nil {
		return nil, err
	}
	if err := b.Add("redaction/coverage.txt",
		[]byte(cov.Report(outcomes)+"\n"+corpus.LevelStatement()+"\n")); err != nil {
		return nil, err
	}
	if err := b.AddJSON("redaction/outcomes.json", outcomes); err != nil {
		return nil, err
	}

	// --- threat model -----------------------------------------------
	if err := b.AddJSON("threat-model.json", VeriqoThreatModel()); err != nil {
		return nil, err
	}

	// --- a case the verifier can actually check -----------------------
	if err := addWorkedCase(b); err != nil {
		return nil, err
	}

	return b, nil
}

// PolicyRule is a rule as far as it can be serialised.
type PolicyRule struct {
	Order int    `json:"order"`
	Name  string `json:"name"`
	// Condition is deliberately absent: see Note.
	Note string `json:"note,omitempty"`
}

func policyRules() map[string]any {
	rs := policy.Baseline()
	out := make([]PolicyRule, 0, len(rs))
	for i, r := range rs {
		out = append(out, PolicyRule{Order: i, Name: r.Name})
	}
	return map[string]any{
		"evaluation": "deny-overrides. A core layer is evaluated first and no configurable " +
			"rule can permit what it denies; the default is DENY, so a request matching " +
			"no rule is refused",
		"rules": out,
		"limitation": "each rule's condition is a Go closure and cannot be serialised. " +
			"What is exported is the rule set's identity and order, which is what decides " +
			"the outcome under deny-overrides. To assess the conditions themselves, read " +
			"pkg/policy/policy.go -- Baseline() and core() -- which is why the source is " +
			"in the capsule",
		"core_layer_note": "core() is not in this list. It is evaluated before every " +
			"configurable rule and is not configurable at all, which is the property that " +
			"makes the rest of the rule set safe to change",
	}
}

func authorityMatrix() map[string][]authority.Capability {
	out := map[string][]authority.Capability{}
	for _, r := range authority.Roles() {
		out[string(r)] = authority.Of(r)
	}
	return out
}

func ladderText() string {
	var b strings.Builder
	b.WriteString("THE ASSURANCE LADDER\n\n")
	b.WriteString("No rung may be skipped. Everything above INTERNALLY_ASSURED requires\n")
	b.WriteString("evidence from a party that is not the implementer -- Law 11.\n\n")
	for _, s := range state.States() {
		who := "requires " + string(s.RequiredEvidence())
		if s.SelfReachable() {
			who += " (VERIQO can reach this alone)"
		} else {
			who += " (VERIQO CANNOT reach this alone)"
		}
		fmt.Fprintf(&b, "  %-24s %s\n", s, who)
	}
	b.WriteString("\nVERIQO's own position: nothing is above INTERNALLY_ASSURED.\n")
	return b.String()
}

// VeriqoThreatModel is what was considered, and what was not.
func VeriqoThreatModel() ThreatModel {
	return ThreatModel{
		Considered: []Threat{
			{ID: "T-01", Actor: "a tenant", Goal: "read another tenant's evidence",
				Control: "per-surface key derivation from a tenant anchor, with a fail-closed guard",
				Tested:  "by VERIQO, in-process (test/adversarial/tenancy_test.go)",
				Residual: "no deployed datastore, cache or index exists, so isolation is " +
					"proven only at the derivation layer"},
			{ID: "T-02", Actor: "an author of evidence",
				Goal:    "have a model instruction inside a document widen an agent's reach",
				Control: "grants sealed at launch; every scoped argument constrained or the launch fails",
				Tested:  "by VERIQO (test/adversarial/injection_test.go, ten tests)",
				Residual: "the attacker was the party that wrote the defence; no model was " +
					"in the loop"},
			{ID: "T-03", Actor: "an insider with write access to storage",
				Goal:    "alter or remove a recorded decision",
				Control: "hash chain covering height and predecessor; damage that is not a torn tail refuses to open",
				Tested:  "by VERIQO; this control YIELDED a real defect and was fixed",
				Residual: "with no external anchor, a wholesale rewrite between two " +
					"observations is undetectable from the artefact alone"},
			{ID: "T-04", Actor: "a party producing evidence",
				Goal:    "have one source counted as two so a claim looks corroborated",
				Control: "six-dimension independence assessment; UNKNOWN never counts",
				Tested:  "by VERIQO (test/adversarial/laundering_test.go)",
				Residual: "the dimension values are supplied by whoever configures the " +
					"source; nothing here verifies them against the world"},
			{ID: "T-05", Actor: "an operator under commercial pressure",
				Goal:    "mark a control assured without the evidence",
				Control: "Law 11: promotions above INTERNALLY_ASSURED are unrepresentable without independent evidence",
				Tested:  "by VERIQO (pkg/assurance/state)",
				Residual: "an operator with commit access can change the code. The control " +
					"raises the cost and makes the change visible in a diff; it does not " +
					"make it impossible"},
			{ID: "T-06", Actor: "a recipient of a redacted document",
				Goal:    "recover the redacted content",
				Control: "terms replaced in the decompressed view; undecodable structures refused",
				Tested: "by VERIQO, against VERIQO's own fixtures. This control YIELDED a " +
					"real defect (silent truncation) and was fixed",
				Residual: "no recovery has ever been attempted by anyone. Absence in twelve " +
					"encodings is not irrecoverability"},
			{ID: "T-07", Actor: "a supplier of a dependency or build input",
				Goal:    "have malicious code reach a VERIQO artefact",
				Control: "the runtime depends only on the Go standard library",
				Tested:  "not tested; asserted from the module graph",
				Residual: "no SBOM, no artefact signing, no vulnerability scanning. The " +
					"toolchain itself is trusted implicitly"},
		},
		NotConsidered: []string{
			"a compromise of the Go toolchain or its distribution",
			"physical access to a host, or a malicious hypervisor",
			"side channels: timing, cache, power, and any microarchitectural attack",
			"denial of service beyond in-process backpressure; no network layer exists",
			"a compromised operator with both commit access and release authority acting " +
				"deliberately over time",
			"legal compulsion to produce or withhold evidence",
			"post-quantum adversaries: every signature here is Ed25519",
			"anything in a deployment topology, because there is no deployment",
		},
		Assumptions: []string{
			"the Go standard library's crypto is correct",
			"the operating system's file system honours fsync",
			"the reader of a report reads its caveats. Every control in this system is " +
				"designed to make the caveats hard to skip, and none of them can force it",
		},
	}
}

const readme = `VERIQO AUDITOR CAPSULE

This directory exists so that you do not have to take VERIQO's word for
anything.

Start here:

  1. veriqo-verify .            recomputes every digest, rehashes the
                                ledger from genesis, recomputes the
                                passport digest before checking its
                                signature, and DERIVES the qualification
                                state rather than reading it

  2. assurance/register.txt     the shortest honest account of what is
                                established and what is not

  3. threat-model.json          read the not_considered list first

  4. CONTENTS.json              what this capsule contains, and -- as
                                prominently -- what it does not

What you will find is that VERIQO claims exactly one thing about its own
assurance: INTERNALLY_ASSURED. That is the highest rung reachable
without a party like you, and every rung above it is defined in terms of
work only you can do.

The failure modes VERIQO would most like you to look for are recorded in
assurance/claims.json, each with a disproof path. Two of them have
already produced real defects, found internally and fixed; those are
recorded too, because a disproof path that has produced something is one
we know is capable of producing something.

Nothing here has been examined by anybody outside VERIQO. That is the
whole reason you were sent this.
`

// Marshal is a small helper for callers that want the capsule's
// contents as JSON without writing to disk.
func Marshal(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

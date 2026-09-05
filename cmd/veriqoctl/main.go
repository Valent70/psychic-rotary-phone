// Command veriqoctl reports what VERIQO is, honestly.
//
// It is deliberately a REPORTING tool rather than an operational one.
// Every subcommand prints something a reader can disagree with: the
// production gates and why each is blocked, the scorecard and why
// nothing is GREEN, the corpus coverage with its ESTIMATE label
// attached, the self-doubt register's closing line about who attacked
// the claims.
//
// There is no subcommand that resolves a case, approves a finding or
// merges two entities. Those are authority acts with separation-of-
// duties requirements, and a CLI that offered them would let one
// person perform a two-party act.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"veriqo/pkg/api"
	"veriqo/pkg/assurance/capsule"
	"veriqo/pkg/assurance/failureclass"
	"veriqo/pkg/assurance/honesty"
	"veriqo/pkg/assurance/metrics"
	"veriqo/pkg/assurance/register"
	"veriqo/pkg/assurance/selfdoubt"
	"veriqo/pkg/contract"
	"veriqo/pkg/domain"
	"veriqo/pkg/epistemic"
	"veriqo/pkg/epistemic/ladder"
	"veriqo/pkg/evidence/redaction/corpus"
	"veriqo/pkg/gates"
	"veriqo/pkg/ontology"
	"veriqo/pkg/policy"
	"veriqo/pkg/readiness"
	"veriqo/pkg/resilience"
	"veriqo/pkg/scorecard"
)

const usage = `veriqoctl -- report what VERIQO is

  firewall     the four epistemic inequalities and VERIQO's own states
  ladder       what was seen, separated from what it was taken to mean
  metrics      three registers that are deliberately never combined
  honesty      what each overclaim check can and cannot catch (H1-H5)
  assurance    the master assurance graph: gate -> control -> claim ->
               evidence -> validator -> level -> release decision
  readiness    nine dimensions, each status naming who is blocking it
  procurement  the same blockers as a schedule: who sells it, what they
               must hand back, what must be true first, and the critical path
  debt         the evidence VERIQO does not have, and what it blocks
  gates        the twenty permanent production gates and their state
  scorecard    the nine-dimension enterprise qualification scorecard
  corpus       Article 18 structural and weighted coverage
  ontology     the operational ontology and its cross-domain edges
  templates    the domain claim templates and each domain's refusals
  failures     the failure-class register
  claims       the self-doubt register
  api          the API surface and the guarantees each endpoint declares
  all          every report, in order

  capsule DIR  write the auditor capsule to DIR, for somebody who should
               not have to take VERIQO's word for any of this. Check it
               with: veriqo-verify DIR

Every figure carries what it rests on. Nothing here is a summary that
cannot be disagreed with.

This tool reports what VERIQO believes about itself. It is not a
verifier: for that, use veriqo-verify, which recomputes rather than
reads and does not trust the system it is checking.
`

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	cmd := "all"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	if cmd == "capsule" {
		if err := writeCapsule(flag.Args()); err != nil {
			fmt.Fprintf(os.Stderr, "veriqoctl: capsule: %v\n", err)
			os.Exit(1)
		}
		return
	}

	reports := map[string]func() (string, error){
		"assurance":   assuranceReport,
		"metrics":     metricsReport,
		"firewall":    firewallReport,
		"ladder":      ladderReport,
		"honesty":     honestyReport,
		"readiness":   readinessReport,
		"procurement": procurementReport,
		"debt":        debtReport,
		"gates":       gatesReport,
		"scorecard":   scorecardReport,
		"corpus":      corpusReport,
		"ontology":    ontologyReport,
		"templates":   templatesReport,
		"failures":    failuresReport,
		"claims":      claimsReport,
		"api":         apiReport,
	}
	order := []string{"readiness", "procurement", "firewall", "ladder", "metrics", "honesty", "assurance", "debt", "scorecard", "gates", "corpus",
		"ontology", "templates", "failures", "claims", "api"}

	var run []string
	if cmd == "all" {
		run = order
	} else if _, ok := reports[cmd]; ok {
		run = []string{cmd}
	} else {
		fmt.Fprintf(os.Stderr, "veriqoctl: unknown report %q\n\n", cmd)
		flag.Usage()
		os.Exit(2)
	}

	failed := false
	for i, name := range run {
		if i > 0 {
			fmt.Println()
			fmt.Println(strings.Repeat("=", 78))
			fmt.Println()
		}
		out, err := reports[name]()
		if err != nil {
			fmt.Fprintf(os.Stderr, "veriqoctl: %s: %v\n", name, err)
			failed = true
			continue
		}
		fmt.Print(out)
	}
	if failed {
		os.Exit(1)
	}
}

func assuranceReport() (string, error) {
	g, err := register.VeriqoGraph()
	if err != nil {
		return "", err
	}
	return g.Report(register.AssessedAt()), nil
}

func metricsReport() (string, error) { return metrics.VeriqoPanel() }

// ladderReport demonstrates the rungs on the audit's own three
// sentences, because the distinction is easier to see than to state.
func ladderReport() (string, error) {
	at := register.AssessedAt()
	c, err := ladder.NewChain(
		ladder.Statement{ID: "st:obs-1", Kind: ladder.Observation,
			Text:     "the vessel reported no position for six hours while at 1.00N 103.80E",
			Recorder: "ais-network-a", EvidenceRefs: []string{"evidenceversion:ais-1"},
			State: epistemic.Present, At: at},
		ladder.Statement{ID: "st:obs-2", Kind: ladder.Observation,
			Text:     "reported draught rose from 7.1 m to 13.4 m across that window",
			Recorder: "ais-network-a", EvidenceRefs: []string{"evidenceversion:ais-2"},
			State: epistemic.Present, At: at},
		ladder.Statement{ID: "st:inf-1", Kind: ladder.Inference,
			Text:    "the two reports are 3.1 NM apart",
			RestsOn: []contract.ID{"st:obs-1", "st:obs-2"},
			Method:  "haversine over the reported positions", At: at},
		ladder.Statement{ID: "st:hyp-1", Kind: ladder.Hypothesis,
			Text:    "the vessel loaded cargo during the reporting gap",
			RestsOn: []contract.ID{"st:obs-1", "st:obs-2"},
			Alternatives: []string{
				"ballast was taken on and the earlier draught was stale",
				"both draught values are data-entry artefacts",
			},
			Discriminator: "the terminal's berth and crane records for the window",
			State:         epistemic.Present, At: at},
		ladder.Statement{ID: "st:asr-1", Kind: ladder.Assertion,
			Text:         "on the evidence available, cargo was loaded during the gap",
			RestsOn:      []contract.ID{"st:hyp-1"},
			StandsBehind: "human:analyst-1", At: at},
	)
	if err != nil {
		return "", err
	}
	return c.Report(), nil
}

func procurementReport() (string, error) {
	p, err := readiness.VeriqoPlan()
	if err != nil {
		return "", err
	}
	return p.Report(), nil
}

// firewallReport shows the four inequalities against VERIQO's own
// evidence position, so the principle is demonstrated rather than
// merely stated.
func firewallReport() (string, error) {
	s := epistemic.Set{Observations: []epistemic.Observation{
		{Subject: "the code and its tests", State: epistemic.Verified,
			Value: "built, tested and attacked by the implementer"},
		{Subject: "the assurance register's evidence", State: epistemic.Present,
			Value: "internal evidence only"},
		{Subject: "an independent security assessment", State: epistemic.Absent},
		{Subject: "a real-world document corpus", State: epistemic.Absent},
		{Subject: "operational history", State: epistemic.Absent},
		{Subject: "a legal opinion on restricted source classes", State: epistemic.Unexamined,
			Why: "no counsel has been engaged in any jurisdiction (ED-010)"},
		{Subject: "cross-implementation canonicaliser conformance", State: epistemic.Unexamined,
			Why: "no independent RFC 8785 implementation has been fed the same inputs (ED-011)"},
		{Subject: "recoverability of redacted derivatives", State: epistemic.Unexamined,
			Why: "no recovery has ever been attempted, by anyone (ED-004)"},
	}}
	return s.Report(), nil
}

func honestyReport() (string, error) {
	s, err := honesty.Veriqo()
	if err != nil {
		return "", err
	}
	return s.Report(), nil
}

func readinessReport() (string, error) {
	p, err := readiness.Veriqo()
	if err != nil {
		return "", err
	}
	return p.Report(), nil
}

func debtReport() (string, error) {
	g, err := register.VeriqoGraph()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("EVIDENCE DEBT\n")
	b.WriteString("  what is NOT established, why, who owns it, and what it blocks.\n")
	b.WriteString("  A gap stated this way can be priced. 'OPEN' cannot.\n\n")
	open, external := 0, 0
	for _, d := range g.Debts() {
		b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(d.Describe(), "\n"),
			"\n", "\n  ") + "\n")
		if d.Open() {
			open++
			if !d.SelfPayable() {
				external++
			}
		}
	}
	fmt.Fprintf(&b, "%d open debt(s); %d require a party that is not VERIQO.\n", open, external)
	return b.String(), nil
}

// writeCapsule builds the auditor capsule.
//
// It is not a report, so it does not go through the reports map: it
// writes files, and a tool that printed to stdout and wrote to disk
// under the same verb would eventually do one when the caller meant
// the other.
func writeCapsule(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: veriqoctl capsule DIR")
	}
	dir := args[1]
	b, err := capsule.BuildCapsule(capsule.Options{Commit: commit()})
	if err != nil {
		return err
	}
	m, err := b.Write(dir)
	if err != nil {
		return err
	}
	fmt.Printf("wrote the auditor capsule to %s\n", dir)
	fmt.Printf("  %d file(s), claiming exactly %s\n", len(m.Files), m.ClaimedQualification)
	fmt.Printf("\nCheck it without trusting this tool:\n\n    veriqo-verify %s\n\n", dir)
	fmt.Print("The verifier recomputes every digest, rehashes the ledger from genesis,\n" +
		"and DERIVES the qualification state rather than reading it. Where its\n" +
		"answer differs from the claim above, believe the verifier.\n")
	return nil
}

// commit reports the build's commit, when the environment supplies one.
// It is read from the environment rather than by shelling out to git,
// so that a capsule built in a container without a checkout says
// "unknown" instead of failing.
func commit() string { return os.Getenv("VERIQO_COMMIT") }

func gatesReport() (string, error) {
	r, err := gates.VeriqoRegister()
	if err != nil {
		return "", err
	}
	return r.Report(), nil
}

func scorecardReport() (string, error) {
	s, err := scorecard.Veriqo()
	if err != nil {
		return "", err
	}
	return s.Report(), nil
}

func corpusReport() (string, error) {
	outcomes, cov, err := corpus.Run()
	if err != nil {
		return "", err
	}
	return cov.Report(outcomes) + "\n" + corpus.LevelStatement() + "\n", nil
}

func ontologyReport() (string, error) {
	o, err := ontology.Veriqo(1)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "OPERATIONAL ONTOLOGY %s\n", o.Version)
	fmt.Fprintf(&b, "  %d object types, %d relationship types, %d domain views\n",
		len(o.ObjectTypes()), len(o.RelationshipTypes()), len(o.Domains()))
	cross := o.CrossDomainRelationships()
	fmt.Fprintf(&b, "  %d relationships cross a domain boundary. These are what make VERIQO\n"+
		"  one graph rather than five products sharing a repository:\n", len(cross))
	for _, r := range cross {
		fmt.Fprintf(&b, "    %-24s %s -> %s\n", r.Name, r.From, r.To)
	}
	b.WriteString("  views:\n")
	for _, d := range o.Domains() {
		objs, rels := o.View(d)
		fmt.Fprintf(&b, "    %-14s %2d object types, %2d relationships\n", d, len(objs), len(rels))
	}
	return b.String(), nil
}

func templatesReport() (string, error) {
	r, err := domain.Veriqo()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("DOMAIN TEMPLATES\n")
	for _, d := range r.Domains() {
		fmt.Fprintf(&b, "  %s\n", d)
		for _, t := range r.ForDomain(d) {
			fmt.Fprintf(&b, "    %-28s %d necessary condition(s), %d competing hypothesis(es)\n",
				t.ID, len(t.Conditions), len(t.Hypotheses))
			fmt.Fprintf(&b, "      %s\n", t.Question)
		}
	}
	b.WriteString("  statements each domain may NOT make:\n")
	for _, d := range ontology.Domains() {
		rs := r.Refusals(d)
		if len(rs) == 0 {
			continue
		}
		sort.Strings(rs)
		fmt.Fprintf(&b, "    %-14s %s\n", d, strings.Join(rs, "; "))
	}
	return b.String(), nil
}

func failuresReport() (string, error) {
	reg, err := failureclass.NewRegister(failureclass.Closed...)
	if err != nil {
		return "", err
	}
	return reg.Report(), nil
}

func claimsReport() (string, error) {
	reg, err := selfdoubt.NewRegister(selfdoubt.Claims...)
	if err != nil {
		return "", err
	}
	return reg.Report(), nil
}

func apiReport() (string, error) {
	engine, err := policy.New(contract.Version{Component: "baseline", Revision: 1},
		policy.Baseline()...)
	if err != nil {
		return "", err
	}
	clock := contract.FixedClock(zeroInstant())
	idem, err := resilience.NewIdempotency(clock, 60)
	if err != nil {
		return "", err
	}
	r, err := api.NewRouter(engine, clock, idem)
	if err != nil {
		return "", err
	}
	for _, e := range api.Endpoints() {
		if err := r.Register(e, func(api.Request) (any, contract.Outcome, error) {
			return nil, contract.Succeeded, nil
		}); err != nil {
			return "", err
		}
	}
	return r.Describe(), nil
}

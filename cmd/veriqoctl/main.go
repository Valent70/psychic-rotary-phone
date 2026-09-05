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
	"veriqo/pkg/assurance/failureclass"
	"veriqo/pkg/assurance/selfdoubt"
	"veriqo/pkg/contract"
	"veriqo/pkg/domain"
	"veriqo/pkg/evidence/redaction/corpus"
	"veriqo/pkg/gates"
	"veriqo/pkg/ontology"
	"veriqo/pkg/policy"
	"veriqo/pkg/resilience"
	"veriqo/pkg/scorecard"
)

const usage = `veriqoctl -- report what VERIQO is

  gates        the twenty permanent production gates and their state
  scorecard    the nine-dimension enterprise qualification scorecard
  corpus       Article 18 structural and weighted coverage
  ontology     the operational ontology and its cross-domain edges
  templates    the domain claim templates and each domain's refusals
  failures     the failure-class register
  claims       the self-doubt register
  api          the API surface and the guarantees each endpoint declares
  all          every report, in order

Every figure carries what it rests on. Nothing here is a summary that
cannot be disagreed with.
`

func main() {
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	cmd := "all"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	reports := map[string]func() (string, error){
		"gates":     gatesReport,
		"scorecard": scorecardReport,
		"corpus":    corpusReport,
		"ontology":  ontologyReport,
		"templates": templatesReport,
		"failures":  failuresReport,
		"claims":    claimsReport,
		"api":       apiReport,
	}
	order := []string{"scorecard", "gates", "corpus", "ontology", "templates",
		"failures", "claims", "api"}

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

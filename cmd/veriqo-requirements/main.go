// Command veriqo-requirements closes P0-04 of the V7.12.0 integrated
// audit: "the repository and the v7.12.0 report must have exactly one
// authoritative requirement state."
//
// Before this command, docs/governance/REQUIREMENT_TRACEABILITY_MATRIX.md
// was hand-maintained prose over a hand-maintained table: the per-row
// status table correctly marked two requirements OPEN (R-029, R-050),
// while its own "Honest summary" paragraph, typed separately, said only
// one was OPEN and miscounted VERIFIED as 45 instead of 44 and BLOCKED
// as 5 instead of 4. That drift is exactly the failure mode the audit
// named: a report can say CLOSED while the artifact underneath says
// something else, and nobody notices because nothing recomputes the
// summary from the rows.
//
// docs/governance/requirements.json is now the single authoritative
// source: every other governance document about requirement status is
// generated from it, never edited by hand. Running this command with
// -check (as the traceability_matrix readiness gate does) fails loudly
// if the checked-in matrix has drifted from what the JSON would
// produce — so a hand-edit to either file is caught, not silently
// tolerated.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Requirement is one row of the traceability matrix. Status is
// restricted to the same vocabulary docs/governance/RELEASE_GATES.md
// uses elsewhere in the repo: VERIFIED, IMPLEMENTED, OPEN, BLOCKED.
type Requirement struct {
	ID           string `json:"id"`
	Phase        string `json:"phase"`
	Title        string `json:"title"`
	OwnerPackage string `json:"owner_package"`
	Tests        string `json:"tests"`
	Status       string `json:"status"`
	Note         string `json:"note"`
}

func main() {
	src := flag.String("source", "docs/governance/requirements.json", "authoritative requirements JSON")
	out := flag.String("out", "docs/governance/REQUIREMENT_TRACEABILITY_MATRIX.md", "generated matrix path")
	check := flag.Bool("check", false, "fail if the generated matrix differs from the checked-in file, instead of writing it")
	flag.Parse()

	raw, err := os.ReadFile(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-requirements: read source:", err)
		os.Exit(2)
	}
	var reqs []Requirement
	if err := json.Unmarshal(raw, &reqs); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-requirements: parse source:", err)
		os.Exit(2)
	}
	if err := validate(reqs); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-requirements:", err)
		os.Exit(2)
	}

	rendered := render(reqs)

	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "veriqo-requirements: read existing matrix:", err)
			os.Exit(2)
		}
		if !bytes.Equal(existing, []byte(rendered)) {
			fmt.Fprintln(os.Stderr, "veriqo-requirements: "+*out+" is out of sync with "+*src+
				" — run: go run ./cmd/veriqo-requirements")
			os.Exit(1)
		}
		fmt.Println("veriqo-requirements: matrix matches requirements.json")
		return
	}

	if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-requirements: write:", err)
		os.Exit(2)
	}
	fmt.Println("veriqo-requirements: wrote", *out)
}

var validStatus = map[string]bool{"VERIFIED": true, "IMPLEMENTED": true, "OPEN": true, "BLOCKED": true}

func validate(reqs []Requirement) error {
	seen := map[string]bool{}
	for _, r := range reqs {
		if seen[r.ID] {
			return fmt.Errorf("duplicate requirement id %s", r.ID)
		}
		seen[r.ID] = true
		if !validStatus[r.Status] {
			return fmt.Errorf("%s: unknown status %q (must be VERIFIED, IMPLEMENTED, OPEN or BLOCKED)", r.ID, r.Status)
		}
		if r.Status == "BLOCKED" && r.Note == "" {
			return fmt.Errorf("%s: BLOCKED requires a note naming the blocker — a vague BLOCKED is not allowed", r.ID)
		}
	}
	return nil
}

func statusCell(r Requirement) string {
	switch r.Status {
	case "OPEN", "BLOCKED":
		if r.Note != "" {
			return "**" + r.Status + "** (" + r.Note + ")"
		}
		return "**" + r.Status + "**"
	default:
		if r.Note != "" {
			return r.Status + " (" + r.Note + ")"
		}
		return r.Status
	}
}

func render(reqs []Requirement) string {
	var b strings.Builder
	b.WriteString("# Requirement Traceability Matrix — v7.12.0\n\n")
	b.WriteString("Status vocabulary per `docs/governance/RELEASE_GATES.md`. Every row\n")
	b.WriteString("either names the package that implements it and the test that proves\n")
	b.WriteString("it, or is honestly marked OPEN/BLOCKED. There is no row whose status\n")
	b.WriteString("rests on prose.\n\n")
	b.WriteString("This file is generated from `docs/governance/requirements.json` by\n")
	b.WriteString("`cmd/veriqo-requirements` (P0-04: one authoritative requirement state).\n")
	b.WriteString("Do not hand-edit the table or the summary below — edit the JSON and\n")
	b.WriteString("regenerate with `go run ./cmd/veriqo-requirements`; the `traceability_matrix`\n")
	b.WriteString("readiness gate fails the build if the two drift apart.\n\n")

	b.WriteString("| ID | Audit phase | Requirement | Owner package | Test | Status |\n")
	b.WriteString("|----|-------------|-------------|---------------|------|--------|\n")
	for _, r := range reqs {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
			r.ID, r.Phase, r.Title, r.OwnerPackage, r.Tests, statusCell(r)))
	}

	var verified, implemented int
	var open, blocked []string
	var caveated []string
	for _, r := range reqs {
		switch r.Status {
		case "VERIFIED":
			verified++
			if r.Note != "" {
				caveated = append(caveated, r.ID+" ("+r.Note+")")
			}
		case "IMPLEMENTED":
			implemented++
		case "OPEN":
			open = append(open, r.ID)
		case "BLOCKED":
			blocked = append(blocked, r.ID+" ("+r.Note+")")
		}
	}
	sort.Strings(open)
	sort.Strings(blocked)

	b.WriteString("\n## Honest summary (computed, not hand-typed)\n\n")
	b.WriteString(fmt.Sprintf("%d of %d tracked requirements are VERIFIED with named tests. "+
		"%d is IMPLEMENTED but not executed in this environment. %d are OPEN: %s. "+
		"%d are BLOCKED on infrastructure or third parties: %s.\n\n",
		verified, len(reqs), implemented, len(open), joinOr(open, "none"),
		len(blocked), joinOr(blocked, "none")))

	if len(caveated) > 0 {
		b.WriteString(fmt.Sprintf("%d row(s) are marked VERIFIED **with an inline caveat** rather than "+
			"silently upgraded, because part of what the phase asked for cannot be produced here: %s. "+
			"Each caveat names exactly what is missing; none of them is counted as closed.\n\n",
			len(caveated), strings.Join(caveated, "; ")))
	}

	total := verified + implemented + len(open) + len(blocked)
	consistency := "consistent"
	if total != len(reqs) {
		consistency = "INCONSISTENT — counts do not sum to the row total; this is a generator bug, not a documentation typo"
	}
	b.WriteString(fmt.Sprintf("Row count check: %d VERIFIED + %d IMPLEMENTED + %d OPEN + %d BLOCKED = %d, "+
		"of %d total rows (%s).\n", verified, implemented, len(open), len(blocked), total, len(reqs), consistency))

	return b.String()
}

func joinOr(xs []string, empty string) string {
	if len(xs) == 0 {
		return empty
	}
	return strings.Join(xs, ", ")
}

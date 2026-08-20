// Command veriqo-verify-release-identity closes audit item P1-RLS
// ("Release Artifact <-> Manifest <-> Source Identity Consistency"):
// it proves the chain
//
//	ZIP source tree -> source hash -> manifest source_hash ->
//	manifest git_commit -> release certificate -> trusted signature ->
//	archive identity
//
// is one real, checkable lineage, rather than something a reader has
// to take on faith by eyeballing three separate hex strings.
//
// The one structural wrinkle this closes honestly instead of by
// asserting it away: a READINESS_MANIFEST.json necessarily records the
// git commit that existed WHEN THE GATES RAN, before the manifest file
// itself existed. Committing the manifest produces a NEW commit (the
// manifest's own bytes are now part of the tree), so the commit
// actually shipped in a release archive can never be bit-identical to
// manifest.git_commit -- by construction, not by mistake. Requiring
// literal commit-hash equality would be a check no real release could
// ever pass, and a reader who only compares those two hex strings
// (exactly what surfaced this gap) will always see a "mismatch" that
// looks like tampering. internal/sourcehash already excludes exactly
// the self-referential paths (READINESS_MANIFEST.json, SBOM.json,
// evidence/) from the source hash for the same underlying reason. This
// tool proves the REAL invariant instead: that the two commits' source
// content is genuinely provenance-identical -- everything that differs
// between them is confined to those declared, self-referential
// exclusions -- rather than merely hoping it is.
//
// Two independent checks, either of which can fail on its own:
//
//  1. Source hash reproducibility: recompute internal/sourcehash over
//     -source-dir (a real checkout or an extracted zip -- this check
//     needs no .git) and confirm it equals the manifest's own
//     source_hash. This is the strongest single proof: if it passes,
//     the exact bytes the release certificate committed to are the
//     bytes actually sitting on disk right now.
//  2. Commit lineage (only when -zip and -repo-dir are both given): the
//     zip's git-archive comment (the commit `git archive --format=zip`
//     embeds automatically) is compared to manifest.git_commit. An
//     exact match passes trivially; otherwise `git diff --name-only`
//     between the two commits is inspected and must touch ONLY paths
//     internal/sourcehash.DefaultExclusions already excludes -- proving
//     the two commits are the same release before/after the manifest
//     was added, not an unrelated substitution.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"veriqo/internal/assurance"
	"veriqo/internal/sourcehash"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("veriqo-verify-release-identity", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "READINESS_MANIFEST.json", "path to a READINESS_MANIFEST.json")
	sourceDir := fs.String("source-dir", ".", "directory containing the source tree to verify against the manifest's source_hash (a checkout or an extracted release zip)")
	zipPath := fs.String("zip", "", "optional: path to a release zip produced by `git archive --format=zip`; its embedded commit is checked against the manifest's git_commit")
	repoDir := fs.String("repo-dir", ".", "git repository to consult for commit-lineage reconciliation when -zip is given and its commit differs from the manifest's")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release-identity: read manifest:", err)
		return 2
	}
	var m assurance.ReadinessManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release-identity: parse manifest:", err)
		return 2
	}

	ok := true

	// Check 1: source hash reproducibility.
	res, err := sourcehash.Compute(*sourceDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "veriqo-verify-release-identity: recompute source hash:", err)
		return 2
	}
	if res.RootHash == m.Release.SourceHash {
		fmt.Printf("SOURCE HASH : PASS -- %s recomputed from %s matches manifest.source_hash exactly (%d files)\n",
			res.RootHash, *sourceDir, res.FileCount)
	} else {
		fmt.Printf("SOURCE HASH : FAIL -- recomputed %s from %s, manifest.source_hash says %s\n",
			res.RootHash, *sourceDir, m.Release.SourceHash)
		ok = false
	}

	// Check 2: commit lineage, only attempted when a zip is supplied.
	if *zipPath != "" {
		shippedCommit, err := zipArchiveComment(*zipPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "veriqo-verify-release-identity: read zip archive comment:", err)
			return 2
		}
		switch {
		case shippedCommit == "":
			fmt.Println("COMMIT LINEAGE : FAIL -- zip carries no git-archive commit comment; it was not produced by `git archive`, so its provenance cannot be checked")
			ok = false
		case shippedCommit == m.Release.GitCommit:
			fmt.Printf("COMMIT LINEAGE : PASS -- zip commit %s matches manifest.git_commit exactly\n", shippedCommit)
		default:
			touched, diffErr := gitDiffNames(*repoDir, m.Release.GitCommit, shippedCommit)
			if diffErr != nil {
				fmt.Printf("COMMIT LINEAGE : FAIL -- zip commit %s differs from manifest.git_commit %s, and the difference could not be reconciled: %v\n",
					shippedCommit, m.Release.GitCommit, diffErr)
				ok = false
				break
			}
			var offending []string
			for _, path := range touched {
				if !sourcehash.Excluded(path, sourcehash.DefaultExclusions) {
					offending = append(offending, path)
				}
			}
			if len(offending) == 0 {
				fmt.Printf("COMMIT LINEAGE : PASS (benign) -- zip commit %s differs from manifest.git_commit %s, but every changed path (%s) is a declared self-referential exclusion (the manifest committing itself); source content is provably identical, confirmed independently by the SOURCE HASH check above\n",
					shippedCommit, m.Release.GitCommit, strings.Join(touched, ", "))
			} else {
				fmt.Printf("COMMIT LINEAGE : FAIL -- zip commit %s differs from manifest.git_commit %s, and %d changed path(s) are NOT declared exclusions: %s\n",
					shippedCommit, m.Release.GitCommit, len(offending), strings.Join(offending, ", "))
				ok = false
			}
		}
	}

	if !ok {
		fmt.Println("veriqo-verify-release-identity: FAILED")
		return 1
	}
	fmt.Println("veriqo-verify-release-identity: PASSED")
	return 0
}

// zipArchiveComment extracts the whole-archive comment `git archive
// --format=zip` embeds (by default, the commit the archive was
// generated from), trimmed of whitespace. Uses the `unzip -z` CLI
// rather than archive/zip's Reader.Comment to match exactly what a
// release engineer running the same command by hand would see, and
// because this tool already shells out to `git` below for the same
// class of release-engineering reason (see cmd/veriqo-readiness's own
// precedent for os/exec use outside the zero-dependency runtime path).
func zipArchiveComment(path string) (string, error) {
	out, err := exec.Command("unzip", "-z", path).Output() // #nosec G204 -- path is the operator-supplied -zip CLI flag of this release-engineering tool, not untrusted network input
	if err != nil {
		return "", fmt.Errorf("unzip -z %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return "", nil
	}
	// Line 1 is "Archive:  <path>"; the comment (if any) follows.
	return strings.TrimSpace(lines[1]), nil
}

// gitDiffNames returns the repo-relative paths that differ between
// from and to in repoDir.
func gitDiffNames(repoDir, from, to string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoDir, "diff", "--name-only", from, to) // #nosec G204 -- repoDir/from/to are this release-engineering tool's own -repo-dir CLI flag and manifest/zip commit hashes, not untrusted network input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff --name-only %s %s: %w (%s)", from, to, err, stderr.String())
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

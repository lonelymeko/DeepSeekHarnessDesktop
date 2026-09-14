package main

import (
	"strings"
	"testing"
)

func TestBreakingReportNamesEveryFileAndTheGates(t *testing.T) {
	report := breakingReport("old-commit", "new-commit", []string{"package.json", "apps/cli/src/args.ts"})
	// The report is the only thing a maintainer sees from a failed scheduled run,
	// so it has to name both commits, every changed contract file, and the
	// commands that close the loop.
	for _, expected := range []string{
		"old-commit",
		"new-commit",
		"package.json",
		"apps/cli/src/args.ts",
		"make smoke",
		"make sync-accept",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("breaking report lacks %q:\n%s", expected, report)
		}
	}
	if strings.Contains(report, "go run ./cmd/upstream-sync") {
		t.Error("the report still points at the raw command instead of the make target")
	}
}

func TestExitCodesKeepTheWorkflowContract(t *testing.T) {
	// .github/workflows/upstream-sync.yml reads 2 as the adaptation verdict and
	// any other non-zero as a crash. Go's default panic status is also 2, so the
	// crash path must not use it, or a broken check is misreported as the verdict.
	if exitAdapt != 2 {
		t.Fatalf("verdict exit code is %d, but the workflow expects 2", exitAdapt)
	}
	if exitFailed == exitAdapt || exitFailed == exitOK {
		t.Fatalf("crash exit code %d collides with another exit code", exitFailed)
	}
}

func TestManifestsEquivalentIgnoresUpdateTimestamp(t *testing.T) {
	left := Manifest{
		Repository:   "https://example.test/upstream.git",
		Ref:          "master",
		Commit:       "abc123",
		CLIVersion:   "1.2.3",
		NodeVersion:  "26.7.0",
		NodeEngines:  ">=24",
		UpdatedAt:    "2026-09-01T00:00:00Z",
		Fingerprints: map[string]string{"package.json": "digest"},
	}
	right := left
	right.UpdatedAt = "2026-09-04T00:00:00Z"
	if !manifestsEquivalent(left, right) {
		t.Fatal("timestamps must not make an unchanged upstream manifest differ")
	}
}

func TestManifestsEquivalentDetectsFingerprintChange(t *testing.T) {
	left := Manifest{Fingerprints: map[string]string{"package.json": "old"}}
	right := Manifest{Fingerprints: map[string]string{"package.json": "new"}}
	if manifestsEquivalent(left, right) {
		t.Fatal("changed upstream fingerprints must require a manifest update")
	}
}

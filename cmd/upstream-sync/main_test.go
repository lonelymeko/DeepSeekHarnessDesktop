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

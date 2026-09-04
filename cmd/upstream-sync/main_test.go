package main

import "testing"

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

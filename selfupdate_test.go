package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateHelperQuotesPaths(t *testing.T) {
	// A path with a space and a quote must survive shell parsing intact.
	tricky := "/tmp/My App's Folder/DeepSeekHarnessDesktop-0.1.6-darwin-arm64.dmg"
	quoted := shellQuote(tricky)
	if !strings.HasPrefix(quoted, "'") {
		t.Fatalf("not quoted: %s", quoted)
	}
	script := darwinUpdateHelper(tricky, "/Applications/DeepSeek Harness Desktop.app", "/x/y")
	if !strings.Contains(script, `'\''`) {
		t.Fatal("an embedded quote was not escaped")
	}
	if strings.Contains(script, "$(echo "+tricky) {
		t.Fatal("the raw path leaked into the script")
	}
}

// A binary outside a bundle is not something the helper can replace, and on the
// one platform where that matters it must say so rather than guess a target.
func TestUpdateTargetRejectsNonBundle(t *testing.T) {
	target, err := updateTargetPath("/usr/local/bin/DeepSeekHarnessDesktop")
	if runtime.GOOS == "darwin" {
		if err == nil {
			t.Fatalf("a bare binary must be refused on darwin, got %q", target)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error on %s: %v", runtime.GOOS, err)
	}
}

func TestUpdateTargetResolvesTheBundle(t *testing.T) {
	if _, err := updateTargetPath("/Applications/Foo.app/Contents/MacOS/Foo"); err != nil {
		t.Fatal(err)
	}
}

func TestSelfUpdateSupportedOnPackagedPlatforms(t *testing.T) {
	if !selfUpdateSupported() {
		t.Fatalf("self-update must be supported on %s", "this platform")
	}
}

func TestUpdateHelperScriptsExistPerPlatform(t *testing.T) {
	script, err := updateHelperScript("/tmp/a.dmg", "/Applications/X.app", "/Applications/X.app/Contents/MacOS/X")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hdiutil", "ditto", "DSH_UPDATE_LOG", "dsh-previous"} {
		if !strings.Contains(script, want) {
			t.Errorf("darwin helper lacks %q", want)
		}
	}
	if !strings.Contains(script, "while kill -0") {
		t.Error("the helper must wait for the application to exit")
	}
}

func TestTarGzRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := tarGzRoot(filepath.Join(dir, "missing.tar.gz")); err == nil {
		t.Fatal("a missing archive must fail")
	}
	_ = os.Remove(filepath.Join(dir, "missing.tar.gz"))
}

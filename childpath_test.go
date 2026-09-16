package main

import (
	"reflect"
	"runtime"
	"testing"
)

// The desktop app launched from Finder or the Dock inherits launchd's minimal
// PATH. augmentChildPath must repair it so the harness child can spawn dsh
// (plugins such as the plugin shop), pnpm (dsh plugin), and node (pnpm's
// shebang) — all bundled inside the runtime.

func TestAugmentChildPathLaunchdMinimal(t *testing.T) {
	root := "/Applications/DeepSeekHarnessDesktop.app/Contents/Resources/runtime"
	env := []string{
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"DSH_DESKTOP=1",
	}
	got := augmentChildPath(env, root)
	want := "PATH=" + root + "/node/bin:" + root + "/app/node_modules/.bin" +
		":/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin:/usr/local/bin"
	found := false
	for _, entry := range got {
		if entry == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("augmentChildPath(launchd minimal) missing rebuilt PATH\nwant: %s\ngot:  %v", want, got)
	}
	if len(got) != len(env) {
		t.Fatalf("expected same entry count (%d), got %d: %v", len(env), len(got), got)
	}
}

func TestAugmentChildPathKeepsTerminalPATH(t *testing.T) {
	root := "/runtime"
	env := []string{
		"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin",
		"HOME=/Users/test",
	}
	got := augmentChildPath(env, root)
	want := "PATH=/runtime/node/bin:/runtime/app/node_modules/.bin" +
		":/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
	found := false
	for _, entry := range got {
		if entry == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("augmentChildPath(terminal PATH) missing rebuilt PATH\nwant: %s\ngot:  %v", want, got)
	}
	kept := false
	for _, entry := range got {
		if entry == "HOME=/Users/test" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("non-PATH entries must survive, got %v", got)
	}
}

func TestAugmentChildPathAddsWhenMissing(t *testing.T) {
	got := augmentChildPath([]string{"HOME=/Users/test"}, "/runtime")
	want := "PATH=/runtime/node/bin:/runtime/app/node_modules/.bin" +
		":/opt/homebrew/bin:/usr/local/bin"
	if !contains(got, want) {
		t.Fatalf("missing PATH must be added\nwant: %s\ngot:  %v", want, got)
	}
}

func TestAugmentChildPathDeduplicates(t *testing.T) {
	root := "/runtime"
	env := []string{
		"PATH=" + root + "/node/bin:/usr/bin:" + root + "/node/bin",
	}
	got := augmentChildPath(env, root)
	count := 0
	for _, entry := range got {
		if entry == "PATH="+root+"/node/bin:/runtime/app/node_modules/.bin:/usr/bin:/opt/homebrew/bin:/usr/local/bin" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("deduplicated PATH expected exactly once, got %d in %v", count, got)
	}
}

func TestChildPathPrefixesWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		want := []string{"/runtime/node"}
		if got := childPathPrefixes("/runtime"); !reflect.DeepEqual(got, want) {
			t.Fatalf("windows prefixes want %v got %v", want, got)
		}
	} else {
		want := []string{"/runtime/node/bin", "/runtime/app/node_modules/.bin"}
		if got := childPathPrefixes("/runtime"); !reflect.DeepEqual(got, want) {
			t.Fatalf("posix prefixes want %v got %v", want, got)
		}
	}
}

func contains(entries []string, want string) bool {
	for _, entry := range entries {
		if entry == want {
			return true
		}
	}
	return false
}

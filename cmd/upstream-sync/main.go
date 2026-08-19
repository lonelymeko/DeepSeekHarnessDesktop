package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manifestPath = "upstream/manifest.json"

var criticalFiles = []string{
	"package.json",
	"apps/cli/package.json",
	"apps/cli/src/args.ts",
	"packages/bundle/web-app/src/startup.ts",
	"packages/bundle/web-app/cordis.patch.yml",
}

type Manifest struct {
	Repository   string            `json:"repository"`
	Ref          string            `json:"ref"`
	Commit       string            `json:"commit"`
	CLIVersion   string            `json:"cliVersion"`
	NodeVersion  string            `json:"nodeVersion"`
	NodeEngines  string            `json:"nodeEngines"`
	UpdatedAt    string            `json:"updatedAt"`
	Fingerprints map[string]string `json:"fingerprints"`
}

func main() {
	ref := flag.String("ref", "master", "upstream branch, tag, or commit")
	acceptBreaking := flag.Bool("accept-breaking", false, "record an update after manually reviewing breaking changes")
	flag.Parse()

	previous := readManifest()
	temporary, err := os.MkdirTemp("", "dsh-upstream-")
	must(err)
	defer os.RemoveAll(temporary)
	run("git", "clone", "--depth", "1", "--branch", *ref, previous.Repository, temporary)

	commit := output(temporary, "git", "rev-parse", "HEAD")
	rootPackage := readPackage(filepath.Join(temporary, "package.json"))
	cliPackage := readPackage(filepath.Join(temporary, "apps", "cli", "package.json"))
	fingerprints := map[string]string{}
	var changed []string
	for _, name := range criticalFiles {
		content, readErr := os.ReadFile(filepath.Join(temporary, filepath.FromSlash(name)))
		must(readErr)
		digest := sha256.Sum256(content)
		fingerprints[name] = hex.EncodeToString(digest[:])
		if old := previous.Fingerprints[name]; old != "" && old != fingerprints[name] {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)

	if len(changed) > 0 && !*acceptBreaking {
		report := breakingReport(previous.Commit, commit, changed)
		must(os.WriteFile("upstream/ADAPTATION_REQUIRED.md", []byte(report), 0o644))
		fmt.Fprintln(os.Stderr, "\n⚠️  检测到可能破坏桌面适配的上游更新，已停止同步。")
		fmt.Fprintln(os.Stderr, "请查看 upstream/ADAPTATION_REQUIRED.md，完成适配后使用 --accept-breaking 继续。")
		os.Exit(2)
	}

	next := Manifest{
		Repository:   previous.Repository,
		Ref:          *ref,
		Commit:       commit,
		CLIVersion:   stringValue(cliPackage, "version"),
		NodeVersion:  previous.NodeVersion,
		NodeEngines:  nestedString(rootPackage, "engines", "node"),
		UpdatedAt:    time.Now().UTC().Format(time.RFC3339),
		Fingerprints: fingerprints,
	}
	encoded, err := json.MarshalIndent(next, "", "  ")
	must(err)
	must(os.WriteFile(manifestPath, append(encoded, '\n'), 0o644))
	_ = os.Remove("upstream/ADAPTATION_REQUIRED.md")
	fmt.Printf("Upstream synchronized: %s (%s), npm @deepseek-ai/dsh@%s\n", commit, *ref, next.CLIVersion)
}

func readManifest() Manifest {
	content, err := os.ReadFile(manifestPath)
	must(err)
	var manifest Manifest
	must(json.Unmarshal(content, &manifest))
	if manifest.Repository == "" {
		manifest.Repository = "https://github.com/deepseek-ai/deepseek-harness.git"
	}
	if manifest.Fingerprints == nil {
		manifest.Fingerprints = map[string]string{}
	}
	return manifest
}

func readPackage(path string) map[string]any {
	content, err := os.ReadFile(path)
	must(err)
	var value map[string]any
	must(json.Unmarshal(content, &value))
	return value
}

func stringValue(value map[string]any, key string) string {
	text, _ := value[key].(string)
	return text
}

func nestedString(value map[string]any, outer, inner string) string {
	nested, _ := value[outer].(map[string]any)
	return stringValue(nested, inner)
}

func breakingReport(oldCommit, newCommit string, files []string) string {
	return fmt.Sprintf(`# Upstream adaptation required

DeepSeek Harness changed files that define the desktop launch contract.

- Previous commit: %s
- Candidate commit: %s
- Detected at: %s

## Critical changes

%s

## Continue after adapting

1. Review the upstream diff for every file above.
2. Update the Go launcher, runtime layout, proxy, packaging, or documentation as needed.
3. Run go test ./..., ./scripts/prepare-runtime.sh, and a desktop smoke test.
4. Record the reviewed update with go run ./cmd/upstream-sync --ref master --accept-breaking.
`, oldCommit, newCommit, time.Now().UTC().Format(time.RFC3339), "- "+strings.Join(files, "\n- "))
}

func run(name string, args ...string) {
	command := exec.Command(name, args...)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	must(command.Run())
}

func output(dir, name string, args ...string) string {
	command := exec.Command(name, args...)
	command.Dir = dir
	content, err := command.Output()
	must(err)
	return strings.TrimSpace(string(content))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

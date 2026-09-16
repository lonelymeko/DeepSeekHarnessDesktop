package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const manifestPath = "upstream/manifest.json"

// Exit codes are a contract with .github/workflows/upstream-sync.yml. Two means
// "the upstream launch contract changed; adapt before syncing" — a verdict, not
// a crash. Every unexpected failure must therefore leave with a different code.
// Go's default panic status is also 2, so an unhandled error used to masquerade
// as that verdict and turn a broken check into a bogus "adaptation required".
const (
	exitOK     = 0
	exitFailed = 1
	exitAdapt  = 2
)

// criticalFile names a launch-contract file and, optionally, projects its
// content onto the contract-relevant fields. Without a projection the whole
// file is hashed. The JSON package manifests use packageContractProjection so
// routine upstream churn — a version bump, a new script, a dependency range —
// does not read as a launch-contract change, while engines, entrypoints, the
// dependency set and every non-JSON contract file still do.
type criticalFile struct {
	path    string
	project func([]byte) ([]byte, error)
}

var criticalFiles = []criticalFile{
	{"package.json", packageContractProjection},
	{"apps/cli/package.json", packageContractProjection},
	{"apps/cli/src/args.ts", nil},
	{"packages/bundle/web-app/src/startup.ts", nil},
	{"packages/bundle/web-app/cordis.patch.yml", nil},
}

// benignPackageFields never reach the desktop launch contract, so upstream may
// change them without a manual adaptation review.
var benignPackageFields = []string{
	"version", "scripts", "publishConfig", "devDependencies",
	"packageManager", "license", "description", "author",
	"repository", "bugs", "homepage", "keywords", "files", "comments",
}

// packageContractProjection keeps only the package.json fields the desktop
// shell actually depends on. Dependency names are kept (adding or dropping one
// is structural) but their ranges are dropped, because every routine bump would
// otherwise force a review.
func packageContractProjection(content []byte) ([]byte, error) {
	var manifest map[string]any
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	for _, field := range benignPackageFields {
		delete(manifest, field)
	}
	for _, field := range []string{"dependencies", "peerDependencies", "optionalDependencies"} {
		dependencies, ok := manifest[field].(map[string]any)
		if !ok {
			continue
		}
		for name := range dependencies {
			dependencies[name] = ""
		}
	}
	// json.Marshal orders object keys, so the projection is canonical.
	return json.Marshal(manifest)
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
	cloneUpstream(previous.Repository, *ref, temporary)

	commit := output(temporary, "git", "rev-parse", "HEAD")
	rootPackage := readPackage(filepath.Join(temporary, "package.json"))
	cliPackage := readPackage(filepath.Join(temporary, "apps", "cli", "package.json"))
	fingerprints := map[string]string{}
	var changed []string
	for _, file := range criticalFiles {
		content, readErr := os.ReadFile(filepath.Join(temporary, filepath.FromSlash(file.path)))
		must(readErr)
		projected := content
		if file.project != nil {
			projected, readErr = file.project(content)
			must(readErr)
		}
		digest := sha256.Sum256(projected)
		fingerprints[file.path] = hex.EncodeToString(digest[:])
		if old := previous.Fingerprints[file.path]; old != "" && old != fingerprints[file.path] {
			changed = append(changed, file.path)
		}
	}
	sort.Strings(changed)

	if len(changed) > 0 && !*acceptBreaking {
		report := breakingReport(previous.Commit, commit, changed)
		must(os.WriteFile("upstream/ADAPTATION_REQUIRED.md", []byte(report), 0o644))
		fmt.Fprintln(os.Stderr, "\n⚠️  检测到可能破坏桌面适配的上游更新，已停止同步。")
		fmt.Fprintln(os.Stderr, "请查看 upstream/ADAPTATION_REQUIRED.md，完成适配后使用 --accept-breaking 继续。")
		os.Exit(exitAdapt)
	}

	next := Manifest{
		Repository:   previous.Repository,
		Ref:          *ref,
		Commit:       commit,
		CLIVersion:   stringValue(cliPackage, "version"),
		NodeVersion:  previous.NodeVersion,
		NodeEngines:  nestedString(rootPackage, "engines", "node"),
		Fingerprints: fingerprints,
	}
	if manifestsEquivalent(previous, next) {
		_ = os.Remove("upstream/ADAPTATION_REQUIRED.md")
		fmt.Printf("Upstream already synchronized: %s (%s), npm @deepseek-ai/dsh@%s\n", commit, *ref, next.CLIVersion)
		return
	}
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(next, "", "  ")
	must(err)
	must(os.WriteFile(manifestPath, append(encoded, '\n'), 0o644))
	_ = os.Remove("upstream/ADAPTATION_REQUIRED.md")
	fmt.Printf("Upstream synchronized: %s (%s), npm @deepseek-ai/dsh@%s\n", commit, *ref, next.CLIVersion)
}

func manifestsEquivalent(left, right Manifest) bool {
	return left.Repository == right.Repository &&
		left.Ref == right.Ref &&
		left.Commit == right.Commit &&
		left.CLIVersion == right.CLIVersion &&
		left.NodeVersion == right.NodeVersion &&
		left.NodeEngines == right.NodeEngines &&
		maps.Equal(left.Fingerprints, right.Fingerprints)
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
3. Run go test ./... and make smoke, which prepares the runtime and drives this
   shell's own handoff, session-exchange and reverse-proxy path against it.
4. Record the reviewed update with make sync-accept.
`, oldCommit, newCommit, time.Now().UTC().Format(time.RFC3339), "- "+strings.Join(files, "\n- "))
}

// cloneUpstream shallow-clones the upstream repository, retrying a few times.
// The nightly check clones a large public repository over the runner's network;
// one transient failure must not abort the whole check (and must never look like
// the adaptation verdict).
func cloneUpstream(repository, ref, destination string) {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		_ = os.RemoveAll(destination)
		command := exec.Command("git", "clone", "--depth", "1", "--branch", ref, repository, destination)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err = command.Run(); err == nil {
			return
		}
		fmt.Fprintf(os.Stderr, "upstream-sync: git clone attempt %d/3 failed: %v\n", attempt, err)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
	}
	must(err)
}

func output(dir, name string, args ...string) string {
	command := exec.Command(name, args...)
	command.Dir = dir
	content, err := command.Output()
	must(err)
	return strings.TrimSpace(string(content))
}

// must fails with the crash code rather than panicking: a panic would exit 2,
// which the workflow reads as the adaptation verdict.
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "upstream-sync:", err)
		os.Exit(exitFailed)
	}
}

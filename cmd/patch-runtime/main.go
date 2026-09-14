// Command patch-runtime applies the desktop shell's adaptations to a prepared
// DeepSeek Harness runtime.
//
// The upstream Harness ships a compatibility gap with OpenCode Go: the gateway
// rejects any model request without a stable, per-conversation
// `x-opencode-session` header (`400 MissingSessionID`), but `dsh-llm-pi-ai`
// builds request headers from the route profile alone and never forwards the
// session id it already receives in `options.sessionId`. This command patches
// that one adapter file so the header rides every API protocol.
//
// The patch is idempotent and fails loudly (without writing) when an upstream
// update moves the anchors it depends on, so a broken adaptation stops
// packaging instead of silently shipping an unpatched runtime.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// adapterRelativePath locates the patched file inside the prepared runtime.
var adapterRelativePath = []string{"app", "node_modules", "@deepseek-ai", "dsh-llm-pi-ai", "lib", "index.js"}

// patchMarker only ever appears in a patched file, making re-runs a no-op.
const patchMarker = "opencodeGoSessionHeaders"

// anchorFunc marks the module-scope function the helper is injected before.
const anchorFunc = "function requestHeaders(headers) {"

// anchorCall is the single call site that turns a profile into request headers.
const anchorCall = "headers: requestHeaders(profile.headers)"

const helperSource = `
/**
 * OpenCode Go requires a stable per-conversation "x-opencode-session" header on
 * every model request; without it the gateway answers 400 MissingSessionID and
 * refuses to route. The Harness session id already reaches this adapter as
 * options.sessionId, and this is the only seam that sees both it and the
 * resolved model's base URL, so the header is added here. pi-ai merges
 * options.headers last in every api protocol, so one injection covers
 * openai-completions, openai-responses and anthropic-messages alike.
 */
function opencodeGoSessionHeaders(headers, model, options) {
	const sessionId = options.sessionId;
	if (sessionId === void 0 || sessionId === null || String(sessionId) === "") return headers;
	let host = "";
	try {
		host = new URL(String(model.baseUrl ?? "")).hostname.toLowerCase();
	} catch {
		return headers;
	}
	if (host !== "opencode.ai" && !host.endsWith(".opencode.ai")) return headers;
	return { ...headers, "x-opencode-session": String(sessionId) };
}
`

const callReplacement = "headers: opencodeGoSessionHeaders(requestHeaders(profile.headers), model, options)"

func main() {
	runtimeDir := flag.String("runtime", "runtime/current", "prepared runtime directory")
	flag.Parse()

	path := filepath.Join(append([]string{*runtimeDir}, adapterRelativePath...)...)
	changed, err := patchAdapterFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "patch-runtime:", err)
		os.Exit(1)
	}
	if changed {
		fmt.Printf("Patched %s with the OpenCode Go session header\n", path)
		return
	}
	fmt.Printf("OpenCode Go session header already present in %s\n", path)
}

// patchAdapterFile reads, patches and atomically rewrites the adapter file it is
// given, preserving the original permissions. It reports whether it changed
// anything.
func patchAdapterFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("stat adapter %s: %w", path, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read adapter %s: %w", path, err)
	}

	patched, changed, err := patchAdapterContent(string(content))
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return false, nil
	}

	temp := path + ".patch-tmp"
	if err := os.WriteFile(temp, []byte(patched), info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("write patched adapter %s: %w", temp, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return false, fmt.Errorf("replace adapter %s: %w", path, err)
	}
	// Rename keeps the temp file's permissions, so re-apply the original bits
	// explicitly in case the umask narrowed them.
	if err := os.Chmod(path, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("restore adapter permissions %s: %w", path, err)
	}
	return true, nil
}

// patchAdapterContent inserts the session-header helper and rewrites its single
// call site. It is pure so the wording of the anchors can be validated without
// touching a file.
func patchAdapterContent(content string) (string, bool, error) {
	if strings.Contains(content, patchMarker) {
		return content, false, nil
	}

	if count := strings.Count(content, anchorFunc); count != 1 {
		return "", false, fmt.Errorf("expected exactly 1 %q anchor, found %d; the adapter changed upstream and needs re-adaptation", anchorFunc, count)
	}
	if count := strings.Count(content, anchorCall); count != 1 {
		return "", false, fmt.Errorf("expected exactly 1 %q anchor, found %d; the adapter changed upstream and needs re-adaptation", anchorCall, count)
	}

	// Insert the helper before the anchor so it lands at module scope, next to
	// requestHeaders, rather than inside another function's body.
	content = strings.Replace(content, anchorFunc, helperSource+"\n"+anchorFunc, 1)
	content = strings.Replace(content, anchorCall, callReplacement, 1)
	if !strings.Contains(content, patchMarker) {
		return "", false, fmt.Errorf("patched adapter is missing %q; refusing to write a no-op patch", patchMarker)
	}
	return content, true, nil
}

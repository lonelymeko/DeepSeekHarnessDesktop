package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// credentialDocumentName is the provider-managed credential store inside the
// Harness home, exactly as the upstream credentials provider names it.
const credentialDocumentName = ".credentials.yaml"

// osGetenv is the process environment lookup, indirected so tests can supply
// credentials without mutating the real environment.
var osGetenv = os.Getenv

// harnessCredential resolves one CredentialRef the way the upstream Harness
// does, so the desktop shell reads the same key the model adapter would use.
// The documented precedence is:
//
//	inherited process environment > $DSH_HOME/.credentials.yaml > <cwd>/.env > $DSH_HOME/.env
//
// It returns the value and a human-readable source name for the settings panel.
func harnessCredential(home, name string, lookup func(string) string) (string, string, bool) {
	if value := strings.TrimSpace(lookup(name)); value != "" {
		return value, "环境变量 " + name, true
	}
	if home != "" {
		if value := credentialFromDocument(filepath.Join(home, credentialDocumentName), name); value != "" {
			return value, credentialDocumentName, true
		}
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		if value := credentialFromEnvFile(filepath.Join(workingDirectory, ".env"), name); value != "" {
			return value, "项目 .env", true
		}
	}
	if home != "" {
		if value := credentialFromEnvFile(filepath.Join(home, ".env"), name); value != "" {
			return value, "Harness .env", true
		}
	}
	return "", "", false
}

// credentialFromDocument reads one reference out of a credentials document,
// returning "" when the file is absent or the reference is not stored.
func credentialFromDocument(path, name string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return parseCredentialRefs(string(content))[name]
}

// parseCredentialRefs reads the `refs:` mapping of a credentials document.
// The document is a strict two-level YAML mapping — `version`, then `refs` and
// `records` — so a line reader that tracks indent admits its `refs` half
// without taking on a YAML dependency, while `records` is deliberately left
// alone: records are addressed by `scope/id`, never by a CredentialRef.
func parseCredentialRefs(text string) map[string]string {
	refs := map[string]string{}
	inRefs := false
	refsIndent := -1
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if indent == 0 {
			// A top-level key opens or closes the section this reader wants.
			inRefs = strings.TrimSpace(key) == "refs"
			refsIndent = -1
			continue
		}
		if !inRefs {
			continue
		}
		if refsIndent < 0 {
			refsIndent = indent
		}
		// Deeper lines belong to a nested value rather than to the mapping.
		if indent != refsIndent {
			continue
		}
		refs[unquoteScalar(strings.TrimSpace(key))] = unquoteScalar(strings.TrimSpace(value))
	}
	return refs
}

// credentialFromEnvFile reads one `NAME=value` entry from a dotenv file.
func credentialFromEnvFile(path, name string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) != name {
			continue
		}
		return unquoteScalar(strings.TrimSpace(value))
	}
	return ""
}

// unquoteScalar removes the quoting a YAML or dotenv writer may have added.
func unquoteScalar(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		// Single-quoted YAML escapes a quote by doubling it.
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value[1 : len(value)-1]
	}
	return value
}

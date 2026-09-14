package main

import (
	"strings"
	"testing"
)

const adapterFixture = `function other() {}
function requestHeaders(headers) {
	const attribution = attributionHeaders();
	return { ...headers, ...attribution };
}
function streamWithSnapshot(options, snapshot) {
	return streamSimple(model, context, {
		headers: requestHeaders(profile.headers)
	});
}
`

func TestPatchAdapterContentAppliesHeader(t *testing.T) {
	patched, changed, err := patchAdapterContent(adapterFixture)
	if err != nil {
		t.Fatalf("patchAdapterContent: %v", err)
	}
	if !changed {
		t.Fatal("expected the fixture to be changed")
	}
	helperAt := strings.Index(patched, "function opencodeGoSessionHeaders(headers, model, options)")
	if helperAt < 0 {
		t.Fatal("patched content is missing the helper definition")
	}
	if anchorAt := strings.Index(patched, anchorFunc); anchorAt < helperAt {
		t.Fatal("helper must be declared at module scope before requestHeaders, not nested inside it")
	}
	if !strings.Contains(patched, `"x-opencode-session": String(sessionId)`) {
		t.Fatal("patched content never sets the OpenCode session header")
	}
	if !strings.Contains(patched, callReplacement) {
		t.Fatal("patched content did not rewrite the call site")
	}
	if strings.Contains(patched, anchorCall) {
		t.Fatal("patched content still contains the original call site")
	}
}

func TestPatchAdapterContentIsIdempotent(t *testing.T) {
	once, changed, err := patchAdapterContent(adapterFixture)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	if !changed {
		t.Fatal("first patch reported no change")
	}
	twice, changed, err := patchAdapterContent(once)
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if changed {
		t.Fatal("second patch changed an already-patched adapter")
	}
	if twice != once {
		t.Fatal("second patch altered already-patched content")
	}
}

func TestPatchAdapterContentRejectsMovedAnchors(t *testing.T) {
	cases := map[string]string{
		"missing function anchor": `const requestHeaders = (headers) => headers; headers: requestHeaders(profile.headers)`,
		"missing call anchor":     `function requestHeaders(headers) { return headers; }`,
		"duplicated call anchor":  adapterFixture + "\nheaders: requestHeaders(profile.headers)\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := patchAdapterContent(content); err == nil {
				t.Fatal("expected an error when an anchor is not unique")
			}
		})
	}
}

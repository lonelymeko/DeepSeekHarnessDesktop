package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseAssetName(t *testing.T) {
	tests := []struct {
		tag, goos, goarch, want string
	}{
		{"continuous", "darwin", "arm64", "DeepSeekHarnessDesktop-continuous-darwin-arm64.dmg"},
		{"v0.2.0", "windows", "amd64", "DeepSeekHarnessDesktop-0.2.0-windows-amd64-installer.exe"},
		{"v0.2.0", "linux", "amd64", "DeepSeekHarnessDesktop-0.2.0-linux-amd64.tar.gz"},
	}
	for _, test := range tests {
		got, err := releaseAssetName(test.tag, test.goos, test.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("releaseAssetName(%q, %q, %q) = %q, want %q", test.tag, test.goos, test.goarch, got, test.want)
		}
	}
}

func TestReleaseUpdaterChecksContinuousCommit(t *testing.T) {
	const latestCommit = "774d1af84fddb26ed15588b140e864053b9eef57"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/lonelymeko/DeepSeekHarnessDesktop/releases/tags/continuous" {
			t.Errorf("release request path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{
          "tag_name":"continuous",
          "name":"Continuous",
          "body":"Automated desktop packages for commit %s.",
          "html_url":"https://github.com/lonelymeko/DeepSeekHarnessDesktop/releases/tag/continuous",
          "published_at":"2026-09-04T03:04:38Z",
          "assets":[{"name":"DeepSeekHarnessDesktop-continuous-darwin-arm64.dmg","browser_download_url":"https://github.com/lonelymeko/DeepSeekHarnessDesktop/releases/download/continuous/DeepSeekHarnessDesktop-continuous-darwin-arm64.dmg","size":123,"digest":"sha256:%s"}]
        }`, latestCommit, strings.Repeat("a", 64))
	}))
	defer server.Close()

	updater := newReleaseUpdater()
	updater.apiBase = server.URL
	updater.goos = "darwin"
	updater.goarch = "arm64"
	updater.version = "continuous"
	updater.commit = strings.Repeat("1", 40)
	info, err := updater.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.ReleaseCommit != latestCommit || info.AssetSize != 123 {
		t.Fatalf("continuous update info = %+v", info)
	}

	updater.commit = latestCommit
	info, err = updater.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Fatalf("matching continuous commit reported an update: %+v", info)
	}
}

func TestReleaseUpdaterChecksStableVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/lonelymeko/DeepSeekHarnessDesktop/releases/latest" {
			t.Errorf("release request path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
          "tag_name":"v0.2.0",
          "name":"v0.2.0",
          "html_url":"https://github.com/lonelymeko/DeepSeekHarnessDesktop/releases/tag/v0.2.0",
          "assets":[{"name":"DeepSeekHarnessDesktop-0.2.0-windows-amd64-installer.exe","browser_download_url":"https://github.com/lonelymeko/DeepSeekHarnessDesktop/releases/download/v0.2.0/DeepSeekHarnessDesktop-0.2.0-windows-amd64-installer.exe","size":456,"digest":"sha256:`+strings.Repeat("b", 64)+`"}]
        }`)
	}))
	defer server.Close()

	updater := newReleaseUpdater()
	updater.apiBase = server.URL
	updater.goos = "windows"
	updater.goarch = "amd64"
	updater.version = "0.1.2"
	info, err := updater.check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.LatestVersion != "0.2.0" || info.AssetName != "DeepSeekHarnessDesktop-0.2.0-windows-amd64-installer.exe" {
		t.Fatalf("stable update info = %+v", info)
	}
}

func TestDownloadWritesExpectedBytes(t *testing.T) {
	payload := []byte("downloaded update")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	updater := newReleaseUpdater()
	updater.client = server.Client()
	destination := filepath.Join(t.TempDir(), "update.part")
	asset := githubReleaseAsset{BrowserDownloadURL: server.URL, Size: int64(len(payload))}
	if err := updater.download(context.Background(), asset, destination, nil); err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA256(destination)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(payload))
	if digest != want {
		t.Fatalf("download digest = %q, want %q", digest, want)
	}
	if content, err := os.ReadFile(destination); err != nil || string(content) != string(payload) {
		t.Fatalf("downloaded content = %q, err = %v", content, err)
	}
}

func TestReleaseDownloadValidation(t *testing.T) {
	valid := "https://github.com/lonelymeko/DeepSeekHarnessDesktop/releases/download/continuous/DeepSeekHarnessDesktop-continuous-darwin-arm64.dmg"
	if err := validateReleaseDownloadURL(valid, desktopRepository, "continuous", "DeepSeekHarnessDesktop-continuous-darwin-arm64.dmg"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"http://github.com/lonelymeko/DeepSeekHarnessDesktop/releases/download/continuous/file.dmg",
		"https://example.com/lonelymeko/DeepSeekHarnessDesktop/releases/download/continuous/file.dmg",
		"https://github.com/other/repository/releases/download/continuous/file.dmg",
	} {
		if err := validateReleaseDownloadURL(value, desktopRepository, "continuous", "file.dmg"); err == nil {
			t.Errorf("accepted untrusted download URL %q", value)
		}
	}
}

func TestSHA256DigestValidation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	if got, err := parseSHA256Digest("sha256:" + digest); err != nil || got != digest {
		t.Fatalf("valid digest = %q, %v", got, err)
	}
	for _, value := range []string{"", "sha1:" + digest, "sha256:abc", "sha256:" + strings.Repeat("z", 64)} {
		if _, err := parseSHA256Digest(value); err == nil {
			t.Errorf("accepted invalid digest %q", value)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"0.2.0", "0.1.9", 1},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
	} {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

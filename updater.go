package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	desktopRepository = "lonelymeko/DeepSeekHarnessDesktop"
	githubAPIBase     = "https://api.github.com"
)

var (
	desktopVersion = "0.1.1"
	desktopCommit  = ""
	commitPattern  = regexp.MustCompile(`(?i)\b[0-9a-f]{40}\b`)
)

type UpdateInfo struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	ReleaseName    string `json:"releaseName"`
	ReleaseURL     string `json:"releaseUrl"`
	PublishedAt    string `json:"publishedAt"`
	AssetName      string `json:"assetName"`
	AssetSize      int64  `json:"assetSize"`
	Channel        string `json:"channel"`
	Platform       string `json:"platform"`
	ReleaseCommit  string `json:"releaseCommit"`
}

type UpdateDownloadProgress struct {
	Downloaded int64 `json:"downloaded"`
	Total      int64 `json:"total"`
	Percent    int   `json:"percent"`
}

type githubRelease struct {
	TagName     string               `json:"tag_name"`
	Name        string               `json:"name"`
	Body        string               `json:"body"`
	HTMLURL     string               `json:"html_url"`
	PublishedAt string               `json:"published_at"`
	Assets      []githubReleaseAsset `json:"assets"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

type releaseUpdater struct {
	apiBase        string
	repository     string
	version        string
	commit         string
	goos           string
	goarch         string
	client         *http.Client
	cacheRoot      func() (string, error)
	openDownloaded func(string) error
	downloadMu     sync.Mutex
}

func newReleaseUpdater() *releaseUpdater {
	return &releaseUpdater{
		apiBase:    githubAPIBase,
		repository: desktopRepository,
		version:    desktopVersion,
		commit:     desktopCommit,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		client: &http.Client{
			Timeout: 45 * time.Minute,
		},
		cacheRoot: func() (string, error) {
			root, err := os.UserCacheDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(root, "DeepSeekHarnessDesktop", "updates"), nil
		},
		openDownloaded: openDownloadedUpdate,
	}
}

func (u *releaseUpdater) installDownloaded(downloaded string) error {
	return applyDownloadedUpdate(downloaded)
}

// setProxyPlan routes the updater's requests through the decided proxy. Go's
// default transport already honours the proxy environment, but it resolves that
// environment once per process, so a preference changed at runtime — or a proxy
// only the operating system knows about — needs an explicit hook.
func (u *releaseUpdater) setProxyPlan(plan proxyPlan) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if err := plan.applyToTransport(transport); err != nil {
		// An unusable proxy must not disable updating altogether: the failure is
		// reported and the request goes direct, which is what the Harness itself
		// does with a proxy URL it cannot use.
		log.Printf("DeepSeek Harness Desktop update proxy: %v; updating directly", err)
	}
	u.client.Transport = transport
}

func (u *releaseUpdater) check(ctx context.Context) (UpdateInfo, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	info := UpdateInfo{
		CurrentVersion: u.version,
		Channel:        "stable",
		Platform:       u.goos + "/" + u.goarch,
	}
	if u.version == "" || u.version == "dev" {
		return info, nil
	}

	endpoint := "/repos/" + u.repository + "/releases/latest"
	if u.version == "continuous" {
		info.Channel = "continuous"
		endpoint = "/repos/" + u.repository + "/releases/tags/continuous"
	}
	release, err := u.fetchRelease(checkCtx, endpoint)
	if err != nil {
		return info, err
	}
	assetName, err := releaseAssetName(release.TagName, u.goos, u.goarch)
	if err != nil {
		return info, err
	}
	asset, ok := findReleaseAsset(release.Assets, assetName)
	if !ok {
		return info, fmt.Errorf("release %s does not contain %s", release.TagName, assetName)
	}

	info.LatestVersion = strings.TrimPrefix(release.TagName, "v")
	info.ReleaseName = release.Name
	info.ReleaseURL = release.HTMLURL
	info.PublishedAt = release.PublishedAt
	info.AssetName = asset.Name
	info.AssetSize = asset.Size
	if u.version == "continuous" {
		info.ReleaseCommit = releaseCommit(release.Body)
		if info.ReleaseCommit == "" {
			return info, fmt.Errorf("continuous release does not identify its source commit")
		}
		info.Available = u.commit == "" || !strings.EqualFold(info.ReleaseCommit, u.commit)
	} else {
		info.Available = compareVersions(info.LatestVersion, u.version) > 0
	}
	return info, nil
}

func (u *releaseUpdater) fetchRelease(ctx context.Context, endpoint string) (githubRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.apiBase, "/")+endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "DeepSeekHarnessDesktop/"+u.version)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := u.client.Do(request)
	if err != nil {
		return githubRelease{}, fmt.Errorf("query GitHub Releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return githubRelease{}, fmt.Errorf("query GitHub Releases: %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if release.TagName == "" {
		return githubRelease{}, fmt.Errorf("GitHub release is missing a tag")
	}
	return release, nil
}

func (u *releaseUpdater) downloadAndOpen(ctx context.Context, progress func(UpdateDownloadProgress)) (string, error) {
	u.downloadMu.Lock()
	defer u.downloadMu.Unlock()

	info, err := u.check(ctx)
	if err != nil {
		return "", err
	}
	if !info.Available {
		return "", fmt.Errorf("DeepSeek Harness Desktop is already up to date")
	}
	endpoint := "/repos/" + u.repository + "/releases/latest"
	if info.Channel == "continuous" {
		endpoint = "/repos/" + u.repository + "/releases/tags/continuous"
	}
	release, err := u.fetchRelease(ctx, endpoint)
	if err != nil {
		return "", err
	}
	asset, ok := findReleaseAsset(release.Assets, info.AssetName)
	if !ok {
		return "", fmt.Errorf("release asset disappeared: %s", info.AssetName)
	}
	expectedDigest, err := parseSHA256Digest(asset.Digest)
	if err != nil {
		return "", fmt.Errorf("release asset cannot be verified: %w", err)
	}
	if err := validateReleaseDownloadURL(asset.BrowserDownloadURL, u.repository, release.TagName, asset.Name); err != nil {
		return "", err
	}

	cacheRoot, err := u.cacheRoot()
	if err != nil {
		return "", fmt.Errorf("locate update cache: %w", err)
	}
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return "", fmt.Errorf("create update cache: %w", err)
	}
	destination := filepath.Join(cacheRoot, asset.Name)
	if digest, digestErr := fileSHA256(destination); digestErr == nil && digest == expectedDigest {
		if err := u.openDownloaded(destination); err != nil {
			return "", fmt.Errorf("open downloaded update: %w", err)
		}
		return destination, nil
	}

	temporary := destination + ".part"
	_ = os.Remove(temporary)
	if err := u.download(ctx, asset, temporary, progress); err != nil {
		_ = os.Remove(temporary)
		return "", err
	}
	digest, err := fileSHA256(temporary)
	if err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("verify downloaded update: %w", err)
	}
	if digest != expectedDigest {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("downloaded update SHA-256 mismatch")
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("save downloaded update: %w", err)
	}
	if err := u.openDownloaded(destination); err != nil {
		return "", fmt.Errorf("open downloaded update: %w", err)
	}
	return destination, nil
}

func (u *releaseUpdater) download(ctx context.Context, asset githubReleaseAsset, destination string, progress func(UpdateDownloadProgress)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "DeepSeekHarnessDesktop/"+u.version)
	response, err := u.client.Do(request)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: %s", response.Status)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return fmt.Errorf("create update download: %w", err)
	}
	defer file.Close()

	total := asset.Size
	if total <= 0 {
		total = response.ContentLength
	}
	buffer := make([]byte, 256*1024)
	var downloaded int64
	lastPercent := -1
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			written, writeErr := file.Write(buffer[:read])
			downloaded += int64(written)
			if writeErr != nil {
				return fmt.Errorf("write update download: %w", writeErr)
			}
			percent := 0
			if total > 0 {
				percent = int(downloaded * 100 / total)
				if percent > 100 {
					percent = 100
				}
			}
			if progress != nil && percent != lastPercent {
				progress(UpdateDownloadProgress{Downloaded: downloaded, Total: total, Percent: percent})
				lastPercent = percent
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read update download: %w", readErr)
		}
	}
	if asset.Size > 0 && downloaded != asset.Size {
		return fmt.Errorf("downloaded update size is %d bytes, expected %d", downloaded, asset.Size)
	}
	return file.Sync()
}

func releaseAssetName(tag, goos, goarch string) (string, error) {
	version := strings.TrimPrefix(tag, "v")
	base := "DeepSeekHarnessDesktop-" + version + "-" + goos + "-" + goarch
	switch goos {
	case "darwin":
		return base + ".dmg", nil
	case "windows":
		return base + "-installer.exe", nil
	case "linux":
		return base + ".tar.gz", nil
	default:
		return "", fmt.Errorf("automatic updates are unavailable for %s/%s", goos, goarch)
	}
}

func findReleaseAsset(assets []githubReleaseAsset, name string) (githubReleaseAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubReleaseAsset{}, false
}

func releaseCommit(body string) string {
	return strings.ToLower(commitPattern.FindString(body))
}

func compareVersions(left, right string) int {
	parse := func(value string) []int {
		value = strings.TrimPrefix(value, "v")
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		result := make([]int, 3)
		for index := range result {
			if index < len(parts) {
				result[index], _ = strconv.Atoi(parts[index])
			}
		}
		return result
	}
	l, r := parse(left), parse(right)
	for index := range l {
		if l[index] > r[index] {
			return 1
		}
		if l[index] < r[index] {
			return -1
		}
	}
	return 0
}

func parseSHA256Digest(value string) (string, error) {
	algorithm, digest, ok := strings.Cut(strings.ToLower(value), ":")
	if !ok || algorithm != "sha256" || len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("GitHub did not provide a valid SHA-256 digest")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid SHA-256 digest: %w", err)
	}
	return digest, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateReleaseDownloadURL(value, repository, tag, assetName string) error {
	address, err := url.Parse(value)
	if err != nil || address.Scheme != "https" || !strings.EqualFold(address.Hostname(), "github.com") {
		return fmt.Errorf("release asset has an untrusted download URL")
	}
	wantPath := "/" + repository + "/releases/download/" + tag + "/" + assetName
	if address.EscapedPath() != wantPath {
		return fmt.Errorf("release asset download URL does not match the selected release")
	}
	return nil
}

func openDownloadedUpdate(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", path)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	case "linux":
		command = exec.Command("xdg-open", path)
	default:
		return fmt.Errorf("cannot open an update package on %s", runtime.GOOS)
	}
	return command.Start()
}

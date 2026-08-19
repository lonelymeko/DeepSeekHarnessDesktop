package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Manifest struct {
	CLIVersion  string `json:"cliVersion"`
	NodeVersion string `json:"nodeVersion"`
}

type RuntimeMarker struct {
	CLIVersion  string `json:"cliVersion"`
	NodeVersion string `json:"nodeVersion"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
}

func main() {
	goos := flag.String("os", runtime.GOOS, "target operating system")
	goarch := flag.String("arch", runtime.GOARCH, "target architecture")
	output := flag.String("output", "runtime/current", "runtime output directory")
	flag.Parse()
	absoluteOutput, err := filepath.Abs(*output)
	must(err)
	*output = absoluteOutput
	manifest := loadManifest()
	if manifest.CLIVersion == "" || manifest.NodeVersion == "" {
		panic("upstream manifest lacks cliVersion or nodeVersion")
	}
	marker := RuntimeMarker{CLIVersion: manifest.CLIVersion, NodeVersion: manifest.NodeVersion, OS: *goos, Arch: *goarch}
	if runtimeMatches(*output, marker) {
		fmt.Printf("Reusing prepared DeepSeek Harness runtime at %s\n", *output)
		return
	}

	platform, archive := nodePlatform(*goos, *goarch, manifest.NodeVersion)
	cache := filepath.Join("runtime", "cache", archive)
	if _, err := os.Stat(cache); os.IsNotExist(err) {
		download("https://nodejs.org/dist/v"+manifest.NodeVersion+"/"+archive, cache)
	}
	_ = os.RemoveAll(*output)
	must(os.MkdirAll(filepath.Join(*output, "node"), 0o755))
	if strings.HasSuffix(archive, ".zip") {
		extractZip(cache, filepath.Join(*output, "node"))
	} else {
		extractTarGz(cache, filepath.Join(*output, "node"))
	}

	appDir := filepath.Join(*output, "app")
	must(os.MkdirAll(appDir, 0o755))
	packageJSON := fmt.Sprintf("{\n  \"private\": true,\n  \"dependencies\": {\n    \"@deepseek-ai/dsh\": \"%s\"\n  }\n}\n", manifest.CLIVersion)
	must(os.WriteFile(filepath.Join(appDir, "package.json"), []byte(packageJSON), 0o644))
	node := filepath.Join(*output, "node", "bin", "node")
	npmCLI := filepath.Join(*output, "node", "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if *goos == "windows" {
		node = filepath.Join(*output, "node", "node.exe")
		npmCLI = filepath.Join(*output, "node", "node_modules", "npm", "bin", "npm-cli.js")
	}
	command := exec.Command(node, npmCLI, "install", "--omit=dev", "--no-audit", "--no-fund")
	command.Dir = appDir
	command.Env = append(os.Environ(), "npm_config_platform="+platform)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	must(command.Run())
	approve := exec.Command(node, npmCLI, "install-scripts", "approve", "--all")
	approve.Dir = appDir
	approve.Env = os.Environ()
	approve.Stdout, approve.Stderr = os.Stdout, os.Stderr
	must(approve.Run())
	markerJSON, err := json.MarshalIndent(marker, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(*output, ".runtime-manifest.json"), append(markerJSON, '\n'), 0o644))
	fmt.Printf("Prepared DeepSeek Harness %s with Node %s for %s/%s at %s\n", manifest.CLIVersion, manifest.NodeVersion, *goos, *goarch, *output)
}

func runtimeMatches(root string, expected RuntimeMarker) bool {
	content, err := os.ReadFile(filepath.Join(root, ".runtime-manifest.json"))
	if err != nil {
		return false
	}
	var actual RuntimeMarker
	if json.Unmarshal(content, &actual) != nil || actual != expected {
		return false
	}
	node := filepath.Join(root, "node", "bin", "node")
	if expected.OS == "windows" {
		node = filepath.Join(root, "node", "node.exe")
	}
	entry := filepath.Join(root, "app", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js")
	for _, path := range []string{node, entry} {
		if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func loadManifest() Manifest {
	content, err := os.ReadFile("upstream/manifest.json")
	must(err)
	var manifest Manifest
	must(json.Unmarshal(content, &manifest))
	return manifest
}

func nodePlatform(goos, goarch, version string) (string, string) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		panic("unsupported architecture: " + goarch)
	}
	osName := map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win"}[goos]
	if osName == "" {
		panic("unsupported operating system: " + goos)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return osName, fmt.Sprintf("node-v%s-%s-%s%s", version, osName, arch, ext)
}

func download(address, destination string) {
	fmt.Println("Downloading", address)
	response, err := http.Get(address)
	must(err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		panic(response.Status)
	}
	must(os.MkdirAll(filepath.Dir(destination), 0o755))
	file, err := os.Create(destination)
	must(err)
	_, err = io.Copy(file, response.Body)
	must(err)
	must(file.Close())
}

func extractTarGz(source, destination string) {
	file, err := os.Open(source)
	must(err)
	defer file.Close()
	gz, err := gzip.NewReader(file)
	must(err)
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		must(err)
		writeEntry(destination, stripRoot(header.Name), header.FileInfo().Mode(), header.FileInfo().IsDir(), reader)
	}
}

func extractZip(source, destination string) {
	reader, err := zip.OpenReader(source)
	must(err)
	defer reader.Close()
	for _, file := range reader.File {
		input, err := file.Open()
		must(err)
		writeEntry(destination, stripRoot(file.Name), file.Mode(), file.FileInfo().IsDir(), input)
		input.Close()
	}
}

func stripRoot(name string) string {
	parts := strings.Split(filepath.ToSlash(name), "/")
	if len(parts) < 2 {
		return ""
	}
	return filepath.Join(parts[1:]...)
}

func writeEntry(root, name string, mode os.FileMode, directory bool, input io.Reader) {
	if name == "" {
		return
	}
	path := filepath.Join(root, name)
	if directory {
		must(os.MkdirAll(path, mode.Perm()))
		return
	}
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	must(err)
	_, err = io.Copy(output, input)
	must(err)
	must(output.Close())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

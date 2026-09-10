package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// The Node distribution ships `bin/npm` and `bin/npx` as symlinks into
// `lib/node_modules`. Flattening them into empty regular files leaves a shim
// that exits 0 without doing anything, which is what a broken packaged runtime
// looks like: every `npm` invocation silently succeeds and does nothing.

func writeSymlinkArchive(t *testing.T, archive string, root string, linkTarget string) {
	t.Helper()
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	entries := []struct {
		name     string
		mode     int64
		body     string
		linkname string
		typeflag byte
	}{
		{name: root + "/bin/", mode: 0o755, typeflag: tar.TypeDir},
		{name: root + "/bin/node", mode: 0o755, body: "binary\n", typeflag: tar.TypeReg},
		{name: root + "/bin/npm", mode: 0o777, linkname: linkTarget, typeflag: tar.TypeSymlink},
		{name: root + "/lib/node_modules/npm/bin/npm-cli.js", mode: 0o644, body: "console.log(1)\n", typeflag: tar.TypeReg},
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Typeflag: entry.typeflag, Linkname: entry.linkname}
		if entry.typeflag == tar.TypeReg {
			header.Size = int64(len(entry.body))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarGzPreservesSymlinks(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "node.tar.gz")
	writeSymlinkArchive(t, archive, "node-v1.0.0-darwin-arm64", "../lib/node_modules/npm/bin/npm-cli.js")
	destination := filepath.Join(t.TempDir(), "out")
	extractTarGz(archive, destination)

	link := filepath.Join(destination, "bin", "npm")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bin/npm extracted as %v, want a symlink", info.Mode())
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Fatalf("bin/npm target = %q", target)
	}
	// The link must resolve to the extracted file; a link that dangles is no
	// better than the empty file this test exists to prevent.
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("bin/npm does not resolve: %v", err)
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "console.log(1)\n" {
		t.Fatalf("bin/npm resolves to %q", body)
	}

	// A regular file must keep its executable bit.
	nodeInfo, err := os.Stat(filepath.Join(destination, "bin", "node"))
	if err != nil {
		t.Fatal(err)
	}
	if nodeInfo.Mode().Perm()&0o100 == 0 {
		t.Fatalf("bin/node mode = %v, want the execute bit", nodeInfo.Mode().Perm())
	}
}

func TestExtractZipPreservesSymlinks(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "node.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: "node-v1.0.0-win-x64/bin/npm", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	link, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := link.Write([]byte("../lib/node_modules/npm/bin/npm-cli.js")); err != nil {
		t.Fatal(err)
	}
	regular, err := writer.Create("node-v1.0.0-win-x64/bin/npm.cmd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := regular.Write([]byte("@echo off\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "out")
	extractZip(archive, destination)

	info, err := os.Lstat(filepath.Join(destination, "bin", "npm"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bin/npm extracted as %v, want a symlink", info.Mode())
	}
	body, err := os.ReadFile(filepath.Join(destination, "bin", "npm.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "@echo off\n" {
		t.Fatalf("bin/npm.cmd = %q", body)
	}
}

func TestExtractTarGzRefusesEscapingSymlink(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "node.tar.gz")
	writeSymlinkArchive(t, archive, "node-v1.0.0-darwin-arm64", "../../../../etc/passwd")
	destination := filepath.Join(t.TempDir(), "out")

	defer func() {
		if recover() == nil {
			t.Fatal("an escaping symlink must abort the extraction")
		}
	}()
	extractTarGz(archive, destination)
}

func TestWithinRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "root")
	cases := map[string]bool{
		filepath.Join(root, "bin", "npm"):         true,
		filepath.Join(root, "lib", "x"):           true,
		root:                                      true,
		filepath.Join(root, "..", "escape"):       false,
		filepath.Join(root, "..", "root-sibling"): false,
		filepath.Join(string(filepath.Separator)): false,
	}
	for path, want := range cases {
		if got := withinRoot(root, path); got != want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, path, got, want)
		}
	}
}

func TestStripRoot(t *testing.T) {
	cases := map[string]string{
		"node-v26.7.0-darwin-arm64/bin/node": "bin/node",
		"node-v26.7.0-win-x64/npm.cmd":       "npm.cmd",
		"top-level":                          "",
	}
	for input, want := range cases {
		if got := stripRoot(input); got != want {
			t.Errorf("stripRoot(%q) = %q, want %q", input, got, want)
		}
	}
}

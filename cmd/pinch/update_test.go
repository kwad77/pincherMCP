package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsPincherModule(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"matches pincher", "module github.com/pincherMCP/pincher\n\ngo 1.25.0\n", true},
		{"submodule path also matches", "// note\nmodule github.com/pincherMCP/pincher/cmd/foo\n", true},
		{"different module", "module github.com/other/pkg\n", false},
		{"empty file", "", false},
		{"no module directive", "go 1.25.0\nrequire foo v1.0.0\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := filepath.Join(t.TempDir(), "go.mod")
			if err := os.WriteFile(tmp, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := isPincherModule(tmp); got != tc.want {
				t.Fatalf("isPincherModule(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestIsPincherModule_MissingFile(t *testing.T) {
	if isPincherModule(filepath.Join(t.TempDir(), "nope.mod")) {
		t.Fatal("missing file should return false")
	}
}

func TestFindRepoRoot_FindsAncestor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/pincherMCP/pincher\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	deep := filepath.Join(root, "internal", "db", "nested")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got := findRepoRoot(deep)
	if got != root {
		t.Fatalf("findRepoRoot(%q) = %q, want %q", deep, got, root)
	}
}

func TestFindRepoRoot_NoAncestor(t *testing.T) {
	dir := t.TempDir()
	if got := findRepoRoot(dir); got != "" {
		t.Fatalf("findRepoRoot in empty tree returned %q, want empty", got)
	}
}

func TestFindRepoRoot_ForeignModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/other/repo\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := findRepoRoot(root); got != "" {
		t.Fatalf("foreign module matched, got %q", got)
	}
}

func TestNormaliseVersion(t *testing.T) {
	cases := map[string]string{
		"v0.3.0":   "0.3.0",
		"0.3.0":    "0.3.0",
		" v1.2.3 ": "1.2.3",
		"":         "",
	}
	for in, want := range cases {
		if got := normaliseVersion(in); got != want {
			t.Errorf("normaliseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickAssetForPlatform_MatchesGOOS(t *testing.T) {
	rel := gitRelease{
		TagName: "v0.3.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "u1", Size: 100},
			{Name: "pincher_" + runtime.GOOS + "_" + runtime.GOARCH, BrowserDownloadURL: "u-bin", Size: 12345},
			{Name: "pincher_other_arch", BrowserDownloadURL: "u3", Size: 9999},
		},
	}
	got := pickAssetForPlatform(rel)
	if got.BrowserDownloadURL != "u-bin" {
		t.Fatalf("got %+v, want asset with URL u-bin", got)
	}
}

func TestPickAssetForPlatform_SkipsArchives(t *testing.T) {
	osTag := runtime.GOOS
	archTag := runtime.GOARCH
	rel := gitRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{Name: "pincher_" + osTag + "_" + archTag + ".tar.gz", BrowserDownloadURL: "u-tgz"},
			{Name: "pincher_" + osTag + "_" + archTag + ".zip", BrowserDownloadURL: "u-zip"},
		},
	}
	got := pickAssetForPlatform(rel)
	if got.BrowserDownloadURL != "" {
		t.Fatalf("archive should not be picked, got %+v", got)
	}
}

func TestPickAssetForPlatform_NoMatch(t *testing.T) {
	rel := gitRelease{
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{
			{Name: "checksums.txt", BrowserDownloadURL: "u"},
		},
	}
	got := pickAssetForPlatform(rel)
	if got.BrowserDownloadURL != "" {
		t.Fatalf("expected no match, got %+v", got)
	}
}

// TestUpdateCLI_CheckBinary exercises the dispatch + in-repo detection
// end-to-end by building the binary, then running `pincher update --check`
// from inside this checkout. We don't assert "up to date" / "behind"
// because that depends on origin state; we only assert the command exits
// 0 and prints the in-repo banner.
func TestUpdateCLI_CheckBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	bin := filepath.Join(t.TempDir(), pincherBinaryName())
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	cmd := exec.Command(bin, "update", "--check")
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pincher update --check: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "in-repo mode") {
		t.Fatalf("expected in-repo banner; got:\n%s", got)
	}
}

// TestUpdateCLI_DryRunStandalone forces the standalone path with --source
// pointed at a directory that is *not* a pincher checkout, so detection
// fails and we exercise the GitHub releases code path with --dry-run.
// The test depends on GitHub being reachable and the latest release
// existing — skip when offline.
func TestUpdateCLI_DryRunStandalone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network-dependent test in -short mode")
	}

	bin := filepath.Join(t.TempDir(), pincherBinaryName())
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	notARepo := t.TempDir()
	cmd := exec.Command(bin, "update", "--check", "--source", notARepo)
	out, err := cmd.CombinedOutput()
	// --source pointing at a non-repo is treated as "no source detected",
	// which falls through to standalone mode.
	if err != nil && !strings.Contains(string(out), "standalone mode") {
		t.Skipf("standalone path unreachable (likely offline): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "standalone mode") && !strings.Contains(string(out), "in-repo mode") {
		t.Fatalf("expected standalone or in-repo banner; got:\n%s", out)
	}
}

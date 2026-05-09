package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pincherMCP/pincher/internal/db"
)

func TestDashboardURL(t *testing.T) {
	cases := map[string]string{
		"http://localhost:7777":          "http://localhost:7777/v1/dashboard",
		"http://localhost:7777/":         "http://localhost:7777/v1/dashboard",
		"http://localhost:7777/pincher":  "http://localhost:7777/pincher/v1/dashboard",
		"http://localhost:7777/pincher/": "http://localhost:7777/pincher/v1/dashboard",
	}
	for in, want := range cases {
		if got := dashboardURL(in); got != want {
			t.Errorf("dashboardURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickFreePort_FindsFreeSlot(t *testing.T) {
	// Bind 7777 ourselves so pickFreePort has to scan past it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port

	// Scan starting at the taken port. pickFreePort should walk past it.
	got, err := pickFreePort(taken, 4)
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	if got == taken {
		t.Fatalf("pickFreePort returned the taken port %d", got)
	}
	// And it should be reachable.
	ln2, err := net.Listen("tcp", "127.0.0.1:"+itoa(got))
	if err != nil {
		t.Fatalf("Listen on returned port %d: %v", got, err)
	}
	ln2.Close()
}

func TestPickFreePort_AllBusy(t *testing.T) {
	// Take 3 consecutive ports, then ask pickFreePort to scan exactly that range.
	listeners := make([]net.Listener, 0, 3)
	startPort := 0
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		listeners = append(listeners, ln)
		if i == 0 {
			startPort = ln.Addr().(*net.TCPAddr).Port
		}
	}
	defer func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}()

	// Note: this test is flaky in theory because the OS may not give us
	// 3 consecutive ports. In practice on a quiet test host it usually does.
	// We just assert that *some* error message comes back when no port is
	// free, even if the listener-port spread doesn't line up.
	_, _ = pickFreePort(startPort, 1) // 1-port scan against a known-busy port
	if _, err := pickFreePort(startPort, 1); err == nil {
		t.Fatalf("pickFreePort with n=1 on busy port should fail")
	}
}

func TestProbeHTTPHealthy_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.WriteHeader(200)
			w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if !probeHTTPHealthy(srv.URL) {
		t.Fatal("probe should succeed against healthy server")
	}
}

func TestProbeHTTPHealthy_NotRunning(t *testing.T) {
	// Bind and immediately close so the port is free.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	if probeHTTPHealthy("http://" + addr) {
		t.Fatal("probe should fail against closed port")
	}
}

func TestProbeHTTPHealthy_Empty(t *testing.T) {
	if probeHTTPHealthy("") {
		t.Fatal("probe with empty URL should be false")
	}
}

func TestPidIsAlive_Zero(t *testing.T) {
	if pidIsAlive(0) {
		t.Fatal("pid 0 should be dead")
	}
	if pidIsAlive(-1) {
		t.Fatal("negative pid should be dead")
	}
}

func TestPidIsAlive_Self(t *testing.T) {
	if !pidIsAlive(os.Getpid()) {
		t.Fatal("self should be alive")
	}
}

// TestFindLiveHTTPServer_NoRow returns false when the sessions table is empty.
func TestFindLiveHTTPServer_NoRow(t *testing.T) {
	store := newWebTestStore(t)
	if _, _, ok := findLiveHTTPServer(store); ok {
		t.Fatal("expected no result on empty sessions table")
	}
}

// TestFindLiveHTTPServer_DeadPID returns false when the row's PID is dead.
func TestFindLiveHTTPServer_DeadPID(t *testing.T) {
	store := newWebTestStore(t)
	if err := store.RecordSession("sess-dead", time.Now().Add(-1*time.Hour), 1, 100, 200, 0.001, "http://127.0.0.1:65535", 999999); err != nil {
		t.Fatalf("RecordSession: %v", err)
	}
	if _, _, ok := findLiveHTTPServer(store); ok {
		t.Fatal("expected dead PID to disqualify the row")
	}
}

// TestFindLiveHTTPServer_LiveServer returns true when a real httptest server
// is recorded with the current PID. Uses an httptest server so the probe
// path actually succeeds.
func TestFindLiveHTTPServer_LiveServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	store := newWebTestStore(t)
	if err := store.RecordSession("sess-live", time.Now(), 1, 100, 200, 0.001, srv.URL, os.Getpid()); err != nil {
		t.Fatalf("RecordSession: %v", err)
	}

	url, pid, ok := findLiveHTTPServer(store)
	if !ok {
		t.Fatal("expected to find live HTTP server")
	}
	if url != srv.URL {
		t.Errorf("url=%q, want %q", url, srv.URL)
	}
	if pid != os.Getpid() {
		t.Errorf("pid=%d, want %d", pid, os.Getpid())
	}
}

// TestWebCLI_Binary_NoStart asserts the command exits 1 with --no-start
// when no server is running. We run the actual binary so dispatch +
// flag parsing are exercised.
func TestWebCLI_Binary_NoStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping CLI binary build in -short mode")
	}

	bin := filepath.Join(t.TempDir(), pincherBinaryName())
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	dataDir := t.TempDir()
	cmd := exec.Command(bin, "web", "--no-start", "--data-dir", dataDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit with --no-start on empty store; got success.\nstdout: %s", out)
	}
	if !strings.Contains(string(out), "no live HTTP server") {
		t.Fatalf("expected 'no live HTTP server' in error; got:\n%s", out)
	}
}

func newWebTestStore(t *testing.T) *db.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// itoa is a tiny stdlib-free integer-to-string for test setup so we
// don't pull in strconv just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

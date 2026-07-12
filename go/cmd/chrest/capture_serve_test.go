package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	capture_serve "github.com/amarbel-llc/cutting-garden/pkgs/capture_serve"
)

// buildChrestBinary compiles the chrest binary this test package produces
// into a temp dir and returns its path. Built fresh rather than relying on
// a $CHREST_BIN / PATH lookup (the bats suites' convention) because this
// test needs the exact binary this working tree produces, capture-serve
// wiring included.
func buildChrestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "chrest")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build chrest: %v\n%s", err, out)
	}
	return bin
}

// requireHeadlessFirefox skips the test when no functional headless
// Firefox is available — mirrors the bats suites' `setup()` skip so this
// test degrades the same way in environments without a browser (e.g. the
// nix build sandbox's checkPhase, which never has Firefox on PATH).
func requireHeadlessFirefox(t *testing.T) string {
	t.Helper()
	firefox, err := exec.LookPath("firefox")
	if err != nil {
		firefox, err = exec.LookPath("firefox-esr")
	}
	if err != nil {
		t.Skip("no Firefox found on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, firefox, "--headless", "--version").Run(); err != nil {
		t.Skip("headless Firefox not functional")
	}
	return firefox
}

// memWriter is a minimal capture_plugin.Writer fake for the orchestrator
// side of the test: it doesn't compute real content-addressed digests
// (byte-stable digesting is capturebatch's own unit-tested responsibility,
// see TestBuildReceiptPostOrder) — it only proves every blob the RFC 0008
// blob protocol hands it round-trips end to end over the real
// socket/fd-passing transport.
type memWriter struct {
	mu sync.Mutex
	n  int
}

func (w *memWriter) WriteBlob(_ context.Context, r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	w.mu.Lock()
	w.n++
	id := fmt.Sprintf("blake2b256-fake%d", w.n)
	w.mu.Unlock()
	return id, int64(len(b)), nil
}

// TestCaptureServeEndToEnd drives one real RFC 0008 session against the
// compiled chrest binary: launch with CAPTURE_PLUGIN_COOKIE set, read and
// validate the announce line, dial the rendezvous socket, and run one
// capture.batch via cutting-garden's own capture_serve.RunBatch — the same
// driver code a real orchestrator uses. This proves the handshake,
// transport, and NewBatchHandler wiring work end to end, and that the
// lifecycle wrapper (stdin EOF) shuts the session down cleanly.
func TestCaptureServeEndToEnd(t *testing.T) {
	requireHeadlessFirefox(t)
	bin := buildChrestBinary(t)

	fixture := filepath.Join(t.TempDir(), "test.html")
	if err := os.WriteFile(fixture,
		[]byte("<!doctype html><html><head><title>Test</title></head>"+
			"<body><h1>Hello from chrest</h1></body></html>"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	target := "file://" + fixture

	cookie, err := capture_serve.NewCookie()
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}

	cmd := exec.Command(bin, "capture-serve")
	cmd.Env = append(os.Environ(), capture_serve.CookieEnv+"="+cookie)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start chrest capture-serve: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	handshake, err := capture_serve.ReadAnnounce(stdout, cookie)
	if err != nil {
		t.Fatalf("ReadAnnounce: %v (stderr: %s)", err, stderr.String())
	}

	conn, err := capture_serve.DialAnnounced(handshake)
	if err != nil {
		t.Fatalf("DialAnnounced: %v", err)
	}

	dest := &memWriter{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := capture_serve.RunBatch(ctx, conn, dest, capture_serve.BatchParams{
		Target:   target,
		Captures: []capture_serve.CaptureSpec{{Name: "cap", Format: "text"}},
	})
	if err != nil {
		t.Fatalf("RunBatch: %v (stderr: %s)", err, stderr.String())
	}

	if result.Schema != capture_serve.SchemaV2 {
		t.Errorf("result schema = %q, want %q", result.Schema, capture_serve.SchemaV2)
	}
	if result.Plugin.Name != "chrest" {
		t.Errorf("result plugin name = %q, want %q", result.Plugin.Name, "chrest")
	}
	if len(result.Captures) != 1 {
		t.Fatalf("captures = %d, want 1", len(result.Captures))
	}
	got := result.Captures[0]
	if got.Error != nil {
		t.Fatalf("capture error: %+v", got.Error)
	}
	if got.Receipt == nil || got.Receipt.ID == "" {
		t.Fatalf("capture receipt missing: %+v", got)
	}
	if dest.n == 0 {
		t.Error("no blobs were written through the blob protocol")
	}

	// Exercise the lifecycle wrapper's stdin-EOF trigger rather than
	// killing the child — a clean shutdown, not a forced one.
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Errorf("chrest capture-serve exit: %v (stderr: %s)", err, stderr.String())
	}
}

// TestCaptureServeRequiresCookie asserts the RFC 0008 guard: invoked
// without CAPTURE_PLUGIN_COOKIE, chrest MUST exit non-zero and print
// nothing to stdout (the handshake-rejection contract other launchers rely
// on to detect "not orchestrator-launched" vs. a real bring-up failure).
// No Firefox needed — this fails before any browser session opens.
func TestCaptureServeRequiresCookie(t *testing.T) {
	bin := buildChrestBinary(t)

	cmd := exec.Command(bin, "capture-serve")
	cmd.Env = os.Environ() // deliberately no CAPTURE_PLUGIN_COOKIE
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected a non-zero exit when CAPTURE_PLUGIN_COOKIE is unset")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty (protocol-only stdout MUST NOT be polluted)", stdout.String())
	}
}

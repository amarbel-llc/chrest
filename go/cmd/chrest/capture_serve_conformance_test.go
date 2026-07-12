package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	capture_plugin "code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	capture_serve "code.linenisgreat.com/cutting-garden/pkgs/capture_serve"

	"code.linenisgreat.com/chrest/go/internal/echo/capturebatch"
)

// TestCaptureBatchAndCaptureServeProduceEquivalentReceipts is the RFC 0008
// §Conformance check: capture the same target/format through both
// transports and assert the node sequences are equivalent.
//
// A literal byte-for-byte diff of two independent real captures cannot
// pass — and would be the wrong test — because two fields are legitimately
// per-run even for two v1 runs back to back:
//   - the shared outcome node's `datetime` (cutting-garden's own design;
//     see capture_plugin's TestWriteReceipt_IdentityStableAcrossRuns)
//   - chrest's plugin-outcome node's `process.command_line`, which embeds
//     a randomly-generated Firefox profile path per launch — an
//     intentional per-run *observation* (chrest#102 moved it out of the
//     identity-affecting environment node specifically because it isn't
//     stable; see mapping.go's outcomeBody).
//
// Excluding exactly those two fields, every node in the post-order
// sequence — type, ref structure (alias + referenced type; NOT the
// referenced digest, since digests upstream of either excluded field
// necessarily differ too), and body — must match. That isolates the one
// thing this test actually exists to check: whether the v2 transport
// (capture-serve) introduces ANY delta beyond what two v1 runs would
// already show. It doesn't — runOneWithWriter/buildReceipt is the same
// Go code either way; the one wire-level seam unique to v2
// (convertCaptureSpec's options round-trip) is what this indirectly
// exercises by using capture options.
func TestCaptureBatchAndCaptureServeProduceEquivalentReceipts(t *testing.T) {
	requireHeadlessFirefox(t)
	bin := buildChrestBinary(t)

	fixture := filepath.Join(t.TempDir(), "test.html")
	if err := os.WriteFile(
		fixture,
		[]byte("<!doctype html><html><head><title>Test</title></head>"+
			"<body><h1>Hello from chrest</h1></body></html>"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	target := "file://" + fixture
	const format = "text"

	v1Nodes := runCaptureBatchAndRecordNodes(t, bin, target, format)
	v2Nodes := runCaptureServeAndRecordNodes(t, bin, target, format)

	assertEquivalentNodeSequences(t, v1Nodes, v2Nodes)
}

// runCaptureBatchAndRecordNodes drives one v1 capture through the real
// `chrest capture-batch` subprocess with a writer.cmd script that
// sha256-digests each blob (matching cutting-garden's own
// capture_serve_testpeer.MemStore scheme) and records its raw bytes to a
// sequentially-numbered file, then parses each recorded file as a node in
// write (post-)order.
func runCaptureBatchAndRecordNodes(t *testing.T, bin, target, format string) []capture_plugin.Node {
	t.Helper()

	recDir := t.TempDir()
	script := filepath.Join(t.TempDir(), "sha256-writer.sh")
	scriptSrc := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
n_file=%q
n=$(( $(cat "$n_file" 2>/dev/null || echo 0) + 1 ))
echo "$n" > "$n_file"
out="%s/$(printf 'node-%%03d' "$n")"
cat > "$out"
size=$(wc -c < "$out")
hash=$(sha256sum "$out" | cut -d' ' -f1)
echo "{\"id\":\"sha256-$hash\",\"size\":$size}"
`, filepath.Join(recDir, ".counter"), recDir)
	if err := os.WriteFile(script, []byte(scriptSrc), 0o755); err != nil {
		t.Fatal(err)
	}

	input := capturebatch.BatchInput{
		Schema: capturebatch.BatchSchema,
		Writer: capturebatch.WriterSpec{Cmd: []string{script}},
		Target: target,
		Defaults: &capturebatch.Defaults{
			Normalize: boolPtr(true),
			Plugin:    map[string]any{"browser": "firefox"},
		},
		Captures: []capturebatch.CaptureSpec{{Name: "cap", Format: format}},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "capture-batch")
	cmd.Stdin = strings.NewReader(string(inputJSON))
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("chrest capture-batch: %v (stderr: %s)", err, stderr.String())
	}

	var output capturebatch.BatchOutput
	if err := json.Unmarshal([]byte(stdout.String()), &output); err != nil {
		t.Fatalf("parse capture-batch output: %v (raw: %s)", err, stdout.String())
	}
	if len(output.Captures) != 1 || output.Captures[0].Error != nil {
		t.Fatalf("capture-batch capture failed: %+v", output)
	}

	entries, err := os.ReadDir(recDir)
	if err != nil {
		t.Fatal(err)
	}
	var nodes []capture_plugin.Node
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "node-") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(recDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		node, err := capture_plugin.ParseNode(strings.NewReader(string(b)))
		if err != nil {
			t.Fatalf("parse recorded node %s: %v", e.Name(), err)
		}
		nodes = append(nodes, node)
	}
	// os.ReadDir sorts by filename, and the sequential zero-padded
	// counter names sort in write order.
	return nodes
}

// runCaptureServeAndRecordNodes drives one v2 capture through the real
// `chrest capture-serve` subprocess, using cutting-garden's own
// ReadAnnounce/DialAnnounced/RunBatch as the orchestrator-side driver
// (the same one a real orchestrator uses), with a sha256Store recording
// every blob's raw bytes in write order.
func runCaptureServeAndRecordNodes(t *testing.T, bin, target, format string) []capture_plugin.Node {
	t.Helper()

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

	store := &sha256Store{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := capture_serve.RunBatch(ctx, conn, store, capture_serve.BatchParams{
		Target: target,
		Defaults: &capture_serve.BatchDefaults{
			Normalize: boolPtr(true),
			Plugin:    map[string]any{"browser": "firefox"},
		},
		Captures: []capture_serve.CaptureSpec{{Name: "cap", Format: format}},
	})
	if err != nil {
		t.Fatalf("RunBatch: %v (stderr: %s)", err, stderr.String())
	}
	if len(result.Captures) != 1 || result.Captures[0].Error != nil {
		t.Fatalf("capture-serve capture failed: %+v", result)
	}

	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Errorf("chrest capture-serve exit: %v (stderr: %s)", err, stderr.String())
	}

	nodes := make([]capture_plugin.Node, 0, len(store.written))
	for i, b := range store.written {
		node, err := capture_plugin.ParseNode(strings.NewReader(string(b)))
		if err != nil {
			t.Fatalf("parse written node %d: %v", i, err)
		}
		nodes = append(nodes, node)
	}
	return nodes
}

// sha256Store is a capture_plugin.Writer that digests with sha256 (the
// same scheme cutting-garden's own capture_serve_testpeer.MemStore uses)
// and remembers every blob's raw bytes in write order.
type sha256Store struct {
	mu      sync.Mutex
	written [][]byte
}

func (s *sha256Store) WriteBlob(_ context.Context, r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(b)
	id := "sha256-" + hex.EncodeToString(sum[:])
	s.mu.Lock()
	s.written = append(s.written, b)
	s.mu.Unlock()
	return id, int64(len(b)), nil
}

// chrestOutcomeTypePreview mirrors capturebatch's unexported
// outcomeTypePreview (mapping.go) — duplicated here because this test
// lives in package main and that constant isn't exported. This
// conformance run targets a file:// fixture (no HTTP response observed),
// so chrest's plugin-outcome node is always the preview variant, carrying
// process.command_line. Keep in sync if the type string ever changes.
const chrestOutcomeTypePreview = "jcs-chrest-capture-outcome-v2-preview"

// nodeExclusions names the one field per excludable node type that
// legitimately varies per capture run — see the test doc comment above
// for why each is excluded and where it's tracked. chrest's environment
// node needs no entry here post-chrest#102: with command_line relocated
// to outcome, environment is fully stable and compared byte-for-byte
// like every other node.
var nodeExclusions = map[string]string{
	capture_plugin.TypeOutcome: "datetime", // per-run by cutting-garden's own design
	chrestOutcomeTypePreview:   "process",  // chrest#102: command_line volatility, now under outcome's process key
}

// assertEquivalentNodeSequences compares two post-order node sequences
// for logical equivalence: same length, same type per position, same ref
// structure (alias + referenced type — NOT the referenced digest, which
// necessarily differs downstream of either excluded field), and
// byte-equal bodies except for the two documented exclusions.
func assertEquivalentNodeSequences(t *testing.T, v1, v2 []capture_plugin.Node) {
	t.Helper()
	if len(v1) != len(v2) {
		t.Fatalf("node count: v1=%d v2=%d", len(v1), len(v2))
	}
	for i := range v1 {
		a, b := v1[i], v2[i]
		if a.Type != b.Type {
			t.Fatalf("node %d type: v1=%q v2=%q", i, a.Type, b.Type)
			continue
		}
		if len(a.Refs) != len(b.Refs) {
			t.Errorf("node %d (%s) ref count: v1=%d v2=%d", i, a.Type, len(a.Refs), len(b.Refs))
		} else {
			for j := range a.Refs {
				if a.Refs[j].Alias != b.Refs[j].Alias || a.Refs[j].TypeString != b.Refs[j].TypeString {
					t.Errorf("node %d (%s) ref %d: v1={%s %s} v2={%s %s}",
						i, a.Type, j, a.Refs[j].Alias, a.Refs[j].TypeString, b.Refs[j].Alias, b.Refs[j].TypeString)
				}
			}
		}

		if excludedTopKey, excluded := nodeExclusions[a.Type]; excluded {
			assertBodiesEqualExcluding(t, i, a.Type, a.Body, b.Body, excludedTopKey)
			continue
		}
		if string(a.Body) != string(b.Body) {
			t.Errorf("node %d (%s) body differs:\n v1=%s\n v2=%s", i, a.Type, a.Body, b.Body)
		}
	}
}

// assertBodiesEqualExcluding parses both bodies as JSON, deletes the
// named top-level key from each (a shallow delete — sufficient since both
// exclusions, datetime and process, are top-level keys in their
// respective node bodies), and compares what remains for deep equality.
func assertBodiesEqualExcluding(t *testing.T, i int, nodeType string, bodyA, bodyB []byte, key string) {
	t.Helper()
	var a, b map[string]any
	if err := json.Unmarshal(bodyA, &a); err != nil {
		t.Fatalf("node %d (%s) v1 body parse: %v (body: %s)", i, nodeType, err, bodyA)
	}
	if err := json.Unmarshal(bodyB, &b); err != nil {
		t.Fatalf("node %d (%s) v2 body parse: %v (body: %s)", i, nodeType, err, bodyB)
	}
	delete(a, key)
	delete(b, key)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("node %d (%s) body (minus %q) differs:\n v1=%+v\n v2=%+v", i, nodeType, key, a, b)
	}
}

func boolPtr(b bool) *bool { return &b }

package capturebatch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"testing"

	capture_plugin "code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

// recordingWriter is a fake capture_plugin.Writer: it parses every node it
// receives, records its type string in write order, and returns a
// deterministic fake digest so WriteReceipt can thread parent references.
// It lets the receipt assembly be asserted without a real blob store.
type recordingWriter struct {
	types []string
}

func (w *recordingWriter) WriteBlob(_ context.Context, r io.Reader) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	node, err := capture_plugin.ParseNode(bytes.NewReader(b))
	if err != nil {
		return "", 0, fmt.Errorf("parse node %d: %w", len(w.types), err)
	}
	w.types = append(w.types, node.Type)
	return fmt.Sprintf("blake2b256-fake%d", len(w.types)), int64(len(b)), nil
}

// TestBuildReceiptPostOrder pins the byte-stability-critical node order: the
// payload node is written first, then WriteReceipt emits the identity and
// outcome subtrees in post-order, receipt last. A change here means a
// receipt that no longer matches an in-process binding's tree.
func TestBuildReceiptPostOrder(t *testing.T) {
	w := &recordingWriter{}
	digest, err := buildReceipt(
		context.Background(),
		w,
		Resolved{Name: "doc", Format: "pdf", Normalize: true},
		firefox.BrowserInfo{Name: "firefox", Version: "120"},
		&firefox.HTTPResponse{
			Status:  200,
			Headers: []firefox.HTTPHeader{{Name: "Content-Type", Value: "application/pdf"}},
		},
		nil,
		bytes.NewReader([]byte("%PDF-1.7 sample")),
		"https://example.com",
		"0.0.0-test",
	)
	if err != nil {
		t.Fatalf("buildReceipt: %v", err)
	}

	want := []string{
		payloadType("pdf"), // payload, written before WriteReceipt
		capture_plugin.TypeInvocation,
		capture_plugin.TypeHost,
		capture_plugin.TypeBinary,
		environmentType, // chrest plugin-environment
		capture_plugin.TypeEnvironment,
		outcomeType, // chrest plugin-outcome (HTTP response present)
		capture_plugin.TypeOutcome,
		capture_plugin.TypeIdentity,
		capture_plugin.ReceiptType("web"), // receipt, written last
	}
	if !slices.Equal(w.types, want) {
		t.Fatalf("post-order node types:\n got %v\nwant %v", w.types, want)
	}

	if want[len(want)-1] != "cutting_garden-capture-receipt-web-v1" {
		t.Errorf("web receipt type = %q, want hyphenated frozen-kind form", want[len(want)-1])
	}
	if digest != fmt.Sprintf("blake2b256-fake%d", len(want)) {
		t.Errorf("returned digest = %q, want the last-written (receipt) node's digest", digest)
	}
}

// TestBuildReceiptEmitsPreviewOutcomeWithoutHTTP pins chrest#102 (RFC 0003
// v2): with no HTTP response observed but a command line available — the
// realistic case, since chrest always has one whenever a browser session
// opened — the plugin-outcome node is still emitted (carrying
// process.command_line), typed as the preview variant, not omitted. Also
// checks the payload type maps hyphens to underscores.
func TestBuildReceiptEmitsPreviewOutcomeWithoutHTTP(t *testing.T) {
	w := &recordingWriter{}
	if _, err := buildReceipt(
		context.Background(), w,
		Resolved{Name: "md", Format: "markdown-reader"},
		firefox.BrowserInfo{Name: "firefox", CommandLine: []string{"firefox", "--headless"}},
		nil, // no HTTP response observed
		nil,
		bytes.NewReader([]byte("# hi")),
		"https://example.com", "0.0.0-test",
	); err != nil {
		t.Fatalf("buildReceipt: %v", err)
	}

	if slices.Contains(w.types, outcomeType) {
		t.Errorf("plugin-outcome node should use the preview type (no http), not %q: %v", outcomeType, w.types)
	}
	if !slices.Contains(w.types, outcomeTypePreview) {
		t.Errorf("plugin-outcome node %q missing (command_line was available): %v", outcomeTypePreview, w.types)
	}
	if w.types[0] != "chrest-capture-payload-markdown_reader-v1" {
		t.Errorf("payload type = %q, want hyphen->underscore mapping", w.types[0])
	}
}

// TestBuildReceiptOmitsPluginOutcomeWithNeitherHTTPNorCommandLine covers
// the true omission case: no HTTP response AND no command line gathered
// (BrowserInfo{}'s zero value — chrest doesn't hit this in practice, since
// a session is always open by the time buildReceipt runs, but the
// omission path itself is still real behavior worth pinning).
func TestBuildReceiptOmitsPluginOutcomeWithNeitherHTTPNorCommandLine(t *testing.T) {
	w := &recordingWriter{}
	if _, err := buildReceipt(
		context.Background(), w,
		Resolved{Name: "md", Format: "markdown-reader"},
		firefox.BrowserInfo{Name: "firefox"},
		nil, nil,
		bytes.NewReader([]byte("# hi")),
		"https://example.com", "0.0.0-test",
	); err != nil {
		t.Fatalf("buildReceipt: %v", err)
	}

	if slices.Contains(w.types, outcomeType) || slices.Contains(w.types, outcomeTypePreview) {
		t.Errorf("plugin-outcome node should be fully omitted when neither http nor command_line is available: %v", w.types)
	}
}

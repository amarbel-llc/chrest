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

// TestBuildReceiptOmitsPluginOutcomeWithoutHTTP asserts the plugin-outcome
// node is skipped when no HTTP response was observed (the shared outcome
// node still carries datetime), and that the payload type maps hyphens to
// underscores.
func TestBuildReceiptOmitsPluginOutcomeWithoutHTTP(t *testing.T) {
	w := &recordingWriter{}
	if _, err := buildReceipt(
		context.Background(), w,
		Resolved{Name: "md", Format: "markdown-reader"},
		firefox.BrowserInfo{Name: "firefox"},
		nil, // no HTTP response observed
		nil,
		bytes.NewReader([]byte("# hi")),
		"https://example.com", "0.0.0-test",
	); err != nil {
		t.Fatalf("buildReceipt: %v", err)
	}

	if slices.Contains(w.types, outcomeType) {
		t.Errorf("plugin-outcome node %q should be omitted when no HTTP response observed", outcomeType)
	}
	if w.types[0] != "chrest-capture-payload-markdown_reader-v1" {
		t.Errorf("payload type = %q, want hyphen->underscore mapping", w.types[0])
	}
}

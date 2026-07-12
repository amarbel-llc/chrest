package capturebatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	capture_plugin "github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

// cmdWriter adapts the orchestrator-supplied writer.cmd subprocess to the
// capture_plugin.Writer sink: one writer.cmd invocation per node blob
// (WriteThrough). Used by the v1 capture-batch transport only; the v2
// capture-serve transport gets a ready-made capture_plugin.Writer from
// cutting-garden's Serve instead (its blobProtocolWriter realizes the
// same interface over the RFC 0008 blob protocol).
type cmdWriter struct {
	cmd []string
}

func newCmdWriter(cmd []string) *cmdWriter {
	return &cmdWriter{cmd: cmd}
}

// WriteBlob satisfies capture_plugin.Writer.
func (w *cmdWriter) WriteBlob(ctx context.Context, r io.Reader) (string, int64, error) {
	res, err := WriteThrough(ctx, w.cmd, r)
	if err != nil {
		return "", 0, err
	}
	return res.ID, res.Size, nil
}

// sizeTrackingWriter wraps any capture_plugin.Writer and remembers each
// written blob's size for post-hoc lookup by digest. WriteReceipt returns
// only the root receipt's digest, not its size, so runOneWithWriter looks
// the size up here after the tree is built. Shared by both transports:
// v1 wraps a cmdWriter, v2 wraps the capture_plugin.Writer Serve hands
// the batch handler.
type sizeTrackingWriter struct {
	inner capture_plugin.Writer
	sizes map[string]int64
}

func newSizeTrackingWriter(inner capture_plugin.Writer) *sizeTrackingWriter {
	return &sizeTrackingWriter{inner: inner, sizes: make(map[string]int64)}
}

// WriteBlob satisfies capture_plugin.Writer.
func (w *sizeTrackingWriter) WriteBlob(ctx context.Context, r io.Reader) (string, int64, error) {
	id, size, err := w.inner.WriteBlob(ctx, r)
	if err != nil {
		return "", 0, err
	}
	w.sizes[id] = size
	return id, size, nil
}

func (w *sizeTrackingWriter) sizeOf(digest string) int64 { return w.sizes[digest] }

// buildReceipt assembles one capture's RFC 0002+0003 receipt merkle tree
// via the shared cutting-garden builder and returns the root receipt's
// markl digest.
//
// The payload node is written first — its bytes wrapped in the hyphence
// node framing and streamed through the writer — then WriteReceipt emits
// the identity and outcome subtrees in post-order (every child before its
// parent) and returns the receipt digest. The tree is byte-identical to
// one an in-process binding would produce; chrest only supplies the
// plugin-namespaced environment + outcome bodies and the payload bytes.
//
// It takes the narrow capture_plugin.Writer interface (not the concrete
// cmdWriter) so the assembly is unit-testable with a recording fake; the
// caller looks up the receipt's size from its concrete writer.
func buildReceipt(
	ctx context.Context,
	w capture_plugin.Writer,
	r Resolved,
	browser firefox.BrowserInfo,
	httpResp *firefox.HTTPResponse,
	stripped map[string]any,
	payload io.Reader,
	target, version string,
) (string, error) {
	payloadBytes, err := io.ReadAll(payload)
	if err != nil {
		return "", fmt.Errorf("read payload: %w", err)
	}

	pType := payloadType(r.Format)
	payloadDigest, _, err := w.WriteBlob(ctx,
		bytes.NewReader(capture_plugin.BuildNode(pType, nil, payloadBytes)))
	if err != nil {
		return "", fmt.Errorf("write payload node: %w", err)
	}

	params := capture_plugin.ReceiptParams{
		Kind: captureKind,
		Invocation: capture_plugin.Invocation{
			Target:    target,
			Format:    r.Format,
			Normalize: r.Normalize,
			Options:   invocationOptions(r.Options),
		},
		Host: capture_plugin.GatherHost(),
		Binary: capture_plugin.BinaryInfo{
			Name:    CapturerName,
			Version: version,
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: environmentType,
			Body:       environmentBody(r, browser),
		},
		PayloadRefs: []capture_plugin.Ref{
			capture_plugin.LockedRef("payload", payloadDigest, pType),
		},
	}
	if body := outcomeHTTPBody(httpResp, target); body != nil {
		params.OutcomePlugin = &capture_plugin.PluginEnv{
			TypeString: outcomeType,
			Body:       body,
		}
	}
	if len(stripped) > 0 {
		params.OutcomeStripped = stripped
	}

	receiptDigest, err := capture_plugin.WriteReceipt(ctx, w, params)
	if err != nil {
		return "", fmt.Errorf("write receipt: %w", err)
	}

	return receiptDigest, nil
}

// invocationOptions parses the raw capture options into the JCS-serializable
// map echoed into the invocation node. RFC 0002 requires an object body, so
// absent options become `{}` (never nil) for byte-stability.
func invocationOptions(raw json.RawMessage) map[string]any {
	opts := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &opts)
	}
	return opts
}

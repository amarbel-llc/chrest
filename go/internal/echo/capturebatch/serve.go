package capturebatch

import (
	"context"
	"encoding/json"
	"fmt"

	capture_plugin "code.linenisgreat.com/cutting-garden/pkgs/capture_plugin"
	capture_serve "code.linenisgreat.com/cutting-garden/pkgs/capture_serve"
)

// NewBatchHandler adapts chrest's receipt-building runner to cutting-garden's
// RFC 0008 capture_serve.BatchFunc — the plugin-side capture.batch handler
// Serve calls once per batch received over the JSON-RPC control connection.
// The receipt-assembly path (runOneWithWriter) is identical to the v1
// capture-batch command; only the wire types differ: capture_serve.BatchParams
// carries no writer.cmd (w already realizes capture_plugin.Writer over the
// RFC 0008 blob protocol — see server.go's blobProtocolWriter upstream), and
// per-capture options arrive pre-parsed as map[string]any rather than raw
// JSON, so they're re-marshaled at the edge to reach the shared Resolved
// type unchanged.
func NewBatchHandler(capturerVersion string) capture_serve.BatchFunc {
	return func(
		ctx context.Context, params capture_serve.BatchParams, w capture_plugin.Writer,
	) (capture_serve.BatchResult, error) {
		if params.Schema != capture_serve.SchemaV2 {
			return capture_serve.BatchResult{}, fmt.Errorf(
				"batch schema must be %q, got %q", capture_serve.SchemaV2, params.Schema,
			)
		}
		if params.Target == "" {
			return capture_serve.BatchResult{}, fmt.Errorf("target MUST be a non-empty string")
		}

		sw := newSizeTrackingWriter(w)
		defaults := convertBatchDefaults(params.Defaults)

		out := capture_serve.BatchResult{
			Schema:   capture_serve.SchemaV2,
			Plugin:   capture_serve.PluginInfo{Name: CapturerName, Version: capturerVersion},
			Errors:   []capture_serve.ProtocolError{},
			Captures: make([]capture_serve.CaptureResult, 0, len(params.Captures)),
		}

		for _, c := range params.Captures {
			resolved := Resolve(convertCaptureSpec(c), defaults)
			result := runOneWithWriter(ctx, resolved, params.Target, capturerVersion, sw)
			out.Captures = append(out.Captures, convertCaptureResult(result))
		}
		return out, nil
	}
}

func convertBatchDefaults(d *capture_serve.BatchDefaults) *Defaults {
	if d == nil {
		return nil
	}
	return &Defaults{Normalize: d.Normalize, Plugin: d.Plugin}
}

// convertCaptureSpec re-marshals the pre-parsed v2 options back into raw
// JSON so it reaches Resolve/Resolved.Options in the same shape the v1
// stdin path produces — the shared runner never needs to know which
// transport it came from.
func convertCaptureSpec(c capture_serve.CaptureSpec) CaptureSpec {
	spec := CaptureSpec{Name: c.Name, Format: c.Format}
	if len(c.Options) > 0 {
		if raw, err := json.Marshal(c.Options); err == nil {
			spec.Options = raw
		}
	}
	return spec
}

func convertCaptureResult(r CaptureResult) capture_serve.CaptureResult {
	out := capture_serve.CaptureResult{Name: r.Name}
	if r.Receipt != nil {
		out.Receipt = &capture_serve.ReceiptRef{ID: r.Receipt.ID, Size: r.Receipt.Size}
	}
	if r.Error != nil {
		out.Error = &capture_serve.ProtocolError{Kind: r.Error.Kind, Message: r.Error.Message}
	}
	return out
}

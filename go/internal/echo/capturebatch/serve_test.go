package capturebatch

import (
	"bytes"
	"context"
	"testing"

	capture_serve "github.com/amarbel-llc/cutting-garden/pkgs/capture_serve"
)

// TestNewBatchHandlerRejectsWrongSchema and TestNewBatchHandlerRejectsEmptyTarget
// cover the two validation branches that return before any capture runs (and
// so before a browser session is ever opened) — these don't need firefox,
// unlike the full receipt-assembly path exercised by capture_serve.bats.

func TestNewBatchHandlerRejectsWrongSchema(t *testing.T) {
	handler := NewBatchHandler("0.0.0-test")
	if _, err := handler(context.Background(), capture_serve.BatchParams{
		Schema: "capture-plugin/v1", // v1 token on the v2 transport
		Target: "https://example.com",
	}, &recordingWriter{}); err == nil {
		t.Fatal("expected an error for a v1 schema token on capture.batch")
	}
}

func TestNewBatchHandlerRejectsEmptyTarget(t *testing.T) {
	handler := NewBatchHandler("0.0.0-test")
	if _, err := handler(context.Background(), capture_serve.BatchParams{
		Schema: capture_serve.SchemaV2,
	}, &recordingWriter{}); err == nil {
		t.Fatal("expected an error for an empty target")
	}
}

func TestConvertCaptureSpecRoundTripsOptions(t *testing.T) {
	spec := convertCaptureSpec(capture_serve.CaptureSpec{
		Name:    "cap",
		Format:  "markdown-selector",
		Options: map[string]any{"selector": "h1"},
	})
	if spec.Name != "cap" || spec.Format != "markdown-selector" {
		t.Fatalf("convertCaptureSpec name/format = %q/%q", spec.Name, spec.Format)
	}
	if !bytes.Contains(spec.Options, []byte(`"selector":"h1"`)) {
		t.Errorf("options = %s, want selector echoed", spec.Options)
	}
}

func TestConvertCaptureSpecOmitsOptionsWhenAbsent(t *testing.T) {
	spec := convertCaptureSpec(capture_serve.CaptureSpec{Name: "cap", Format: "text"})
	if len(spec.Options) != 0 {
		t.Errorf("options = %s, want empty for a spec with no options", spec.Options)
	}
}

func TestConvertBatchDefaultsNilPassesThrough(t *testing.T) {
	if convertBatchDefaults(nil) != nil {
		t.Fatal("nil BatchDefaults should convert to nil Defaults")
	}

	normalize := true
	d := convertBatchDefaults(&capture_serve.BatchDefaults{
		Normalize: &normalize,
		Plugin:    map[string]any{"browser": "firefox"},
	})
	if d == nil || d.Normalize == nil || !*d.Normalize {
		t.Fatalf("Normalize did not round-trip: %+v", d)
	}
	if d.Plugin["browser"] != "firefox" {
		t.Errorf("Plugin map did not round-trip: %+v", d.Plugin)
	}
}

func TestConvertCaptureResultRoundTrips(t *testing.T) {
	receiptOnly := convertCaptureResult(CaptureResult{
		Name:    "cap",
		Receipt: &ReceiptRef{ID: "blake2b256-x", Size: 42},
	})
	if receiptOnly.Receipt == nil || receiptOnly.Receipt.ID != "blake2b256-x" || receiptOnly.Receipt.Size != 42 {
		t.Fatalf("receipt did not round-trip: %+v", receiptOnly.Receipt)
	}
	if receiptOnly.Error != nil {
		t.Errorf("unexpected error on a receipt-only result: %+v", receiptOnly.Error)
	}

	errOnly := convertCaptureResult(CaptureResult{
		Name:  "cap",
		Error: &ProtocolError{Kind: "bad-format", Message: "unknown capture format"},
	})
	if errOnly.Error == nil || errOnly.Error.Kind != "bad-format" {
		t.Fatalf("error did not round-trip: %+v", errOnly.Error)
	}
	if errOnly.Receipt != nil {
		t.Errorf("unexpected receipt on an error-only result: %+v", errOnly.Receipt)
	}
}

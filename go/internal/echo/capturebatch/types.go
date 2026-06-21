// Package capturebatch implements the chrest side of the cutting-garden
// Capture Plugin Protocol (RFC 0002) under the web-archive binding (RFC
// 0003). The `capture-batch` subcommand reads a batch of capture requests
// as JSON on stdin, runs them sequentially, assembles each capture's
// receipt merkle tree of typed hyphence blobs via the shared
// cutting-garden capture_plugin builder, streams every node through the
// orchestrator-supplied writer.cmd subprocess, and emits one receipt ref
// per capture in a JSON result envelope on stdout.
package capturebatch

import "encoding/json"

// BatchSchema is the wire schema token for both the batch input and the
// batch output (cutting-garden capture-plugin/v1).
const BatchSchema = "capture-plugin/v1"

// CapturerName is chrest's plugin identifier — the `environment.binary.name`
// discriminator that identifies chrest as the binary that produced a
// receipt's bytes (RFC 0002 §Environment).
const CapturerName = "chrest"

// BatchInput is the single JSON document read from stdin.
type BatchInput struct {
	Schema   string        `json:"schema"`
	Writer   WriterSpec    `json:"writer"`
	Target   string        `json:"target"`
	Defaults *Defaults     `json:"defaults,omitempty"`
	Captures []CaptureSpec `json:"captures"`
}

// WriterSpec is the writer-command contract from the orchestrator: each
// node blob is streamed through one invocation of this argv, which prints
// a single `{id, size}` JSON object for the content-addressed blob.
type WriterSpec struct {
	Cmd []string `json:"cmd"`
}

// Defaults carries batch-level fields applied to every capture. `plugin`
// is the plugin-namespaced defaults object; chrest reads `plugin.browser`.
type Defaults struct {
	Normalize *bool          `json:"normalize,omitempty"`
	Plugin    map[string]any `json:"plugin,omitempty"`
}

// CaptureSpec is one entry in the batch input `captures` array. Options
// stay raw so the format dispatcher and the invocation echo each parse
// them independently.
type CaptureSpec struct {
	Name    string          `json:"name"`
	Format  string          `json:"format"`
	Options json.RawMessage `json:"options,omitempty"`
}

// Extension is a browser extension echoed into the plugin-environment
// node. The orchestrator does not yet send extensions on the wire (the
// CaptureSpec has no extensions field); this type backs the
// preinstalled-extension mapping until fetched-extension support (#55).
type Extension struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
}

// Resolved is a capture after batch defaults have been applied.
type Resolved struct {
	Name       string
	Format     string
	Options    json.RawMessage
	Browser    string
	Normalize  bool
	Isolation  string
	Extensions []Extension
}

// BatchOutput is the single JSON document written to stdout.
type BatchOutput struct {
	Schema   string          `json:"schema"`
	Plugin   PluginInfo      `json:"plugin"`
	Errors   []ProtocolError `json:"errors"`
	Captures []CaptureResult `json:"captures"`
}

// PluginInfo identifies the capture plugin + version.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// CaptureResult is one entry in the batch output `captures` array.
// Exactly one of Receipt or Error is set.
type CaptureResult struct {
	Name    string         `json:"name"`
	Receipt *ReceiptRef    `json:"receipt,omitempty"`
	Error   *ProtocolError `json:"error,omitempty"`
}

// ReceiptRef points to a capture's root receipt blob by its markl id.
type ReceiptRef struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

// ProtocolError is a batch-level (errors[]) or per-capture
// (captures[].error) failure.
type ProtocolError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Resolve applies batch defaults to a single capture spec. Browser comes
// from `defaults.plugin.browser` (default firefox, the only backend);
// normalize comes from `defaults.normalize`.
func Resolve(c CaptureSpec, def *Defaults) Resolved {
	r := Resolved{
		Name:    c.Name,
		Format:  c.Format,
		Options: c.Options,
	}
	if def != nil {
		if b, ok := def.Plugin["browser"].(string); ok {
			r.Browser = b
		}
		if def.Normalize != nil {
			r.Normalize = *def.Normalize
		}
	}
	if r.Browser == "" {
		r.Browser = "firefox"
	}
	return r
}

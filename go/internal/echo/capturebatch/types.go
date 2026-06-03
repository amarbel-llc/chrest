// Package capturebatch implements the chrest side of the Capture Plugin
// Protocol (cutting-garden [RFC 0002]) under the web-archive binding
// ([RFC 0003]). chrest is the reference `web`-kind capturer: it reads a
// batch of capture requests as JSON on stdin (schema capture-plugin/v1),
// runs them sequentially, and for each one assembles an RFC 0002 receipt
// merkle tree — streaming every node blob to a writer subprocess — then
// emits a result envelope on stdout referencing each receipt by markl id.
//
// The tree is built with cutting-garden's exported pkgs/capture_plugin
// (WriteReceipt, BuildNode, the type registry, JCS), so a chrest receipt
// is byte-identical to one an in-process cutting-garden binding would
// emit. The web-binding-specific nodes (plugin environment, plugin
// outcome, payload, capabilities) live in web_nodes.go / types_register.go.
//
// [RFC 0002]: https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0002-capture-plugin-protocol.md
// [RFC 0003]: https://github.com/amarbel-llc/cutting-garden/blob/master/docs/rfcs/0003-web-archive-binding.md
package capturebatch

import "encoding/json"

const (
	// InputSchema / OutputSchema are the capture-plugin/v1 batch tokens.
	InputSchema  = "capture-plugin/v1"
	OutputSchema = "capture-plugin/v1"

	// CapturerName is chrest's identifier — plugin.name in the output and
	// environment.binary.name in every receipt.
	CapturerName = "chrest"

	// captureKind is the receipt kind: cutting_garden-capture-receipt-web-v1.
	captureKind = "web"
)

// Input is the single capture-plugin/v1 JSON document read from stdin.
type Input struct {
	Schema   string         `json:"schema"`
	Writer   WriterSpec     `json:"writer"`
	Target   string         `json:"target"`
	Defaults *Defaults      `json:"defaults,omitempty"`
	Captures []InputCapture `json:"captures"`
}

// WriterSpec is the RFC 0002 writer-protocol command: chrest pipes every
// node blob's bytes to this argv's stdin and reads back {"id","size"}.
type WriterSpec struct {
	Cmd []string `json:"cmd"`
}

// Defaults are batch-level fallbacks. Plugin is the plugin-namespaced
// default object (browser, isolation, …).
type Defaults struct {
	Normalize *bool          `json:"normalize,omitempty"`
	Plugin    map[string]any `json:"plugin,omitempty"`
}

// InputCapture is one entry in the batch `captures` array.
type InputCapture struct {
	Name      string          `json:"name"`
	Format    string          `json:"format"`
	Options   json.RawMessage `json:"options,omitempty"`
	Normalize *bool           `json:"normalize,omitempty"`
	Plugin    map[string]any  `json:"plugin,omitempty"`
}

// Resolved is a capture after defaults have been folded in.
type Resolved struct {
	Name      string
	Format    string
	Options   json.RawMessage
	Normalize bool
	Browser   string
	Isolation string
}

// Output is the capture-plugin/v1 result written to stdout.
type Output struct {
	Schema   string          `json:"schema"`
	Plugin   PluginInfo      `json:"plugin"`
	Errors   []Error         `json:"errors"`
	Captures []OutputCapture `json:"captures"`
}

// PluginInfo identifies the capturer implementation + version.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// OutputCapture is one entry in the output `captures` array. Exactly one
// of Receipt or Error is set.
type OutputCapture struct {
	Name    string        `json:"name"`
	Receipt *ReceiptRef   `json:"receipt,omitempty"`
	Error   *CaptureError `json:"error,omitempty"`
}

// ReceiptRef points to the root receipt blob by its markl id.
type ReceiptRef struct {
	ID   string `json:"id"`
	Size int64  `json:"size"`
}

// Error is a batch-level error (e.g. malformed input).
type Error struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// CaptureError is a per-capture error embedded in OutputCapture.
type CaptureError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Resolve folds batch-level defaults into one capture. Per RFC 0002
// §Batch Input, normalize defaults to true when omitted at both levels.
// Plugin-namespaced keys (browser, isolation) merge default-then-override.
func Resolve(in InputCapture, def *Defaults) Resolved {
	r := Resolved{
		Name:      in.Name,
		Format:    in.Format,
		Options:   in.Options,
		Normalize: true,
	}

	plugin := map[string]any{}
	if def != nil {
		for k, v := range def.Plugin {
			plugin[k] = v
		}
	}
	for k, v := range in.Plugin {
		plugin[k] = v
	}
	if s, ok := plugin["browser"].(string); ok {
		r.Browser = s
	}
	if s, ok := plugin["isolation"].(string); ok {
		r.Isolation = s
	}

	if def != nil && def.Normalize != nil {
		r.Normalize = *def.Normalize
	}
	if in.Normalize != nil {
		r.Normalize = *in.Normalize
	}
	return r
}

// Package capturebatch implements the chrest side of the Web Capture
// Archive Protocol (RFC 0001). The capturer reads a batch of capture
// requests as JSON on stdin, runs them sequentially, streams each
// artifact to a writer subprocess for content-addressed storage, and
// emits a JSON result envelope on stdout.
//
// MVP scope: split=false only. For split=true, the runner emits a
// per-capture not-implemented error.
package capturebatch

import "encoding/json"

// InputSchema is the constant `schema` value for the batch input.
const InputSchema = "web-capture-archive/v1"

// OutputSchema is the constant `schema` value for the batch output.
const OutputSchema = "web-capture-archive/v1"

// CapturerName is chrest's identifier in the protocol. Hardcoded so
// other capturers implementing RFC 0001 can be distinguished.
const CapturerName = "chrest"

// Input is the single JSON document read from stdin.
type Input struct {
	Schema   string           `json:"schema"`
	Writer   WriterSpec       `json:"writer"`
	URL      string           `json:"url"`
	Defaults *CaptureDefaults `json:"defaults,omitempty"`
	Captures []InputCapture   `json:"captures"`
}

// WriterSpec is the writer-command contract from the orchestrator.
type WriterSpec struct {
	Cmd []string `json:"cmd"`
}

// CaptureDefaults are applied to any fields a given capture leaves
// unset. RFC 0001 §Capturer Protocol.
type CaptureDefaults struct {
	Browser   string `json:"browser,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	Split     *bool  `json:"split,omitempty"`
}

// InputCapture is one entry in the batch input `captures` array.
type InputCapture struct {
	Name       string          `json:"name"`
	Format     string          `json:"format"`
	Options    json.RawMessage `json:"options,omitempty"`
	Browser    string          `json:"browser,omitempty"`
	Isolation  string          `json:"isolation,omitempty"`
	Split      *bool           `json:"split,omitempty"`
	Extensions []Extension     `json:"extensions,omitempty"`
}

// Extension is a loaded browser extension declared in the batch input
// or echoed in the spec artifact.
type Extension struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	ManifestDigest string `json:"manifest_digest,omitempty"`
}

// Resolved is a capture after defaults have been applied.
type Resolved struct {
	Name       string
	Format     string
	Options    json.RawMessage
	Browser    string
	Isolation  string
	Split      bool
	Extensions []Extension
}

// Output is the single JSON document written to stdout.
type Output struct {
	Schema   string          `json:"schema"`
	Capturer CapturerInfo    `json:"capturer"`
	Errors   []Error         `json:"errors"`
	Captures []OutputCapture `json:"captures"`
}

// CapturerInfo identifies the capturer implementation + version.
type CapturerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// OutputCapture is one entry in the batch output `captures` array.
// Exactly one of `Error` or the artifact refs is set.
type OutputCapture struct {
	Name     string        `json:"name"`
	Spec     *ArtifactRef  `json:"spec,omitempty"`
	Payload  *ArtifactRef  `json:"payload,omitempty"`
	Envelope *ArtifactRef  `json:"envelope,omitempty"`
	Error    *CaptureError `json:"error,omitempty"`
}

// ArtifactRef points to a content-addressed blob via its markl ID.
type ArtifactRef struct {
	ID         string `json:"id"`
	Size       int64  `json:"size"`
	MediaType  string `json:"media_type"`
	Normalized *bool  `json:"normalized,omitempty"`
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

// Resolve applies batch-level defaults to a single input capture and
// produces the final tuple used by the runner.
func Resolve(in InputCapture, def *CaptureDefaults) Resolved {
	r := Resolved{
		Name:       in.Name,
		Format:     in.Format,
		Options:    in.Options,
		Browser:    in.Browser,
		Isolation:  in.Isolation,
		Extensions: in.Extensions,
	}

	if def != nil {
		if r.Browser == "" {
			r.Browser = def.Browser
		}
		if r.Isolation == "" {
			r.Isolation = def.Isolation
		}
	}
	switch {
	case in.Split != nil:
		r.Split = *in.Split
	case def != nil && def.Split != nil:
		r.Split = *def.Split
	}
	return r
}

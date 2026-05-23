package capturebatch

import (
	"encoding/json"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

// SpecSchema is the `schema` constant in the spec artifact.
const SpecSchema = "web-capture-archive.spec/v1"

// SpecMediaType is the Content-Type of the canonicalized spec bytes.
const SpecMediaType = "application/vnd.web-capture-archive.spec+json"

// BuildSpec assembles the spec artifact for a resolved capture and
// returns the JCS-canonicalized bytes.
//
// Per RFC 0001 §Capture Spec Artifact:
//   - `capture.options` is an echo of the input (may be any JSON value);
//     empty object `{}` if input omitted it.
//   - `browser.command_line`, `browser.prefs`, `browser.extensions[].manifest_digest`
//     are optional; omitted when empty (vs present-and-empty).
//   - `browser.extensions` is required; must be `[]` if none.
//   - MUST NOT contain time-varying data.
func BuildSpec(
	r Resolved,
	browser firefox.BrowserInfo,
	host HostFingerprint,
	capturerVersion string,
) ([]byte, error) {
	var options any
	if len(r.Options) > 0 {
		if err := json.Unmarshal(r.Options, &options); err != nil {
			return nil, err
		}
	} else {
		options = map[string]any{}
	}

	capture := map[string]any{
		"format":  r.Format,
		"options": options,
		"split":   r.Split,
	}
	// `isolation` is optional — omit when unset rather than emitting
	// `""`, which would hash differently from the same capture with
	// the key absent and give consumers a value that isn't really
	// there (see #28).
	if r.Isolation != "" {
		capture["isolation"] = r.Isolation
	}

	browserObj := map[string]any{
		"name":       browser.Name,
		"version":    browser.Version,
		"user_agent": browser.UserAgent,
		"platform":   browser.Platform,
		"extensions": extensionsToJSON(r.Extensions),
	}
	if browser.JSEngine != "" {
		browserObj["js_engine"] = browser.JSEngine
	}
	if len(browser.CommandLine) > 0 {
		browserObj["command_line"] = stringsToAny(browser.CommandLine)
	}
	// browser.prefs intentionally omitted in MVP. Per bus coordination:
	// omitting ≠ {}; {} would mean "gathered, nothing rendering-
	// relevant," while omission means "not gathered."

	doc := map[string]any{
		"schema":  SpecSchema,
		"capture": capture,
		"browser": browserObj,
		"host":    host.ToJSON(),
		"capturer": map[string]any{
			"name":    CapturerName,
			"version": capturerVersion,
		},
	}

	return Canonicalize(doc)
}

func extensionsToJSON(exts []Extension) []any {
	out := make([]any, 0, len(exts))
	for _, e := range exts {
		obj := map[string]any{
			"id":      e.ID,
			"version": e.Version,
		}
		if e.ManifestDigest != "" {
			obj["manifest_digest"] = e.ManifestDigest
		}
		out = append(out, obj)
	}
	return out
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

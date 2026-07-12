package capturebatch

import (
	"strings"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

// This file builds the chrest-owned bodies of the two plugin-namespaced
// nodes in a cutting-garden capture receipt (RFC 0003 web-archive
// binding): the plugin-environment node (`!jcs-chrest-capture-
// environment-v2`) and the plugin-outcome node (`!jcs-chrest-capture-
// outcome-v2` / `-v2-preview`). chrest is the sole producer of both type
// strings, so the shapes here are authoritative — there is no other
// binding to be byte-identical to (receipt *framing* byte-identity is
// handled by the shared capture_plugin.WriteReceipt, not by these
// bodies).
//
// These helpers are pure (map[string]any in, map[string]any out) and
// carry no cutting-garden import, so they are unit-testable without the
// shared receipt-builder dependency.

const (
	// captureKind is the web-archive binding's capture kind (RFC 0003).
	// It selects the receipt type string cutting_garden-capture-receipt-
	// web-v1 inside the shared WriteReceipt.
	captureKind = "web"

	// environmentType / outcomeType / outcomeTypePreview are the type
	// strings of chrest's plugin-namespaced environment and outcome
	// nodes. Bumped to v2 (chrest#102, RFC 0003 commit a6548e5):
	// browser.command_line moved out of the identity-affecting
	// environment node into the outcome node's process.command_line —
	// it's a per-run observation (argv as observed, not an identity
	// claim), not config, and embedding a randomly-generated Firefox
	// profile path in an "identity-affecting" field meant no two
	// captures of the same page ever shared an identity markl-id.
	//
	// outcomeType vs outcomeTypePreview is keyed on http.* completeness
	// alone (RFC 0003 §Preview Schema, as revised): process.command_line
	// presence neither requires nor lifts the preview marker — a preview
	// node still SHOULD carry it.
	environmentType    = "jcs-chrest-capture-environment-v2"
	outcomeType        = "jcs-chrest-capture-outcome-v2"
	outcomeTypePreview = "jcs-chrest-capture-outcome-v2-preview"
)

// payloadType returns the typed-blob type string for a capture payload of
// the given batch-input format. RFC 0003 maps the format string to the
// type segment by replacing hyphens with underscores (the type tag MUST
// NOT contain a hyphen in the segment; the batch input MUST keep it):
// `markdown-reader` -> `chrest-capture-payload-markdown_reader-v1`.
func payloadType(format string) string {
	return "chrest-capture-payload-" + strings.ReplaceAll(format, "-", "_") + "-v1"
}

// environmentBody builds the `!jcs-chrest-capture-environment-v2` node
// body: the identity-affecting browser configuration that was actually
// applied for the capture (RFC 0003 §Identity-Affecting Fields).
//
// `command_line` deliberately does NOT appear here (chrest#102, v2):
// it's a per-run observation of the launched process, not stable
// config, and lives in the outcome node's `process.command_line`
// instead — see outcomeBody.
//
// `extensions` is always present (MUST be `[]` when none, never omitted).
// `isolation` is required and MUST be one of fresh/session/shared; chrest
// opens a fresh Firefox session per capture (see runner openSession), so
// an unset value resolves to "fresh". `dns` is omitted until chrest
// honors it (#56); `browser.prefs` is omitted until gathered (omission
// means "not gathered", distinct from an empty object).
func environmentBody(r Resolved, b firefox.BrowserInfo) map[string]any {
	browser := map[string]any{
		"name":       b.Name,
		"version":    b.Version,
		"user_agent": b.UserAgent,
		"platform":   b.Platform,
	}
	if b.JSEngine != "" {
		browser["js_engine"] = b.JSEngine
	}

	isolation := r.Isolation
	if isolation == "" {
		isolation = "fresh"
	}

	return map[string]any{
		"browser":    browser,
		"extensions": preinstalledExtensions(r.Extensions),
		"isolation":  isolation,
	}
}

// preinstalledExtensions renders the requested extensions under the RFC
// 0003 `source: "preinstalled"` discriminator. Fetched-extension support
// (`source: "fetched"`) is #55.
func preinstalledExtensions(exts []Extension) []any {
	out := make([]any, 0, len(exts))
	for _, e := range exts {
		obj := map[string]any{
			"source":  "preinstalled",
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

// outcomeBody builds the `!jcs-chrest-capture-outcome-v2` (or
// `-v2-preview`) node body: the per-run observations RFC 0003 §Outcome
// assigns to the outcome tree — `process.command_line` (chrest#102: the
// browser's launch argv as observed, not an identity claim; two captures
// of the same request legitimately differ here) plus `http.*` when an
// HTTP response was observed.
//
// The returned type string is keyed on `http.*` completeness alone
// (RFC 0003 §Preview Schema, as revised for chrest#102):
// `process.command_line` presence neither requires nor lifts the preview
// marker — a preview node still SHOULD carry it. Returns ("", nil) when
// there is nothing to report at all (no command line gathered and no
// HTTP response observed), so the caller omits the plugin-outcome node
// entirely rather than emitting an empty one.
//
// Header names are lowercased per RFC 0003 (order and duplicates
// preserved). `timing_ms` is the object form `{load: <int>}` — the
// BiDi backend cannot observe dns/tcp/tls/ttfb, and `resolved_ip` is
// omitted entirely (BiDi has no remote-IP field, #52).
func outcomeBody(resp *firefox.HTTPResponse, target string, commandLine []string) (map[string]any, string) {
	body := map[string]any{}
	if len(commandLine) > 0 {
		body["process"] = map[string]any{"command_line": stringsToAny(commandLine)}
	}

	typeString := outcomeTypePreview
	if resp != nil {
		typeString = outcomeType
		httpObj := map[string]any{
			"status":  resp.Status,
			"headers": headersToJSON(resp.Headers),
		}
		if resp.URL != "" && resp.URL != target {
			httpObj["final_url"] = resp.URL
		}
		if resp.TimingMs > 0 {
			httpObj["timing_ms"] = map[string]any{"load": resp.TimingMs}
		}
		body["http"] = httpObj
	}

	if len(body) == 0 {
		return nil, ""
	}
	return body, typeString
}

func headersToJSON(headers []firefox.HTTPHeader) []any {
	out := make([]any, 0, len(headers))
	for _, h := range headers {
		out = append(out, map[string]any{
			"name":  strings.ToLower(h.Name),
			"value": h.Value,
		})
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

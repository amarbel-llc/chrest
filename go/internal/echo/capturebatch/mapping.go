package capturebatch

import (
	"strings"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

// This file builds the chrest-owned bodies of the two plugin-namespaced
// nodes in a cutting-garden capture receipt (RFC 0003 web-archive
// binding): the plugin-environment node (`!jcs-chrest-capture-
// environment-v1`) and the plugin-outcome node (`!jcs-chrest-capture-
// outcome-v1`). chrest is the sole producer of both type strings, so the
// shapes here are authoritative — there is no other binding to be byte-
// identical to (receipt *framing* byte-identity is handled by the shared
// capture_plugin.WriteReceipt, not by these bodies).
//
// These helpers are pure (map[string]any in, map[string]any out) and
// carry no cutting-garden import, so they are unit-testable without the
// shared receipt-builder dependency.

const (
	// captureKind is the web-archive binding's capture kind (RFC 0003).
	// It selects the receipt type string cutting_garden-capture-receipt-
	// web-v1 inside the shared WriteReceipt.
	captureKind = "web"

	// environmentType / outcomeType are the type strings of chrest's
	// plugin-namespaced environment and outcome nodes. The Firefox/BiDi
	// backend always observes an HTTP response, so the outcome node uses
	// the full schema rather than the `-v1-preview` variant (#52).
	environmentType = "jcs-chrest-capture-environment-v1"
	outcomeType     = "jcs-chrest-capture-outcome-v1"
)

// payloadType returns the typed-blob type string for a capture payload of
// the given batch-input format. RFC 0003 maps the format string to the
// type segment by replacing hyphens with underscores (the type tag MUST
// NOT contain a hyphen in the segment; the batch input MUST keep it):
// `markdown-reader` -> `chrest-capture-payload-markdown_reader-v1`.
func payloadType(format string) string {
	return "chrest-capture-payload-" + strings.ReplaceAll(format, "-", "_") + "-v1"
}

// environmentBody builds the `!jcs-chrest-capture-environment-v1` node
// body: the identity-affecting browser configuration that was actually
// applied for the capture (RFC 0003 §Identity-Affecting Fields).
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
	if len(b.CommandLine) > 0 {
		browser["command_line"] = stringsToAny(b.CommandLine)
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

// outcomeHTTPBody builds the `!jcs-chrest-capture-outcome-v1` node body:
// the per-run HTTP observation under a top-level `http` object (RFC 0003
// §Outcome). Returns nil when no HTTP response was observed, so the
// caller can omit the plugin-outcome node entirely (the `-v1-preview`
// outcome schema for backends without `http.*` is #52 follow-up; the
// Firefox/BiDi backend always observes a response).
//
// Header names are lowercased per RFC 0003 (order and duplicates
// preserved). `timing_ms` is the object form `{load: <int>}` — the
// BiDi backend cannot observe dns/tcp/tls/ttfb, and `resolved_ip` is
// omitted entirely (BiDi has no remote-IP field, #52).
func outcomeHTTPBody(resp *firefox.HTTPResponse, target string) map[string]any {
	if resp == nil {
		return nil
	}

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

	return map[string]any{"http": httpObj}
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

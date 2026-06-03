package capturebatch

import (
	"sort"
	"strings"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
)

// payloadMediaTypes maps each RFC 0003 web format to its payload media
// type. Membership doubles as the format catalog (knownFormat).
var payloadMediaTypes = map[string]string{
	"text":              "text/plain; charset=utf-8",
	"pdf":               "application/pdf",
	"screenshot":        "image/png",
	"mhtml":             `multipart/related; type="text/html"`,
	"a11y":              "application/vnd.cutting-garden.a11y+json",
	"html-monolith":     "text/html; charset=utf-8",
	"html-outer":        "text/html; charset=utf-8",
	"markdown-full":     "text/markdown",
	"markdown-reader":   "text/markdown",
	"markdown-selector": "text/markdown",
}

// payloadType is the RFC 0003 payload node type-string. Format segments
// use underscores (markdown-reader → markdown_reader) per RFC 0003.
func payloadType(format string) string {
	return "chrest-capture-payload-" + strings.ReplaceAll(format, "-", "_") + "-v1"
}

func knownFormat(format string) bool {
	_, ok := payloadMediaTypes[format]
	return ok
}

// normalizeSupported reports whether a normalizer is implemented for the
// format (normalize.go dispatch). RFC 0003 defers normalization rules for
// the others; normalize=true on them yields a per-capture not-implemented.
func normalizeSupported(format string) bool {
	switch format {
	case "text", "pdf", "screenshot", "mhtml":
		return true
	default:
		return false
	}
}

// environmentBody builds the jcs-chrest-capture-environment-v1 body (RFC
// 0003 §Environment Plugin Node). Optional fields are omitted when not
// gathered rather than emitted empty — absence ≠ "{}", and a forged value
// would corrupt identity. dns / extensions / prefs are not gathered yet.
func environmentBody(b firefox.BrowserInfo, isolation string) map[string]any {
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

	env := map[string]any{"browser": browser}
	if isolation != "" {
		env["isolation"] = isolation
	}
	return env
}

// outcomePlugin builds the plugin-outcome node (RFC 0003 §Outcome Plugin
// Node) carrying http.* response metadata. When the backend couldn't
// observe the response (http == nil), it emits the -v1-preview type with
// an empty body so strict consumers reject it.
func outcomePlugin(target string, http *firefox.HTTPResponse) capture_plugin.PluginEnv {
	if http == nil {
		return capture_plugin.PluginEnv{
			TypeString: outcomeTypePreview,
			Body:       map[string]any{},
		}
	}

	// Header names lowercased; order + duplicates preserved (array form).
	headers := make([]any, 0, len(http.Headers))
	for _, h := range http.Headers {
		headers = append(headers, map[string]any{
			"name":  strings.ToLower(h.Name),
			"value": h.Value,
		})
	}

	httpObj := map[string]any{
		"status":  int64(http.Status),
		"headers": headers,
	}
	if http.URL != "" && http.URL != target {
		httpObj["final_url"] = http.URL
	}
	// timing_ms is the object form per RFC 0003; BiDi exposes only load.
	if http.TimingMs > 0 {
		httpObj["timing_ms"] = map[string]any{"load": http.TimingMs}
	}

	return capture_plugin.PluginEnv{
		TypeString: outcomeType,
		Body:       map[string]any{"http": httpObj},
	}
}

// capabilitiesBody describes what this chrest build can produce (RFC 0003
// §Capability Discovery). Referenced from environment.binary.capabilities_id.
func capabilitiesBody() map[string]any {
	formats := make([]any, 0, len(payloadMediaTypes))
	for f := range payloadMediaTypes {
		formats = append(formats, f)
	}
	sort.Slice(formats, func(i, j int) bool {
		return formats[i].(string) < formats[j].(string)
	})

	return map[string]any{
		"formats":           formats,
		"browsers":          []any{"firefox"},
		"normalizes":        []any{"mhtml", "pdf", "screenshot", "text"},
		"honors_dns":        false,
		"honors_extensions": false,
		"transport":         "bidi",
	}
}

func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

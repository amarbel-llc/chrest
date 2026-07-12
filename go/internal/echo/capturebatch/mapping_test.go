package capturebatch

import (
	"reflect"
	"testing"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

func TestPayloadType(t *testing.T) {
	cases := map[string]string{
		"pdf":               "chrest-capture-payload-pdf-v1",
		"text":              "chrest-capture-payload-text-v1",
		"screenshot":        "chrest-capture-payload-screenshot-v1",
		"markdown-reader":   "chrest-capture-payload-markdown_reader-v1",
		"markdown-selector": "chrest-capture-payload-markdown_selector-v1",
		"html-monolith":     "chrest-capture-payload-html_monolith-v1",
	}
	for format, want := range cases {
		if got := payloadType(format); got != want {
			t.Errorf("payloadType(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestEnvironmentBodyDefaultsIsolationToFresh(t *testing.T) {
	body := environmentBody(Resolved{Format: "pdf"}, firefox.BrowserInfo{Name: "firefox"})

	if body["isolation"] != "fresh" {
		t.Errorf("isolation = %v, want fresh", body["isolation"])
	}
	// extensions MUST be present and empty (not omitted) when none.
	exts, ok := body["extensions"].([]any)
	if !ok {
		t.Fatalf("extensions = %T, want []any", body["extensions"])
	}
	if len(exts) != 0 {
		t.Errorf("extensions = %v, want empty slice", exts)
	}
}

func TestEnvironmentBodyHonorsExplicitIsolation(t *testing.T) {
	body := environmentBody(Resolved{Isolation: "session"}, firefox.BrowserInfo{Name: "firefox"})
	if body["isolation"] != "session" {
		t.Errorf("isolation = %v, want session", body["isolation"])
	}
}

func TestEnvironmentBodyBrowserOptionalFields(t *testing.T) {
	// js_engine is omitted when empty.
	bare := environmentBody(Resolved{}, firefox.BrowserInfo{Name: "firefox", Version: "120"})
	browser := bare["browser"].(map[string]any)
	if _, ok := browser["js_engine"]; ok {
		t.Errorf("js_engine should be omitted when empty")
	}

	// present when set.
	full := environmentBody(Resolved{}, firefox.BrowserInfo{
		Name:     "firefox",
		JSEngine: "SpiderMonkey",
	})
	fb := full["browser"].(map[string]any)
	if fb["js_engine"] != "SpiderMonkey" {
		t.Errorf("js_engine = %v, want SpiderMonkey", fb["js_engine"])
	}
}

// TestEnvironmentBodyNeverIncludesCommandLine pins chrest#102: command_line
// is a per-run observation (RFC 0003 v2), not identity-affecting config, so
// environmentBody must never emit it regardless of what BrowserInfo carries
// — even a populated CommandLine field must not leak into the identity
// tree. See TestOutcomeBodyIncludesCommandLine for where it actually goes.
func TestEnvironmentBodyNeverIncludesCommandLine(t *testing.T) {
	body := environmentBody(Resolved{}, firefox.BrowserInfo{
		Name:        "firefox",
		CommandLine: []string{"firefox", "--headless", "--profile", "/tmp/volatile-per-launch"},
	})
	browser := body["browser"].(map[string]any)
	if _, ok := browser["command_line"]; ok {
		t.Errorf("command_line must not appear in the environment node (chrest#102): %v", browser)
	}
}

func TestEnvironmentBodyPreinstalledExtensions(t *testing.T) {
	body := environmentBody(Resolved{
		Extensions: []Extension{
			{ID: "ublock@raymondhill.net", Version: "1.0", ManifestDigest: "blake2b256-abc"},
			{ID: "noscript@noscript.net", Version: "11.4"},
		},
	}, firefox.BrowserInfo{Name: "firefox"})

	exts := body["extensions"].([]any)
	if len(exts) != 2 {
		t.Fatalf("len(extensions) = %d, want 2", len(exts))
	}
	first := exts[0].(map[string]any)
	if first["source"] != "preinstalled" || first["id"] != "ublock@raymondhill.net" || first["manifest_digest"] != "blake2b256-abc" {
		t.Errorf("first extension = %v", first)
	}
	second := exts[1].(map[string]any)
	if _, ok := second["manifest_digest"]; ok {
		t.Errorf("manifest_digest should be omitted when empty: %v", second)
	}
}

func TestOutcomeBodyNilWhenNothingObserved(t *testing.T) {
	body, typeString := outcomeBody(nil, "https://example.com", nil)
	if body != nil || typeString != "" {
		t.Errorf("outcomeBody(nil, _, nil) = (%v, %q), want (nil, \"\")", body, typeString)
	}
}

// TestOutcomeBodyIncludesCommandLine pins chrest#102 (RFC 0003 v2): the
// browser's launch argv is a per-run observation, not identity, and lives
// under outcome's process.command_line. Its presence alone (no HTTP
// response) yields the preview type — see
// TestOutcomeBodyTypeKeyedOnHTTPCompletenessAlone for why.
func TestOutcomeBodyIncludesCommandLine(t *testing.T) {
	body, typeString := outcomeBody(nil, "https://example.com", []string{"firefox", "--headless"})
	if typeString != outcomeTypePreview {
		t.Errorf("type = %q, want preview (no http observed)", typeString)
	}
	process, ok := body["process"].(map[string]any)
	if !ok {
		t.Fatalf("process = %T, want map[string]any: %v", body["process"], body)
	}
	if !reflect.DeepEqual(process["command_line"], []any{"firefox", "--headless"}) {
		t.Errorf("command_line = %v", process["command_line"])
	}
	if _, ok := body["http"]; ok {
		t.Errorf("http key should be absent when no response was observed: %v", body)
	}
}

// TestOutcomeBodyTypeKeyedOnHTTPCompletenessAlone pins the RFC 0003
// §Preview Schema revision (chrest#102): the outcome node's type — full
// vs preview — depends only on whether an HTTP response was observed,
// never on whether process.command_line is present (it's present in
// both cases here).
func TestOutcomeBodyTypeKeyedOnHTTPCompletenessAlone(t *testing.T) {
	commandLine := []string{"firefox", "--headless"}

	_, withoutHTTP := outcomeBody(nil, "https://example.com", commandLine)
	if withoutHTTP != outcomeTypePreview {
		t.Errorf("type without http = %q, want %q", withoutHTTP, outcomeTypePreview)
	}

	_, withHTTP := outcomeBody(&firefox.HTTPResponse{Status: 200}, "https://example.com", commandLine)
	if withHTTP != outcomeType {
		t.Errorf("type with http = %q, want %q", withHTTP, outcomeType)
	}
}

func TestOutcomeBodyLowercasesHeaders(t *testing.T) {
	body, typeString := outcomeBody(&firefox.HTTPResponse{
		Status: 200,
		Headers: []firefox.HTTPHeader{
			{Name: "Content-Type", Value: "text/html"},
			{Name: "Set-Cookie", Value: "a=1"},
			{Name: "Set-Cookie", Value: "b=2"},
		},
	}, "https://example.com", nil)

	if typeString != outcomeType {
		t.Errorf("type = %q, want %q", typeString, outcomeType)
	}
	http := body["http"].(map[string]any)
	if http["status"] != 200 {
		t.Errorf("status = %v, want 200", http["status"])
	}
	headers := http["headers"].([]any)
	want := []any{
		map[string]any{"name": "content-type", "value": "text/html"},
		map[string]any{"name": "set-cookie", "value": "a=1"},
		map[string]any{"name": "set-cookie", "value": "b=2"},
	}
	if !reflect.DeepEqual(headers, want) {
		t.Errorf("headers = %v, want %v (lowercased, ordered, duplicates preserved)", headers, want)
	}
}

func TestOutcomeBodyFinalURLAndTiming(t *testing.T) {
	// final_url present when it differs from the target; timing_ms is the
	// object form {load: <int>} when measured.
	body, _ := outcomeBody(&firefox.HTTPResponse{
		Status:   200,
		URL:      "https://example.com/landing",
		TimingMs: 42,
	}, "https://example.com", nil)
	http := body["http"].(map[string]any)
	if http["final_url"] != "https://example.com/landing" {
		t.Errorf("final_url = %v", http["final_url"])
	}
	if !reflect.DeepEqual(http["timing_ms"], map[string]any{"load": int64(42)}) {
		t.Errorf("timing_ms = %v, want {load: 42}", http["timing_ms"])
	}

	// final_url omitted when equal to target; timing_ms omitted when 0.
	same, _ := outcomeBody(&firefox.HTTPResponse{
		Status: 200,
		URL:    "https://example.com",
	}, "https://example.com", nil)
	sh := same["http"].(map[string]any)
	if _, ok := sh["final_url"]; ok {
		t.Errorf("final_url should be omitted when equal to target")
	}
	if _, ok := sh["timing_ms"]; ok {
		t.Errorf("timing_ms should be omitted when unmeasured")
	}
}

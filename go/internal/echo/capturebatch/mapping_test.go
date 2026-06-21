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
	// js_engine and command_line are omitted when empty.
	bare := environmentBody(Resolved{}, firefox.BrowserInfo{Name: "firefox", Version: "120"})
	browser := bare["browser"].(map[string]any)
	if _, ok := browser["js_engine"]; ok {
		t.Errorf("js_engine should be omitted when empty")
	}
	if _, ok := browser["command_line"]; ok {
		t.Errorf("command_line should be omitted when empty")
	}

	// present when set.
	full := environmentBody(Resolved{}, firefox.BrowserInfo{
		Name:        "firefox",
		JSEngine:    "SpiderMonkey",
		CommandLine: []string{"firefox", "--headless"},
	})
	fb := full["browser"].(map[string]any)
	if fb["js_engine"] != "SpiderMonkey" {
		t.Errorf("js_engine = %v, want SpiderMonkey", fb["js_engine"])
	}
	if !reflect.DeepEqual(fb["command_line"], []any{"firefox", "--headless"}) {
		t.Errorf("command_line = %v", fb["command_line"])
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

func TestOutcomeHTTPBodyNilWhenNoResponse(t *testing.T) {
	if got := outcomeHTTPBody(nil, "https://example.com"); got != nil {
		t.Errorf("outcomeHTTPBody(nil) = %v, want nil", got)
	}
}

func TestOutcomeHTTPBodyLowercasesHeaders(t *testing.T) {
	body := outcomeHTTPBody(&firefox.HTTPResponse{
		Status: 200,
		Headers: []firefox.HTTPHeader{
			{Name: "Content-Type", Value: "text/html"},
			{Name: "Set-Cookie", Value: "a=1"},
			{Name: "Set-Cookie", Value: "b=2"},
		},
	}, "https://example.com")

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

func TestOutcomeHTTPBodyFinalURLAndTiming(t *testing.T) {
	// final_url present when it differs from the target; timing_ms is the
	// object form {load: <int>} when measured.
	body := outcomeHTTPBody(&firefox.HTTPResponse{
		Status:   200,
		URL:      "https://example.com/landing",
		TimingMs: 42,
	}, "https://example.com")
	http := body["http"].(map[string]any)
	if http["final_url"] != "https://example.com/landing" {
		t.Errorf("final_url = %v", http["final_url"])
	}
	if !reflect.DeepEqual(http["timing_ms"], map[string]any{"load": int64(42)}) {
		t.Errorf("timing_ms = %v, want {load: 42}", http["timing_ms"])
	}

	// final_url omitted when equal to target; timing_ms omitted when 0.
	same := outcomeHTTPBody(&firefox.HTTPResponse{
		Status: 200,
		URL:    "https://example.com",
	}, "https://example.com")
	sh := same["http"].(map[string]any)
	if _, ok := sh["final_url"]; ok {
		t.Errorf("final_url should be omitted when equal to target")
	}
	if _, ok := sh["timing_ms"]; ok {
		t.Errorf("timing_ms should be omitted when unmeasured")
	}
}

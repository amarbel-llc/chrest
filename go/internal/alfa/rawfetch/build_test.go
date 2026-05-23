package rawfetch

import (
	"strings"
	"testing"
)

func TestBuildFromText_Markdown(t *testing.T) {
	body := []byte("# Hello\n\nWorld\n")
	r := BuildFromText(body, "text/markdown; charset=utf-8", "https://example.com/x.md")
	if string(r.Text) != string(body) {
		t.Errorf("Text mismatch: %q", r.Text)
	}
	if string(r.Markdown) != string(body) {
		t.Errorf("Markdown for md content should be the body verbatim; got %q", r.Markdown)
	}
	if !strings.Contains(string(r.HTML), "<pre>") {
		t.Errorf("HTML should wrap body in <pre>; got %q", r.HTML)
	}
	if len(r.TOC) != 1 || r.TOC[0].Text != "Hello" {
		t.Errorf("TOC mismatch: %+v", r.TOC)
	}
}

func TestBuildFromText_NonMarkdownText(t *testing.T) {
	body := []byte(`{"a":1}`)
	r := BuildFromText(body, "application/json", "https://example.com/x.json")
	if string(r.Text) != string(body) {
		t.Errorf("Text mismatch")
	}
	if !strings.HasPrefix(string(r.Markdown), "```") {
		t.Errorf("non-md text should be wrapped in a fenced code block; got %q", r.Markdown)
	}
	if !strings.Contains(string(r.Markdown), "json") {
		t.Errorf("language hint should appear in fence; got %q", r.Markdown)
	}
	wantHTML := `<pre>{&#34;a&#34;:1}</pre>`
	if string(r.HTML) != wantHTML {
		t.Errorf("HTML mismatch: got %q want %q", r.HTML, wantHTML)
	}
	if len(r.TOC) != 0 {
		t.Errorf("non-md text should have empty TOC; got %+v", r.TOC)
	}
}

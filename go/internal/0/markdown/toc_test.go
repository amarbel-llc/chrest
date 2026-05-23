package markdown

import (
	"strings"
	"testing"
)

func TestExtractTOC_BasicLevels(t *testing.T) {
	fixture := `<h1 id="a">A</h1><h2 id="b">B</h2><h3 id="c">C</h3>`
	got, err := ExtractTOC(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ExtractTOC: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 headings; got %d: %+v", len(got), got)
	}
	want := []Heading{
		{ID: "a", Text: "A", Level: 1},
		{ID: "b", Text: "B", Level: 2},
		{ID: "c", Text: "C", Level: 3},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("headings[%d]: got %+v; want %+v", i, got[i], w)
		}
	}
}

func TestExtractTOC_SkipsNoID(t *testing.T) {
	fixture := `<h2 id="keep">Keep</h2><h2>drop</h2><h3 id="sub">Sub</h3>`
	got, err := ExtractTOC(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ExtractTOC: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 headings; got %d: %+v", len(got), got)
	}
	for _, h := range got {
		if h.Text == "drop" {
			t.Errorf("id-less heading leaked into output: %+v", h)
		}
	}
	if got[0].ID != "keep" || got[1].ID != "sub" {
		t.Errorf("wrong entries: %+v", got)
	}
}

func TestExtractTOC_CollapsesWhitespace(t *testing.T) {
	fixture := "<h2 id=\"x\">  Foo\n\n<span>Bar</span>\tBaz </h2>"
	got, err := ExtractTOC(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ExtractTOC: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 heading; got %d: %+v", len(got), got)
	}
	if got[0].Text != "Foo Bar Baz" {
		t.Errorf("text not collapsed: got %q; want %q", got[0].Text, "Foo Bar Baz")
	}
}

func TestExtractTOC_EmptyTextUsesID(t *testing.T) {
	fixture := `<h2 id="only-id"><span></span></h2>`
	got, err := ExtractTOC(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ExtractTOC: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 heading; got %d: %+v", len(got), got)
	}
	if got[0].Text != "only-id" {
		t.Errorf("empty text should fall back to id: got %q; want %q", got[0].Text, "only-id")
	}
}

func TestFormatTOC_Empty(t *testing.T) {
	got := FormatTOC(nil, "https://x", "")
	want := "(no headings with ids found on https://x)"
	if got != want {
		t.Errorf("FormatTOC(nil): got %q; want %q", got, want)
	}
}

func TestFormatTOC_NestingRelative(t *testing.T) {
	headings := []Heading{
		{ID: "a", Text: "A", Level: 2},
		{ID: "b", Text: "B", Level: 3},
		{ID: "c", Text: "C", Level: 4},
		{ID: "d", Text: "D", Level: 3},
	}
	got := FormatTOC(headings, "https://example.com", "")

	cases := []struct {
		needle string
		desc   string
	}{
		{"- `#a`", "top-level h2 at column 0"},
		{"  - `#b`", "h3 at two-space indent"},
		{"    - `#c`", "h4 at four-space indent"},
		{"  - `#d`", "h3 back at two-space indent"},
	}
	for _, c := range cases {
		if !strings.Contains(got, c.needle) {
			t.Errorf("%s: output missing %q\n---\n%s", c.desc, c.needle, got)
		}
	}
}

func TestFormatTOC_IncludesURL(t *testing.T) {
	headings := []Heading{{ID: "a", Text: "A", Level: 1}}
	url := "https://example.com/some/page"
	got := FormatTOC(headings, url, "")
	if !strings.Contains(got, url) {
		t.Errorf("FormatTOC output does not mention URL %q\n---\n%s", url, got)
	}
}

func TestFormatTOC_FragmentHint(t *testing.T) {
	headings := []Heading{{ID: "target", Text: "Target", Level: 2}}
	got := FormatTOC(headings, "https://example.com/page", "target")
	if !strings.Contains(got, `selector="#target"`) {
		t.Errorf("expected fragment hint in output; got:\n%s", got)
	}
}

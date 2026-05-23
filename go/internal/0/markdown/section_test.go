package markdown

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func readAllToString(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(b)
}

func TestConvertSelectorSection_HeadingExpandsToNextSameLevel(t *testing.T) {
	dom := `<main>
<h2 id="intro">Introduction</h2>
<p>Intro body text.</p>
<h3 id="sub">Sub section</h3>
<p>Sub body text.</p>
<h2 id="other">Other</h2>
<p>Other body text.</p>
</main>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#intro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)

	if !strings.Contains(got, "Introduction") {
		t.Errorf("expected matched heading text; got: %q", got)
	}
	if !strings.Contains(got, "Intro body text.") {
		t.Errorf("expected sibling paragraph after heading; got: %q", got)
	}
	if !strings.Contains(got, "Sub section") {
		t.Errorf("expected deeper h3 (inside section) to be included; got: %q", got)
	}
	if !strings.Contains(got, "Sub body text.") {
		t.Errorf("expected paragraph under deeper h3 to be included; got: %q", got)
	}
	if strings.Contains(got, "Other") {
		t.Errorf("expected section to stop at next h2; got: %q", got)
	}
	if strings.Contains(got, "Other body text.") {
		t.Errorf("expected section to stop before next h2's body; got: %q", got)
	}
}

func TestConvertSelectorSection_HeadingAtEndOfParent(t *testing.T) {
	dom := `<main>
<p>Preamble.</p>
<h2 id="final">Final</h2>
<p>Final body text.</p>
</main>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#final")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)
	if !strings.Contains(got, "Final") {
		t.Errorf("expected matched heading; got: %q", got)
	}
	if !strings.Contains(got, "Final body text.") {
		t.Errorf("expected sibling paragraph; got: %q", got)
	}
	if strings.Contains(got, "Preamble") {
		t.Errorf("expected NO content from before the match; got: %q", got)
	}
}

func TestConvertSelectorSection_NonHeadingMatchDoesNotExpand(t *testing.T) {
	dom := `<main>
<article id="keep"><h2>Inside</h2><p>Body inside.</p></article>
<p>Outside body.</p>
</main>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#keep")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)
	if !strings.Contains(got, "Inside") {
		t.Errorf("expected matched article's own content; got: %q", got)
	}
	if !strings.Contains(got, "Body inside.") {
		t.Errorf("expected matched article's body paragraph; got: %q", got)
	}
	if strings.Contains(got, "Outside body.") {
		t.Errorf("non-heading match should NOT pull in siblings; got: %q", got)
	}
}

func TestConvertSelectorSection_NoMatchReturnsSentinel(t *testing.T) {
	_, err := ConvertSelectorSection(strings.NewReader(`<p>nothing to see</p>`), "#missing")
	if err == nil {
		t.Fatal("expected error for no-match selector; got nil")
	}
	if !errors.Is(err, ErrSelectorNoMatch) {
		t.Fatalf("expected errors.Is(err, ErrSelectorNoMatch); got: %v", err)
	}
}

func TestConvertSelectorSection_EmptySelectorRejected(t *testing.T) {
	_, err := ConvertSelectorSection(strings.NewReader(`<p>hi</p>`), "")
	if err == nil {
		t.Fatal("expected error for empty selector; got nil")
	}
}

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

// DocBook-style 3-deep titlepage wrap. Reproduces the nixpkgs manual
// layout that motivated chrest#62: the matched <h2> has empty sibling
// space inside its innermost wrapping <div>, so a naive sibling-walk
// returns just the heading. Promotion climbs through the single-child
// chain to the outer section container and walks its siblings instead.
func TestConvertSelectorSection_PromotesDocBookTitlepageWrap(t *testing.T) {
	dom := `<body><div class="section">` +
		`<div class="titlepage"><div><div>` +
		`<h2 id="phases" class="title">Phases</h2>` +
		`</div></div></div>` +
		`<div class="toc"><a href="#unpack">Unpack</a></div>` +
		`<p>stdenv.mkDerivation sets builder to a script.</p>` +
		`<div class="section"><h3 id="unpack" class="title">Unpack phase</h3><p>Unpack body.</p></div>` +
		`</div>` +
		`<div class="section"><h2 id="next" class="title">Next phase-level section</h2></div>` +
		`</body>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#phases")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)
	if !strings.Contains(got, "Phases") {
		t.Errorf("expected matched heading; got: %q", got)
	}
	if !strings.Contains(got, "stdenv.mkDerivation sets builder") {
		t.Errorf("expected sibling prose to be included after promotion; got: %q", got)
	}
	if !strings.Contains(got, "Unpack phase") {
		t.Errorf("expected subsection heading inside same section to be included; got: %q", got)
	}
	if !strings.Contains(got, "Unpack body.") {
		t.Errorf("expected subsection body inside same section to be included; got: %q", got)
	}
	if strings.Contains(got, "Next phase-level section") {
		t.Errorf("expected walk to stop at outer-section boundary; got: %q", got)
	}
}

// HTML5 idiom: heading wrapped in a <header> element, with the section
// content as siblings of <header>. Promotion lifts the matched node to
// <header> so the following <p> joins the walk.
func TestConvertSelectorSection_PromotesHTML5HeaderWrap(t *testing.T) {
	dom := `<article id="post">` +
		`<header><h1 id="title">Title</h1></header>` +
		`<p>Body paragraph.</p>` +
		`</article>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#title")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)
	if !strings.Contains(got, "Title") {
		t.Errorf("expected matched heading; got: %q", got)
	}
	if !strings.Contains(got, "Body paragraph.") {
		t.Errorf("expected sibling-of-header body to be included after promotion; got: %q", got)
	}
}

// Over-promotion guard: a heading that is the only descendant of a
// <body><main> chain must not bubble past <main>. The promoted matched
// element should be <main> itself, not the document root.
func TestConvertSelectorSection_PromotionStopsAtSemanticRoot(t *testing.T) {
	dom := `<html><body><main>` +
		`<div class="wrap"><div class="inner"><h1 id="only">Only Heading</h1></div></div>` +
		`</main></body></html>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#only")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)
	if !strings.Contains(got, "Only Heading") {
		t.Errorf("expected matched heading; got: %q", got)
	}
	// No content beyond the wrapper chain should be pulled in. We can't
	// observe "stopped at main" directly, but we can assert the output
	// is bounded — a few hundred chars at most for this fixture.
	if len(got) > 200 {
		t.Errorf("expected bounded output for single-heading page; got %d bytes: %q", len(got), got)
	}
}

// Sibling-walk should stop when a sibling element CONTAINS a heading at
// or above the matched level, not only when the sibling tag itself is a
// heading. This is the wrapper-aware termination needed alongside
// promotion — a sibling section wrapper announces a new same-level
// section even when its own tag is <div>.
func TestConvertSelectorSection_StopsWalkOnDescendantHeadingInSibling(t *testing.T) {
	dom := `<body>` +
		`<div class="section"><div class="titlepage"><div><div>` +
		`<h2 id="first">First</h2>` +
		`</div></div></div>` +
		`<p>First body.</p>` +
		`</div>` +
		`<div class="section"><div class="titlepage"><div><div>` +
		`<h2 id="second">Second</h2>` +
		`</div></div></div>` +
		`<p>Second body.</p>` +
		`</div>` +
		`</body>`
	rc, err := ConvertSelectorSection(strings.NewReader(dom), "#first")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := readAllToString(t, rc)
	if !strings.Contains(got, "First") {
		t.Errorf("expected matched heading; got: %q", got)
	}
	if !strings.Contains(got, "First body.") {
		t.Errorf("expected first-section body; got: %q", got)
	}
	if strings.Contains(got, "Second") {
		t.Errorf("expected walk to stop before sibling section containing same-level heading; got: %q", got)
	}
	if strings.Contains(got, "Second body.") {
		t.Errorf("expected walk to stop before sibling section body; got: %q", got)
	}
}

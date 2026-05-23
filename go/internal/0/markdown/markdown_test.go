package markdown

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	return string(b)
}

const boilerplateFixture = `<!doctype html>
<html>
<head>
	<title>Test Article</title>
	<meta charset="utf-8">
</head>
<body>
	<nav>Site Nav · <a href="/">Home</a> · <a href="/about">About</a></nav>
	<main>
		<article>
			<h1>The Title Of The Article</h1>
			<p>First paragraph of real content with enough text for readability to consider it an article. It needs several sentences to pass the heuristic score. This should do the trick. Lorem ipsum dolor sit amet, consectetur adipiscing elit.</p>
			<p>Second paragraph with more substantive text. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.</p>
			<p>Third paragraph rounding out the article body with yet more filler. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.</p>
		</article>
	</main>
	<footer>Page Footer · Copyright 2026</footer>
</body>
</html>`

func TestConvertFull_IncludesEverything(t *testing.T) {
	rc, err := ConvertFull(context.Background(), strings.NewReader(boilerplateFixture))
	if err != nil {
		t.Fatalf("ConvertFull: %v", err)
	}
	md := readAll(t, rc)
	for _, substr := range []string{
		"# The Title Of The Article",
		"Site Nav",
		"First paragraph",
		"Page Footer",
	} {
		if !strings.Contains(md, substr) {
			t.Errorf("ConvertFull output missing %q\n---\n%s", substr, md)
		}
	}
}

func TestConvertReader_StripsBoilerplate(t *testing.T) {
	rc, err := ConvertReader(context.Background(), strings.NewReader(boilerplateFixture), "https://example.com/article")
	if err != nil {
		t.Fatalf("ConvertReader: %v", err)
	}
	md := readAll(t, rc)

	if !strings.Contains(md, "First paragraph") {
		t.Errorf("ConvertReader dropped the article body\n---\n%s", md)
	}
	// Readability's main-content extraction excludes <nav> and <footer> chrome
	// by scoring heuristics — the article body is much denser text than the
	// nav/footer links, so those boilerplate regions should be gone.
	for _, banned := range []string{"Site Nav", "Page Footer"} {
		if strings.Contains(md, banned) {
			t.Errorf("ConvertReader kept boilerplate %q\n---\n%s", banned, md)
		}
	}
}

func TestConvertSelector_Scoped(t *testing.T) {
	rc, err := ConvertSelector(context.Background(), strings.NewReader(boilerplateFixture), "footer")
	if err != nil {
		t.Fatalf("ConvertSelector: %v", err)
	}
	md := readAll(t, rc)
	// Footer content should be present, article body should be absent.
	if !strings.Contains(md, "Page Footer") {
		t.Errorf("ConvertSelector dropped the selected node\n---\n%s", md)
	}
	if strings.Contains(md, "First paragraph") {
		t.Errorf("ConvertSelector leaked content outside the selector\n---\n%s", md)
	}
}

func TestConvertSelector_NoMatch(t *testing.T) {
	_, err := ConvertSelector(context.Background(),
		strings.NewReader(`<p>no headings here</p>`),
		"#does-not-exist")
	if err == nil {
		t.Fatal("expected error for no-match selector; got nil")
	}
	if !errors.Is(err, ErrSelectorNoMatch) {
		t.Fatalf("expected errors.Is(err, ErrSelectorNoMatch) to be true; got: %v", err)
	}
}

func TestConvertSelector_EmptyRejected(t *testing.T) {
	_, err := ConvertSelector(context.Background(), strings.NewReader(boilerplateFixture), "")
	if err == nil {
		t.Fatal("expected error for empty selector")
	}
}

func TestConvertSelector_InvalidSyntax(t *testing.T) {
	_, err := ConvertSelector(context.Background(), strings.NewReader(boilerplateFixture), "[unclosed")
	if err == nil {
		t.Fatal("expected error for invalid selector syntax")
	}
}

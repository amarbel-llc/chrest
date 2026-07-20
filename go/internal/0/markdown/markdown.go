// Package markdown encodes HTML captures as CommonMark+GFM markdown.
//
// Three extraction strategies are exposed, one per public function:
//
//   - ConvertFull       — whole rendered DOM → markdown
//   - ConvertReader     — Readability-extracted main content → markdown
//   - ConvertSelector   — CSS-selector scoped element → markdown
//
// All three accept the rendered DOM as an io.Reader (per cdp.Session's
// GetDocumentHTML shape) and return the markdown bytes as an
// io.ReadCloser so the caller can stream them to a writer without
// another intermediate copy.
package markdown

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
	readability "codeberg.org/readeck/go-readability/v2"
)

// ErrNoArticle is returned by ConvertReader when Readability was unable
// to find a main-content article in the document.
var ErrNoArticle = errors.Errorf("readability found no article content")

// ErrSelectorNoMatch is returned (wrapped) by ConvertSelector when the
// parsed selector is valid but matches no element in the document.
// Callers can detect this with errors.Is to distinguish a miss from a
// malformed selector or a DOM-parse failure.
var ErrSelectorNoMatch = errors.Errorf("selector matched no element")

// ConvertFull converts the full rendered DOM to markdown.
func ConvertFull(ctx context.Context, dom io.Reader) (io.ReadCloser, error) {
	raw, err := io.ReadAll(dom)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	md, err := htmltomarkdown.ConvertString(string(raw))
	if err != nil {
		return nil, fmt.Errorf("html-to-markdown: %w", err)
	}
	return io.NopCloser(bytes.NewReader([]byte(md))), nil
}

// ConvertReader runs Readability on the DOM to extract the main-content
// subtree, then converts that to markdown. baseURL is required so
// Readability can resolve relative links while cleaning the document;
// pass the navigated URL.
func ConvertReader(ctx context.Context, dom io.Reader, baseURL string) (io.ReadCloser, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse baseURL %q: %w", baseURL, err)
	}

	article, err := readability.FromReader(dom, u)
	if err != nil {
		return nil, fmt.Errorf("readability: %w", err)
	}
	if article.Node == nil {
		return nil, ErrNoArticle
	}

	var htmlBuf bytes.Buffer
	if err := article.RenderHTML(&htmlBuf); err != nil {
		return nil, fmt.Errorf("render extracted html: %w", err)
	}

	md, err := htmltomarkdown.ConvertString(htmlBuf.String())
	if err != nil {
		return nil, fmt.Errorf("html-to-markdown: %w", err)
	}
	return io.NopCloser(bytes.NewReader([]byte(md))), nil
}

// ConvertSelector parses the DOM, finds the first element matching
// selector, and converts that element's subtree to markdown. Returns
// a wrapped error that names the selector if no element matches.
func ConvertSelector(ctx context.Context, dom io.Reader, selector string) (io.ReadCloser, error) {
	if selector == "" {
		return nil, errors.Errorf("selector MUST be non-empty")
	}

	sel, err := cascadia.Parse(selector)
	if err != nil {
		return nil, fmt.Errorf("parse selector %q: %w", selector, err)
	}

	root, err := html.Parse(dom)
	if err != nil {
		return nil, fmt.Errorf("parse dom: %w", err)
	}

	matched := cascadia.Query(root, sel)
	if matched == nil {
		return nil, fmt.Errorf("%q: %w", selector, ErrSelectorNoMatch)
	}

	var htmlBuf bytes.Buffer
	if err := html.Render(&htmlBuf, matched); err != nil {
		return nil, fmt.Errorf("render matched node: %w", err)
	}

	md, err := htmltomarkdown.ConvertString(htmlBuf.String())
	if err != nil {
		return nil, fmt.Errorf("html-to-markdown: %w", err)
	}
	return io.NopCloser(bytes.NewReader([]byte(md))), nil
}

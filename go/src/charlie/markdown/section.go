package markdown

import (
	"bytes"
	"fmt"
	"io"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ConvertSelectorSection parses the DOM, finds the first element matching
// selector, and converts it to markdown — with one twist: when the matched
// element is a heading (h1-h6), the returned subtree is expanded to also
// include every following sibling up to (but not including) the next
// heading of equal-or-higher level. This lets selectors like `#introduction`
// return the whole "Introduction" section instead of just the `<h2>` tag.
//
// For non-heading matches the behavior is identical to ConvertSelector:
// only the matched element's own subtree is rendered.
//
// Wraps ErrSelectorNoMatch when the selector is valid but matches nothing.
func ConvertSelectorSection(dom io.Reader, selector string) (io.ReadCloser, error) {
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

	nodes := []*html.Node{matched}
	if lvl := headingLevel(matched.DataAtom); lvl != 0 {
		for sib := matched.NextSibling; sib != nil; sib = sib.NextSibling {
			if sibLvl := headingLevel(sib.DataAtom); sibLvl != 0 && sibLvl <= lvl {
				break
			}
			nodes = append(nodes, sib)
		}
	}

	var htmlBuf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&htmlBuf, n); err != nil {
			return nil, fmt.Errorf("render section node: %w", err)
		}
	}

	md, err := htmltomarkdown.ConvertString(htmlBuf.String())
	if err != nil {
		return nil, fmt.Errorf("html-to-markdown: %w", err)
	}
	return io.NopCloser(bytes.NewReader([]byte(md))), nil
}

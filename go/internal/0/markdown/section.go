package markdown

import (
	"bytes"
	"fmt"
	"io"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// ConvertSelectorSection parses the DOM, finds the first element matching
// selector, and converts it to markdown — with one twist: when the matched
// element is a heading (h1-h6), the returned subtree is expanded to also
// include every following sibling up to (but not including) the next
// heading of equal-or-higher level. This lets selectors like `#introduction`
// return the whole "Introduction" section instead of just the `<h2>` tag.
//
// Heading promotion: many CMS-generated pages (DocBook, HTML5 <header>,
// MkDocs) wrap their headings inside single-child container chains. A bare
// sibling-walk from the matched heading would only see siblings inside the
// innermost wrapper — empty in those layouts. So when matched is a heading
// that sits at the bottom of a single-child-chain ancestor stack, the
// sibling-walk runs from the topmost such ancestor instead, stopping at
// semantic content roots (body/html/main/article/section/aside/nav) so we
// never escape the page's outer content boundary.
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
		start := promoteHeadingWrapper(matched)
		nodes = []*html.Node{start}
		for sib := start.NextSibling; sib != nil; sib = sib.NextSibling {
			if containsHeadingAtOrAbove(sib, lvl) {
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

// promoteHeadingWrapper walks up from h while each ancestor's only
// non-whitespace child chain leads to h, stopping at the first ancestor
// with multiple content branches or at a semantic content root. Returns
// h itself when no promotion is warranted.
func promoteHeadingWrapper(h *html.Node) *html.Node {
	cur := h
	for cur.Parent != nil {
		parent := cur.Parent
		// Never bubble into the synthetic document node — its DataAtom
		// is 0 so it won't match the semantic-root list, but a render
		// of it would serialize the whole document.
		if parent.Type == html.DocumentNode {
			break
		}
		if isSemanticContentRoot(parent.DataAtom) {
			break
		}
		if !isOnlyContentChild(parent, cur) {
			break
		}
		cur = parent
	}
	return cur
}

// isOnlyContentChild reports whether child is parent's only content
// child. Whitespace-only text nodes and comments are ignored.
func isOnlyContentChild(parent, child *html.Node) bool {
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c == child {
			continue
		}
		if isContentNode(c) {
			return false
		}
	}
	return true
}

// isContentNode reports whether n carries page content. Element nodes
// always do; text nodes do only when they contain at least one
// non-whitespace rune.
func isContentNode(n *html.Node) bool {
	switch n.Type {
	case html.ElementNode:
		return true
	case html.TextNode:
		for _, r := range n.Data {
			if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
				return true
			}
		}
		return false
	}
	return false
}

// isSemanticContentRoot lists the tags we never promote past, so a
// heading that is the only descendant of e.g. <body><main> stays inside
// <main> rather than bubbling up to the document root.
func isSemanticContentRoot(a atom.Atom) bool {
	switch a {
	case atom.Html, atom.Body, atom.Main, atom.Article, atom.Section,
		atom.Aside, atom.Nav:
		return true
	}
	return false
}

// containsHeadingAtOrAbove reports whether node or any descendant is a
// heading at level ≤ targetLvl — i.e., the start of a section equal to
// or broader than the matched one. Used to terminate sibling-walks
// across both flat and wrapper-style DOM shapes.
func containsHeadingAtOrAbove(node *html.Node, targetLvl int) bool {
	if lvl := headingLevel(node.DataAtom); lvl != 0 && lvl <= targetLvl {
		return true
	}
	for c := node.FirstChild; c != nil; c = c.NextSibling {
		if containsHeadingAtOrAbove(c, targetLvl) {
			return true
		}
	}
	return false
}

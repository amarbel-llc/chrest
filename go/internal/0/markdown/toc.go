package markdown

import (
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
)

// Heading is a single h1-h6 element with an `id` attribute, captured by
// ExtractTOC for the purpose of building a selector-ready table of contents.
type Heading struct {
	ID    string
	Text  string
	Level int // 1-6
}

// ExtractTOC returns headings (h1-h6) that carry a non-empty `id`
// attribute, in document order. Headings without ids are skipped — they
// can't be selected via a `#id` CSS selector.
func ExtractTOC(dom io.Reader) ([]Heading, error) {
	root, err := html.Parse(dom)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	sel := cascadia.MustCompile("h1, h2, h3, h4, h5, h6")
	matches := cascadia.QueryAll(root, sel)

	headings := make([]Heading, 0, len(matches))
	for _, node := range matches {
		id := extractID(node)
		if id == "" {
			continue
		}

		level := headingLevel(node.DataAtom)
		if level == 0 {
			continue
		}

		text := collectText(node)
		if text == "" {
			text = id
		}

		headings = append(headings, Heading{
			ID:    id,
			Text:  text,
			Level: level,
		})
	}

	return headings, nil
}

// FormatTOC renders headings as a markdown list whose entries are
// ready-to-paste CSS selectors. Indentation reflects each heading's level
// relative to the shallowest level present in the list. When headings is
// empty, returns a single-line "(no headings...)" fallback naming url.
// fragment, if non-empty, is an anchor from the original URL — a hint line
// is appended pointing the caller to that selector.
func FormatTOC(headings []Heading, url, fragment string) string {
	if len(headings) == 0 {
		return fmt.Sprintf("(no headings with ids found on %s)", url)
	}

	minLevel := headings[0].Level
	for _, h := range headings {
		if h.Level < minLevel {
			minLevel = h.Level
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "### Table of Contents for %s\n\n", url)
	for _, h := range headings {
		indent := strings.Repeat("  ", h.Level-minLevel)
		fmt.Fprintf(&b, "%s- `#%s` — %s\n", indent, h.ID, h.Text)
	}
	b.WriteString("\nPass one of these ids as `selector` to fetch that section only.\n")
	if fragment != "" {
		fmt.Fprintf(&b, "\nNote: the URL points to `#%s` — pass `selector=\"#%s\"` to fetch that section.\n", fragment, fragment)
	}
	return b.String()
}

// extractID returns the trimmed value of the node's `id` attribute, or
// "" if the attribute is missing or blank.
func extractID(node *html.Node) string {
	for _, a := range node.Attr {
		if a.Key == "id" {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// headingLevel maps an html/atom heading tag to its level (1-6). Returns
// 0 for non-heading atoms so callers can skip.
func headingLevel(a atom.Atom) int {
	switch a {
	case atom.H1:
		return 1
	case atom.H2:
		return 2
	case atom.H3:
		return 3
	case atom.H4:
		return 4
	case atom.H5:
		return 5
	case atom.H6:
		return 6
	}
	return 0
}

// collectText concatenates all descendant text nodes and collapses
// consecutive whitespace to single spaces.
func collectText(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(b.String()), " ")
}

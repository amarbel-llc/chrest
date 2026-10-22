package bookmark_plugin

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	cgp "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
)

// entry is one tree node plus where it sits: segments are its URI path
// segments from the browser root (a root folder's own name first).
type entry struct {
	node     browser_items.BookmarkNode
	segments []string
	parent   *entry
	children []*entry
}

// folder is the slash-joined path of the folders above this node,
// e.g. "toolbar/reading/rust" for a bookmark in that folder.
func (e *entry) folder() string {
	if len(e.segments) <= 1 {
		return ""
	}

	return strings.Join(e.segments[:len(e.segments)-1], "/")
}

// treeIndex indexes one browser's tree by id and by path.
type treeIndex struct {
	browserId string
	roots     []*entry
	byId      map[string]*entry
}

func newTreeIndex(browserId string, tree browser_items.BookmarkTree) *treeIndex {
	idx := &treeIndex{browserId: browserId, byId: map[string]*entry{}}

	var add func(n browser_items.BookmarkNode, parent *entry) *entry
	add = func(n browser_items.BookmarkNode, parent *entry) *entry {
		e := &entry{node: n, parent: parent}

		seg := segmentFor(n)
		if parent != nil {
			e.segments = append(append([]string{}, parent.segments...), seg)
		} else {
			e.segments = []string{seg}
		}

		idx.byId[n.Id] = e

		for _, c := range n.Children {
			if c.Kind == browser_items.BookmarkKindSeparator {
				continue
			}

			e.children = append(e.children, add(c, e))
		}

		sort.SliceStable(e.children, func(i, j int) bool {
			a, b := e.children[i].node.Index, e.children[j].node.Index
			if a == nil || b == nil {
				return false
			}

			return *a < *b
		})

		return e
	}

	for _, r := range tree.Roots {
		idx.roots = append(idx.roots, add(r, nil))
	}

	return idx
}

// segmentFor names a node in its URI path: the normalized root name for a
// top-level folder, else the title, else the id for an untitled node.
func segmentFor(n browser_items.BookmarkNode) string {
	switch {
	case n.Root != "":
		return n.Root
	case n.Title != "":
		return n.Title
	default:
		return n.Id
	}
}

// resolve finds the node a URI addresses: by ?id= when present (the
// authoritative identity, since titles repeat), else by walking the path.
func (idx *treeIndex) resolve(node *url.URL) (*entry, error) {
	if id := node.Query().Get("id"); id != "" {
		e, ok := idx.byId[id]
		if !ok {
			return nil, fmt.Errorf("bookmark: no node with id %q in %s", id, idx.browserId)
		}

		return e, nil
	}

	var cur *entry

	for _, raw := range strings.Split(strings.Trim(node.EscapedPath(), "/"), "/") {
		seg, err := url.PathUnescape(raw)
		if err != nil {
			return nil, fmt.Errorf("bookmark: bad path segment %q: %w", raw, err)
		}

		next := idx.child(cur, seg)
		if next == nil {
			return nil, fmt.Errorf("bookmark: no node %q under %s", seg, node)
		}

		cur = next
	}

	return cur, nil
}

func (idx *treeIndex) child(parent *entry, segment string) *entry {
	siblings := idx.roots
	if parent != nil {
		siblings = parent.children
	}

	for _, s := range siblings {
		if s.segments[len(s.segments)-1] == segment {
			return s
		}
	}

	return nil
}

// childrenOf returns the entries directly under node: the browser's root
// folders for the browser root, else a folder's children. ok is false when
// node is a bookmark, which has no children.
func (idx *treeIndex) childrenOf(node *url.URL) (children []*entry, ok bool, err error) {
	if isBrowserRoot(node) {
		return idx.roots, true, nil
	}

	target, err := idx.resolve(node)
	if err != nil {
		return nil, false, err
	}

	if target.node.Kind != browser_items.BookmarkKindFolder {
		return nil, false, nil
	}

	return target.children, true, nil
}

// nodesFor renders entries as traversal nodes.
func (idx *treeIndex) nodesFor(children []*entry) []cgp.Node {
	out := make([]cgp.Node, 0, len(children))
	for _, c := range children {
		out = append(out, idx.nodeFor(c))
	}

	return out
}

func (idx *treeIndex) nodeFor(e *entry) cgp.Node {
	escaped := make([]string, len(e.segments))
	for i, s := range e.segments {
		escaped[i] = url.PathEscape(s)
	}

	u := &url.URL{
		Scheme:   Scheme,
		Host:     idx.browserId,
		Path:     "/" + strings.Join(e.segments, "/"),
		RawPath:  "/" + strings.Join(escaped, "/"),
		RawQuery: url.Values{"id": {e.node.Id}}.Encode(),
	}

	n := cgp.Node{URI: u, Name: e.node.Title}
	if n.Name == "" {
		n.Name = e.segments[len(e.segments)-1]
	}

	if e.node.Kind == browser_items.BookmarkKindBookmark {
		n.Type = TypeBookmark
		n.Facets = facetsFor(e)
	} else {
		n.Type = TypeFolder
	}

	return n
}

func facetsFor(e *entry) map[string][]cgp.FacetValue {
	facets := map[string][]cgp.FacetValue{}

	if f := e.folder(); f != "" {
		facets[FacetFolder] = []cgp.FacetValue{{Key: f}}
	}

	facets[FacetDomain] = []cgp.FacetValue{{Key: domainOf(e.node.Url)}}

	if e.node.DateAdded != nil {
		day := time.UnixMilli(*e.node.DateAdded).UTC().Format("2006-01-02")
		facets[FacetDateAdded] = []cgp.FacetValue{{Key: day}}
	}

	return facets
}

// domainOf is the lowercased host of raw, or a fixed placeholder for URLs
// without one (data:, javascript:, place:, ...), so every bookmark lands in
// exactly one bucket.
func domainOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return noHostDomain
	}

	return strings.ToLower(u.Hostname())
}

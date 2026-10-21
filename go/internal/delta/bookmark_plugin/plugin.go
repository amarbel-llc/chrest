// Package bookmark_plugin serves a browser's bookmark tree as a read-only
// cutting-garden traversal plugin (scheme `bookmark:`, RFC 0013), one root
// per live chrest browser socket. See
// docs/plans/2026-10-21-bookmark-traversal-serve-design.md.
package bookmark_plugin

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	cgp "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
)

const (
	Scheme = "bookmark"

	TypeBrowser  = "chrest-bookmark-browser-v1"
	TypeFolder   = "chrest-bookmark-folder-v1"
	TypeBookmark = "chrest-bookmark-v1"

	FacetFolder    = "folder"
	FacetDomain    = "domain"
	FacetDateAdded = "date_added"

	noHostDomain = "(no host)"
)

// Source supplies the bookmark trees of the live browsers. The production
// implementation reads chrest's per-browser unix sockets; tests inject fakes.
type Source interface {
	// Browsers returns the ids (e.g. "firefox-main") of live browsers.
	Browsers(ctx context.Context) ([]string, error)
	// Tree returns one browser's normalized bookmark tree.
	Tree(ctx context.Context, browserId string) (browser_items.BookmarkTree, error)
}

// Plugin implements the read-only traversal surface: RootLister,
// RootProvider, LeafReader and FacetDescriber.
type Plugin struct {
	src Source
}

func New(src Source) *Plugin {
	return &Plugin{src: src}
}

func (*Plugin) Schemes() []string { return []string{Scheme} }

func (*Plugin) TypeTag() string { return TypeBookmark }

func (*Plugin) Types() []cgp.NodeType {
	return []cgp.NodeType{
		{Tag: TypeBrowser, Container: true},
		{Tag: TypeFolder, Container: true},
		{Tag: TypeBookmark, MimeType: "application/json"},
	}
}

func (*Plugin) DescribeFacets() []cgp.NodeTypeFacets {
	return []cgp.NodeTypeFacets{{
		Tag: TypeBookmark,
		Dimensions: []cgp.FacetDimension{
			{Key: FacetFolder, Label: "Folder", Kind: cgp.FacetCategorical},
			{Key: FacetDomain, Label: "Domain", Kind: cgp.FacetCategorical},
			{Key: FacetDateAdded, Label: "Date added", Kind: cgp.FacetDate},
		},
	}}
}

func (p *Plugin) Roots(ctx context.Context) ([]*url.URL, error) {
	ids, err := p.src.Browsers(ctx)
	if err != nil {
		return nil, err
	}

	sort.Strings(ids)

	roots := make([]*url.URL, 0, len(ids))
	for _, id := range ids {
		roots = append(roots, &url.URL{Scheme: Scheme, Host: id, Path: "/"})
	}

	return roots, nil
}

func (p *Plugin) ListRoots(
	ctx context.Context,
	node *url.URL,
) ([]cgp.Node, error) {
	idx, err := p.index(ctx, node)
	if err != nil {
		return nil, err
	}

	if isBrowserRoot(node) {
		return idx.childNodes(nil), nil
	}

	entry, err := idx.resolve(node)
	if err != nil {
		return nil, err
	}

	if entry.node.Kind != browser_items.BookmarkKindFolder {
		return nil, nil
	}

	return idx.childNodes(entry), nil
}

func (p *Plugin) ReadLeaf(
	ctx context.Context,
	node *url.URL,
) (content cgp.LeafContent, ok bool, err error) {
	if isBrowserRoot(node) {
		return content, false, nil
	}

	idx, err := p.index(ctx, node)
	if err != nil {
		return content, false, err
	}

	entry, err := idx.resolve(node)
	if err != nil || entry.node.Kind != browser_items.BookmarkKindBookmark {
		return content, false, nil
	}

	n := entry.node
	structured := map[string]any{
		"id":       n.Id,
		"title":    n.Title,
		"url":      n.Url,
		"folder":   entry.folder(),
		"browser":  idx.browserId,
		"parentId": n.ParentId,
		"index":    n.Index,
	}

	if n.DateAdded != nil {
		structured["dateAdded"] = *n.DateAdded
		structured["dateAddedISO"] = time.UnixMilli(*n.DateAdded).UTC().Format(time.RFC3339)
	}

	return cgp.LeafContent{Structured: structured}, true, nil
}

func isBrowserRoot(node *url.URL) bool {
	return node.Query().Get("id") == "" &&
		strings.Trim(node.EscapedPath(), "/") == ""
}

func (p *Plugin) index(ctx context.Context, node *url.URL) (*treeIndex, error) {
	if node == nil || node.Scheme != Scheme || node.Host == "" {
		return nil, fmt.Errorf("bookmark: want %s://<browser>/..., got %v", Scheme, node)
	}

	tree, err := p.src.Tree(ctx, node.Host)
	if err != nil {
		return nil, fmt.Errorf("bookmark: browser %q: %w", node.Host, err)
	}

	return newTreeIndex(node.Host, tree), nil
}

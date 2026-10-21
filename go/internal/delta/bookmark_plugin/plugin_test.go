package bookmark_plugin

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"testing"
	"time"

	"code.linenisgreat.com/cutting-garden/pkgs/traversal_serve"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
)

type fakeSource struct {
	trees map[string]browser_items.BookmarkTree
}

func (f fakeSource) Browsers(context.Context) ([]string, error) {
	var ids []string
	for id := range f.trees {
		ids = append(ids, id)
	}

	return ids, nil
}

func (f fakeSource) Tree(_ context.Context, id string) (browser_items.BookmarkTree, error) {
	t, ok := f.trees[id]
	if !ok {
		return t, context.DeadlineExceeded
	}

	return t, nil
}

func ptrI(i int) *int       { return &i }
func ptrS(s string) *string { return &s }
func ptrD(d int64) *int64   { return &d }

// 1700000000000 ms = 2023-11-14T22:13:20Z
func fixtureTree() browser_items.BookmarkTree {
	return browser_items.BookmarkTree{Roots: []browser_items.BookmarkNode{
		{
			Id: "menu________", Kind: "folder", Title: "Bookmarks Menu", Root: "menu",
			Unmodifiable: true,
		},
		{
			Id: "toolbar_____", Kind: "folder", Title: "Bookmarks Toolbar", Root: "toolbar",
			Unmodifiable: true,
			Children: []browser_items.BookmarkNode{
				{
					Id: "f1", ParentId: ptrS("toolbar_____"), Index: ptrI(0), Kind: "folder", Title: "reading",
					Children: []browser_items.BookmarkNode{
						{
							Id: "b1", ParentId: ptrS("f1"), Index: ptrI(1), Kind: "bookmark", Title: "later",
							Url: "https://Example.COM:8443/a?x=1", DateAdded: ptrD(1700000000000),
						},
						{Id: "s1", ParentId: ptrS("f1"), Index: ptrI(0), Kind: "separator"},
						{
							Id: "b2", ParentId: ptrS("f1"), Index: ptrI(2), Kind: "bookmark", Title: "a/b slash",
							Url: "javascript:void(0)",
						},
					},
				},
			},
		},
	}}
}

func newPlugin() *Plugin {
	return New(fakeSource{trees: map[string]browser_items.BookmarkTree{"firefox-main": fixtureTree()}})
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()

	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}

	return u
}

func TestRootsOnePerBrowser(t *testing.T) {
	roots, err := newPlugin().Roots(context.Background())
	if err != nil || len(roots) != 1 || roots[0].String() != "bookmark://firefox-main/" {
		t.Fatalf("roots = %v, err = %v", roots, err)
	}
}

func TestListBrowserRootThenFolders(t *testing.T) {
	p := newPlugin()
	ctx := context.Background()

	top, err := p.ListRoots(ctx, mustURL(t, "bookmark://firefox-main/"))
	if err != nil {
		t.Fatal(err)
	}

	if len(top) != 2 || top[0].Name != "Bookmarks Menu" || top[1].URIString() != "bookmark://firefox-main/toolbar?id=toolbar_____" {
		t.Fatalf("top = %+v", top)
	}

	if top[1].Type != TypeFolder {
		t.Fatalf("root folder type = %q", top[1].Type)
	}

	// descend by the minted URI, then by id alone
	for _, uri := range []string{top[1].URIString(), "bookmark://firefox-main/x?id=toolbar_____"} {
		kids, err := p.ListRoots(ctx, mustURL(t, uri))
		if err != nil || len(kids) != 1 || kids[0].Name != "reading" {
			t.Fatalf("%s: kids = %+v, err = %v", uri, kids, err)
		}
	}
}

func TestSeparatorsHiddenAndOrderedByIndex(t *testing.T) {
	kids, err := newPlugin().ListRoots(context.Background(), mustURL(t, "bookmark://firefox-main/toolbar/reading"))
	if err != nil {
		t.Fatal(err)
	}

	if len(kids) != 2 || kids[0].Name != "later" || kids[1].Name != "a/b slash" {
		t.Fatalf("kids = %+v", kids)
	}

	if got := kids[1].URIString(); got != "bookmark://firefox-main/toolbar/reading/a%2Fb%20slash?id=b2" {
		t.Fatalf("escaped uri = %q", got)
	}

	// the minted URI resolves back to the same node
	leaf, ok, err := newPlugin().ReadLeaf(context.Background(), mustURL(t, kids[1].URIString()))
	if err != nil || !ok || leaf.Structured.(map[string]any)["id"] != "b2" {
		t.Fatalf("leaf = %+v ok=%v err=%v", leaf, ok, err)
	}
}

func TestFacets(t *testing.T) {
	kids, _ := newPlugin().ListRoots(context.Background(), mustURL(t, "bookmark://firefox-main/toolbar/reading"))

	b1 := kids[0]
	if b1.Type != TypeBookmark {
		t.Fatalf("type = %q", b1.Type)
	}

	want := map[string]string{FacetFolder: "toolbar/reading", FacetDomain: "example.com", FacetDateAdded: "2023-11-14"}
	for k, v := range want {
		if got := b1.Facets[k]; len(got) != 1 || got[0].Key != v {
			t.Errorf("facet %s = %v, want %q", k, got, v)
		}
	}

	if got := kids[1].Facets[FacetDomain]; got[0].Key != noHostDomain {
		t.Errorf("javascript: url domain = %v", got)
	}

	if _, ok := kids[1].Facets[FacetDateAdded]; ok {
		t.Errorf("node without dateAdded must carry no date_added facet")
	}
}

func TestReadLeafRefusesFolders(t *testing.T) {
	_, ok, err := newPlugin().ReadLeaf(context.Background(), mustURL(t, "bookmark://firefox-main/toolbar?id=toolbar_____"))
	if err != nil || ok {
		t.Fatalf("folder read: ok=%v err=%v", ok, err)
	}
}

func TestUnknownBrowserAndNode(t *testing.T) {
	p := newPlugin()

	if _, err := p.ListRoots(context.Background(), mustURL(t, "bookmark://chrome-gone/")); err == nil {
		t.Error("unknown browser must error")
	}

	if _, err := p.ListRoots(context.Background(), mustURL(t, "bookmark://firefox-main/x?id=nope")); err == nil {
		t.Error("unknown id must error")
	}
}

// TestWireSession drives the plugin through the real RFC 0013 server over an
// in-memory stream: initialize, then nodes.list and leaf.read.
func TestWireSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hostSide, pluginSide := net.Pipe()

	done := make(chan error, 1)
	go func() {
		done <- traversal_serve.Serve(ctx, pluginSide, traversal_serve.ServeConfig{
			Plugin: newPlugin(),
			Info:   traversal_serve.PluginInfo{Name: "chrest", Version: "test"},
		})
	}()

	peer := traversal_serve.NewPeer(hostSide)
	defer peer.Close()

	var init traversal_serve.InitializeResult
	if err := peer.Call(ctx, traversal_serve.MethodInitialize,
		traversal_serve.InitializeParams{ProtocolVersions: []string{traversal_serve.SchemaV1}}, &init); err != nil {
		t.Fatal(err)
	}

	if len(init.Schemes) != 1 || init.Schemes[0] != Scheme || len(init.NodeTypes) != 3 || len(init.Facets) != 1 {
		t.Fatalf("initialize = %+v", init)
	}

	var list traversal_serve.NodesListResult
	if err := peer.Call(ctx, traversal_serve.MethodNodesList,
		traversal_serve.NodesListParams{URI: "bookmark://firefox-main/toolbar/reading"}, &list); err != nil {
		t.Fatal(err)
	}

	if len(list.Nodes) != 2 || list.Nodes[0].Facets[FacetDomain][0].Key != "example.com" {
		t.Fatalf("nodes.list = %+v", list)
	}

	var leaf traversal_serve.LeafReadResult
	if err := peer.Call(ctx, traversal_serve.MethodLeafRead,
		traversal_serve.LeafReadParams{URI: list.Nodes[0].URI}, &leaf); err != nil {
		t.Fatal(err)
	}

	var structured map[string]any
	if err := json.Unmarshal(leaf.Structured, &structured); err != nil || !leaf.OK || structured["url"] != "https://Example.COM:8443/a?x=1" {
		t.Fatalf("leaf.read = %+v (%v)", leaf, err)
	}

	_ = peer.Notify(traversal_serve.MethodShutdown, nil)
}

package browser_items

import (
	"encoding/json"
	"testing"
)

func TestBookmarkTreeDecode(t *testing.T) {
	const body = `{"browser":{"browser":"firefox","id":"x"},"roots":[
	  {"id":"toolbar_____","parentId":"root________","index":1,"kind":"folder",
	   "title":"Bookmarks Toolbar","dateAdded":null,"unmodifiable":true,"root":"toolbar",
	   "children":[
	     {"id":"b1","parentId":"toolbar_____","index":0,"kind":"bookmark","title":"a",
	      "url":"https://example.com/a","dateAdded":1700000000000,"unmodifiable":false}]}]}`

	var tree BookmarkTree
	if err := json.Unmarshal([]byte(body), &tree); err != nil {
		t.Fatal(err)
	}

	if len(tree.Roots) != 1 || tree.Roots[0].Root != "toolbar" || !tree.Roots[0].Unmodifiable {
		t.Fatalf("unexpected roots: %+v", tree.Roots)
	}

	if tree.Roots[0].DateAdded != nil {
		t.Fatalf("null dateAdded should decode to nil, got %v", *tree.Roots[0].DateAdded)
	}

	if len(tree.Roots[0].Children) != 1 {
		t.Fatalf("expected one child, got %d", len(tree.Roots[0].Children))
	}

	bm := tree.Roots[0].Children[0]
	if bm.Kind != BookmarkKindBookmark || bm.Url != "https://example.com/a" {
		t.Fatalf("unexpected bookmark: %+v", bm)
	}

	if bm.DateAdded == nil || *bm.DateAdded != 1700000000000 {
		t.Fatalf("unexpected dateAdded: %v", bm.DateAdded)
	}
}

package browser_items

import (
	"context"
	"encoding/json"
	"net/http"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// Bookmark node kinds in the normalized tree (extension/src/bookmark_tree.js).
const (
	BookmarkKindFolder    = "folder"
	BookmarkKindBookmark  = "bookmark"
	BookmarkKindSeparator = "separator"
)

// BookmarkNode is one node of GET /bookmarks/tree. Url is set only for
// bookmarks, Root only on top-level folders, Children only on folders.
// DateAdded is milliseconds since the epoch, nil when the browser did not
// report one.
type BookmarkNode struct {
	Id           string         `json:"id"`
	ParentId     *string        `json:"parentId"`
	Index        *int           `json:"index"`
	Kind         string         `json:"kind"`
	Title        string         `json:"title"`
	Url          string         `json:"url,omitempty"`
	DateAdded    *int64         `json:"dateAdded"`
	Unmodifiable bool           `json:"unmodifiable"`
	Root         string         `json:"root,omitempty"`
	Children     []BookmarkNode `json:"children,omitempty"`
}

// BookmarkTree is the GET /bookmarks/tree response for one browser: its
// top-level folders (bar/other/mobile on Chrome, toolbar/menu/unfiled/mobile
// on Firefox).
type BookmarkTree struct {
	Roots []BookmarkNode `json:"roots"`
}

func (BookmarkTree) MakeHTTPRequest(
	ctx context.Context,
) (req *http.Request, err error) {
	if req, err = http.NewRequestWithContext(
		ctx,
		"GET",
		"/bookmarks/tree",
		nil,
	); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

// GetBookmarkTree reads the normalized bookmark tree from exactly one
// browser socket.
func (b BrowserProxy) GetBookmarkTree(
	ctx context.Context,
	sock string,
) (tree BookmarkTree, err error) {
	var httpReq *http.Request

	if httpReq, err = (BookmarkTree{}).MakeHTTPRequest(ctx); err != nil {
		err = errors.Wrap(err)
		return
	}

	var resp *http.Response

	if resp, err = b.httpRequestFor(ctx, httpReq, sock); err != nil {
		err = errors.Wrap(err)
		return
	}

	defer errors.DeferredCloser(&err, resp.Body)

	if resp.StatusCode != http.StatusOK {
		err = errors.Errorf("GET /bookmarks/tree: status %d", resp.StatusCode)
		return
	}

	if err = json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

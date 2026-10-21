# `chrest traversal-serve` (scheme `bookmark:`): design, read-only v1

Status: design note, nothing implemented. Written 2026-10-21 for the
cutting-garden bookmarks plugin
(`cutting-garden/docs/plans/2026-10-21-browser-bookmarks-plugin-research.md`).
Decided by the user and not reopened here: v1 is read-only; folders only (no
Firefox tags/keywords); one root per browser instance
(`bookmark://<browser>-<name>/`). Facts below cite a file; `(guess)` marks
inference with no source behind it.

## 1. Verification of the research worker's claims about chrest

Checked by reading code at repo HEAD `c78577d`. Nothing was run and no browser
profile was touched.

| #   | Claim                                                                      | Verdict                                                 | Evidence                                                                                                                                                                                                                                                                                                                                                                                                     |
| --- | -------------------------------------------------------------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | Both manifests declare `bookmarks`                                         | CONFIRMED                                               | `extension/manifest-chrome.json:6`, `manifest-firefox.json:6`                                                                                                                                                                                                                                                                                                                                                |
| 2   | `GET /items` returns bookmarks flat                                        | CONFIRMED                                               | `extension/src/items.js` `allBookmarkItems` (`bookmarks.search({})`) and `urlItemForBookmark` emit only `{title, id{browser,id,type}, url, date}`; Go `browser_items.Item` is `{Id, Url, Date, Title, ExternalId}` (`go/internal/charlie/browser_items/item.go`). No parentId, index, folder kind or tags.                                                                                                   |
| 3   | `GET /bookmarks` is raw `getTree`, unconsumed in Go                        | CONFIRMED                                               | `extension/src/routes.js` (`Routes["/bookmarks"]`, `browser.bookmarks.getTree()`); no `/bookmarks` literal anywhere under `go/` (grep; the Go client special-cases only `/items`, `go/internal/delta/client/main.go:117`).                                                                                                                                                                                   |
| 4   | Create is broken (undeclared `result`)                                     | CONFIRMED (by reading)                                  | `items.js` `makeUrlItemBookmarks`: `Object.assign(result, ...)` with no `result` in scope, so the first element throws ReferenceError (ES modules are strict). Also: `bookmarks.create` is not awaited (a Promise reaches `urlItemForBookmark`), the map returns nothing, and there is no `parentId`/`index`.                                                                                                |
| 5   | Delete is `bookmarks.remove` only; no move/update/folder-create/removeTree | CONFIRMED                                               | The only `browser.bookmarks.*` calls under `extension/src` are `search`, `getTree`, `create`, `remove` (`items.js`, `lib.js`, `routes.js`).                                                                                                                                                                                                                                                                  |
| 6   | PUT browser-id filter is a no-op                                           | CONFIRMED                                               | `lib.isOurBid` is `async` (`lib.js:1`); `routes.js` passes it straight to `Array.prototype.filter`, which sees a truthy Promise. Nuance: `removeUrlItems` re-checks the id itself, `makeUrlItems`/`focusUrlItems` do not. Whether a PUT "fans out to every browser" depends on the Go proxy, not on this filter; the filter's own job (drop foreign-browser items) is what is broken.                        |
| 7   | MCP `items-put` delete probably doesn't work                               | CONFIRMED (by reading, not run), and worse than guessed | `go/internal/delta/tools/items.go` builds `Item{ExternalId: item.ID}`; `Item.Id` stays zero, and the extension never reads `external_id`. Delete: `removeUrlItems` sees an empty browser id, filters the item out, returns `deleted: []`. Add: `getItemType` returns `""` (not `undefined`) for an empty `id.type`, so `Object.groupBy` puts the item in a group `makeUrlItems` ignores; nothing is created. |
| 8   | Per-browser unix socket, no auth beyond permissions                        | CONFIRMED                                               | `go/internal/charlie/server/main.go`: `MkdirAll(dir, 0o700)`, `net.ListenUnix`, plain HTTP, no token. Socket file mode is whatever the umask gives (not verified); the 0700 directory is the guard.                                                                                                                                                                                                          |
| 9   | go.nix bridges cutting-garden; capture-serve exists; no traversal plugin   | CONFIRMED                                               | `go/go.nix:6-7`; `go/cmd/chrest/main.go:154`; no `traversal` identifier in `go/**/*.go`.                                                                                                                                                                                                                                                                                                                     |
| –   | One stanza cannot declare two protocols                                    | CONFIRMED                                               | `cutting-garden/internal/command_components/roots.go:140-151` returns a bad-request error when a stanza wants both. RFC 0013 §Host integration says the same.                                                                                                                                                                                                                                                |

### `search({})` / `getTree` shape: Firefox OBSERVED, Chrome UNSURE

Firefox 150 was observed in a throwaway headless profile with fixture
bookmarks (`just explore-bookmarks-shape`): getTree is one unnamed
`root________` whose 4 children are, in order, `menu________`,
`toolbar_____`, `unfiled_____`, `mobile______`; non-root ids are 12-char
guids; nodes carry `type` and `parentId`; a separator has `type: separator`,
an empty title and a data: url. `search({})` returned a flat list of every
node with no `children`: the four roots, a fifth `tags________` root (absent
from getTree), folders, bookmarks and separators. chrest's filter in
`allBookmarkItems` therefore keeps all of them on Firefox (15 of 15 in the
run), so `/items` lists folders, separators and the tags root as bookmark
items (filed as a separate bug). Chrome was not observed (no Chrome in the
devshell); the bullets below remain docs-based for Chrome.

- `getTree()` returns a one-element array: an invisible root whose `children`
  are the top-level folders. Chrome: bookmarks bar, other bookmarks, mobile
  (`folderType` `bookmarks-bar` / `other` / `mobile`; the numeric ids `1`/`2`/`3`
  are conventional (guess), key on `folderType`). Firefox: root guid
  `root________` with children `menu________`, `toolbar_____`, `unfiled_____`,
  `mobile______` (research doc §2, MDN). Firefox nodes carry
  `type: bookmark|folder|separator`; Chrome nodes carry no `type`, folders are
  the nodes without `url`.
- `search({})` returns a flat list with no `children`. chrest's own filter
  `b.children === undefined || b.type === "bookmark"` (`items.js` `allBookmarkItems`)
  would therefore let Chrome folders through as url-less "bookmark" items, and
  would let Firefox separators through only if search returns them (guess; both
  would show up as items with `url` undefined).
- Chrome remains to be observed; the recipe above is Firefox-only because the
  devshell has no Chrome/Chromium.

## 2. Read route

Do not change `GET /bookmarks` (undocumented but present, raw). Add
`GET /bookmarks/tree`: the extension walks `getTree()` and emits a normalized,
browser-independent document:

```json
{ "browser": {"browser": "firefox", "id": "main"},
  "roots": [ { "id": "toolbar_____", "root": "toolbar", "kind": "folder",
               "title": "", "index": 0, "dateAdded": 1700000000000,
               "unmodifiable": false, "children": [ ... ] } ] }
```

Per node: `id` (string, browser-native), `parentId`, `index`, `kind`
(`folder` | `bookmark` | `separator`), `title`, `url` (bookmarks only),
`dateAdded` (ms), `unmodifiable` (`"managed"` or root), `root` (normalized root
name on top-level folders only: `bar|other|mobile` for Chrome,
`toolbar|menu|unfiled|mobile` for Firefox). `kind` is derived: Firefox `type`,
else Chrome `url` present means bookmark, else folder. The extension, not Go,
does the normalization, so the Go client stays browser-agnostic. Add a Go
client function and an `app`/CLI surface only if something needs it; the plugin
can call the route through the existing socket client.

## 3. `chrest traversal-serve` mapping

Bypass dewey like `capture-serve` (`go/cmd/chrest/main.go:154`). Bring-up with
`pkgs/traversal_serve`: `CookieFromEnv` (`CookieEnv`), `ListenRendezvous`,
print `AnnounceLine`, accept, `Serve` with a plugin implementing the
capability set below, plus the same stdin-EOF/SIGTERM lifecycle wrapper
`capture_serve.go` uses.

Addressing, one socket per browser: `roots.list` enumerates
`$XDG_STATE_HOME/chrest/*.sock` (the state directory the server creates),
probes each with `GET /`, and returns one root `bookmark://<browser>-<id>/` per
live socket, skipping dead ones with one stderr line. Every call carries the
URI's browser id, so it dials exactly that socket; there is no fan-out
(`proxy.GetSocketPathForBrowserId` already exists). Optional config section
`[bookmark]` with an allowlist of browser ids; credentials do not exist here.

Tree (types; separators hidden in v1):

```
bookmark://<bid>/                       bookmark-browser-v1     (root)
bookmark://<bid>/<root>                  bookmark-folder-v1      (bar|other|mobile / toolbar|menu|unfiled|mobile)
bookmark://<bid>/<root>/<folder...>      bookmark-folder-v1
bookmark://<bid>/<root>/<folder...>/<title>?id=<id>   bookmark-v1 (leaf)
```

Titles repeat and may contain `/`, so the path is display and the `?id=` is
the identity for folders and leaves alike (the research doc's ynab-style
convention); `nodes.list` resolves a container by id when present, by path
otherwise, and the leaf body is the bookmark's JSON (`id`, `title`, `url`,
`dateAdded`, `parentId`, `index`). `nodes.list` fetches `/bookmarks/tree` once
and caches it for a short TTL, because the extension has no change token
(`facets.version` is not advertised, so the host falls back to its TTL).

Capabilities: `roots`, `leaf-read`. `facet-counts` is optional; the host folds
over `nodes.list` otherwise, which is fine at personal-bookmark volumes.

Facets (all derivable from the one tree document, all read-only in v1):

- `folder`: slash-joined ancestor folder titles, single-valued. Declared now
  with write mode `none`; the same dimension becomes the `one`-mode move target
  later.
- `domain`: URL host (lowercased, `www.` kept as-is, a guess at what users want
  is no stripping), single-valued, open domain.
- `date_added`: declared with cutting-garden's `FacetDate` kind (changed from
  the relative bands first sketched here): the bucket key is the ISO day
  ("2026-08-15") and the host coarsens to month/year by key prefix, so it is
  not volatile and needs no `revalidate_after_seconds`. Implemented in
  `go/internal/delta/bookmark_plugin` (chrest#125).

Dedicated `folder` listing fields (title, url, dateAdded) ride the enriched
listing; no `tags` dimension in v1.

## 4. Config stanza

Beside the existing `web` capture stanza, not merged into it:

```toml
[[plugins]]
name = "web"
command = ["chrest"]
schemes = ["web"]
protocols = ["capture"]

[[plugins]]
name = "bookmark"
command = ["chrest"]          # base argv; host appends "traversal-serve"
schemes = ["bookmark"]
protocols = ["traversal"]
```

Names and schemes must be unique across stanzas (`ValidateStanzas`); the host
refuses a single stanza with both protocols (`roots.go:140-151`). Using
`[[plugins]]` (base argv) rather than `[[traversal_plugins]]` (full argv)
matches the `web` stanza. Section for `config_toml` defaults to the stanza name
(`[bookmark]`). The user's cutting-garden home-manager module has a
`{ name; command; schemes; protocols; }` shape for this
(`cutting-garden/docs/features/0019-nixos-home-manager-modules.md`).

## 5. What the write routes need (for cutting-garden's follow-up issues)

All additive on the extension; the socket already scopes to one browser, so the
new routes need no `isOurBid` filtering (the existing PUT bug does not block
them, but should still be fixed).

- `PATCH /bookmarks/#ID` `{title?, url?}` -> `bookmarks.update` (rename, change URL).
- `POST /bookmarks/#ID/move` `{parentId, index?}` -> `bookmarks.move`
  (re-folder, reorder). Firefox and Chrome disagree on `index` semantics
  after a same-parent move (guess; verify per browser).
- `POST /bookmarks` `{parentId, index?, title, url?}` -> `bookmarks.create`
  (folder when `url` absent); replaces the broken PUT /items create.
- `DELETE /bookmarks/#ID` -> `bookmarks.remove`; non-empty folder refused by the
  browser; `DELETE /bookmarks/#ID?tree=1` -> `bookmarks.removeTree`, which
  cutting-garden may choose never to expose.
- Guards chrest should enforce server-side: reject `unmodifiable` nodes and
  roots; do a fresh `bookmarks.get` before each write and return the read-back
  node (parentId/index/title) so the plugin can verify.
- Go: a client method per route, and `node.patch`/`node.create_child`/
  `node.delete` handlers in traversal-serve behind the `mutate`,
  `container-create` capabilities; `known_empty_values` on `folder` so empty
  folders are drop targets.
- Not available at all from the extension API: Firefox tags/keywords (user
  already ruled them out), bookmark guids on Chrome.

## 6. Chrest-owned questions

Should chrest grow a bookmarks API and `traversal-serve`? Yes (recommendation,
the user decides). chrest owns the browser knowledge, both sockets, the
extension and the cutting-garden bridge, and the alternatives push chrest's
socket layout into cutting-garden or read profile files the browser rewrites.

Suggested order:

1. `isOurBid` async fix: a one-line change and a correctness prerequisite for
   anything that writes. Independent of reads.
2. Tree read route + Go client (the feature issue). The only hard prerequisite
   for the plugin.
3. `chrest traversal-serve` + `[[plugins]]` stanza, read-only (this note).
4. Write slice: new `/bookmarks` write routes (section 5) and plugin mutate
   handlers. The broken PUT /items create and the MCP `items-put` id mismatch
   are bugs in a different surface; fix them on their own track. They do not
   block read-only v1, and the write slice should not build on PUT /items.

chrest-bookmark-plugin(7) "2026-10-21" "chrest"

# NAME

chrest-bookmark-plugin - read-only bookmark: traversal plugin for chrest

# STATUS

Read-only v1 (chrest#125), implemented by _chrest traversal-serve_. Code is
in go/internal/(level)/bookmark_plugin. Design is in
_docs/plans/2026-10-21-bookmark-traversal-serve-design.md_.

- Tested on Chrome and on Firefox with real profiles, 2026-10-21.
- NOT tested: separators on Firefox, two browsers changing actively at once,
  Windows and macOS.

# CONFIGURATION

Add a separate cutting-garden _[[plugins]]_ stanza beside the _web_ capture
stanza:

```
[[plugins]]
name = "bookmark"
command = ["chrest"]
schemes = ["bookmark"]
protocols = ["traversal"]
```

It must be separate from the _web_ stanza because cutting-garden rejects a
stanza that declares both protocols. There is one root per live browser
socket: _bookmark://<browser>-<name>/_.

# TREE SHAPE

- Roots are bar, other and mobile on Chrome; menu, toolbar, unfiled and
  mobile on Firefox.
- The query parameter _?id=_ is the node identity. Titles repeat and may
  contain slashes, so they are percent-encoded in paths.
- Separators are hidden.

# FACETS

- _folder_
- _domain_
- _date_added_, a FacetDate whose keys are ISO days, so a month or year
  prefix filter works, for example _date_added=2025-05_.

# WHY THE PLUGIN IMPLEMENTS FacetCounter ITSELF

Before cutting-garden master 19bdee4 there was no framework fold. A plugin
without facet counts got "no facet summary available at this node" on every
_read_facets_ (cutting-garden#124). chrest therefore answers _facets.counts_
from its in-memory tree (_go/internal/delta/bookmark_plugin/facet_counts.go_):
containers report complete=true, leaves report ok=false.

The fold now exists upstream, but chrest keeps its own because it avoids up
to 200 ListRoots round trips.

# KNOWN BEHAVIOURS

Each item is marked observed, source-read or unverified.

- SOURCE-READ: the host memoizes unfiltered facet summaries for tokenless
  plugins (_internal/mcp/facet_cache.go_: _facetTTL_ is 15 minutes, refresh
  interval 5 minutes).
- OBSERVED: after a bookmark sync, unfiltered _read_facets_ showed 4
  bookmarks while the extension held 1,263, with freshness "unverified" and
  an old _computedAt_. A FILTERED read computed on demand and was correct.
  Tracked as cutting-garden#345 (exploration), related #133, #135, #188.
- OBSERVED: the top-level root list did not show a browser whose socket
  appeared after the MCP server started, until the server was restarted.
  Plugin-side discovery is a fresh glob plus dial probe on every call, so
  the list is held host-side. UNVERIFIED: the host code was not read.
- OBSERVED: after rebuilding chrest, the long-running cutting-garden MCP
  server keeps a plugin session on the OLD binary until restarted;
  _read_facets_ kept failing until _restart cutting-garden-dev_.
- SOURCE-READ: the plugin's tree cache TTL is 5 seconds (SocketSource).
- OBSERVED: stderr noise "traversal-serve session: context canceled" on a
  clean session (chrest#128, cutting-garden#342).

# DEBUGGING

- _explore-cg-list <uri> [lines]_: runs cutting-garden list with the session
  config in _.tmp/cg-config_.
- _explore-client GET /bookmarks/tree_: shows the raw extension route.
- A session-local _XDG_CONFIG_HOME_ holding only the web and bookmark
  stanzas keeps real account stanzas out of the session.

# SEE ALSO

*chrest*(1), *chrest-firefox-native-messaging*(7), *cutting-garden*(1),
*cutting-garden-config*(5), *cutting-garden-plugins*(7)

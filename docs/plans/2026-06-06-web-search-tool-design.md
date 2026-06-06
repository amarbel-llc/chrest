# web-search MCP tool — design

Date: 2026-06-06
Status: approved

## Summary

Add a `web-search` MCP tool to chrest: a derivative of the existing
`web-fetch` tool specialized for performing web searches. It queries a
search backend (DuckDuckGo HTML first), returns a structured result
list, and pre-fetches the top N result pages through the existing
web-fetch dispatch pipeline so they are immediately readable via
`read-resource` and cached for subsequent `web-fetch` calls.

## Decisions

- **Backend: DuckDuckGo HTML** (`https://html.duckduckgo.com/html/?q=...`)
  first, behind a `Backend` interface so SearXNG and a full-JS engine
  (Firefox-driven mainstream SERP scraping) can slot in later. Backend
  selection via `CHREST_WEB_SEARCH_BACKEND` env var (`ddg-html` is the
  only value initially).
- **Output: result list + auto-fetch top N.** The SERP is parsed into
  structured results; the top N (default 3) are pre-fetched
  sequentially, best-effort, through the existing dispatch path,
  populating the shared fetch cache.
- **SERP fetch via plain `net/http`.** The DDG HTML endpoint exists for
  no-JS clients; no Firefox session is needed for the search step.
  Firefox only spins up for the prefetches.
- **Caching: per-query session cache** keyed by raw query string,
  symmetric with web-fetch's per-URL cache, with `refresh: true` to
  bypass.

## Architecture

New package `go/internal/<level>/websearch` (dagnabit assigns the
level):

```go
type Result struct {
    Title   string
    URL     string
    Snippet string
}

type Backend interface {
    Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}
```

`ddghtml.go` implements the DDG backend: URL construction, SERP parsing
with `golang.org/x/net/html` + `cascadia` (both already in the module
via `*/markdown`), `uddg=` redirect-parameter decoding to recover real
result URLs, `maxResults` capping.

The MCP tool handler lives in `go/cmd/chrest/main.go` beside web-fetch,
sharing `fetchCache` for prefetched pages.

### Data flow

```
web-search(query)
  → check searchCache[query]            (miss ↓)
  → backend.Search()                    (DDG HTML GET + parse)
  → store searchCache[query]
  → for top N=3 results: prefetch via fetchViaDispatch → fetchCache
  → emit: inline result list (markdown or json) + resource_links
```

## Tool schema

```
name: web-search
annotations: readOnlyHint: true
input:
  query        string  (required) — the search query
  max_results  int     (optional, default 10) — cap on parsed results
  prefetch     int     (optional, default 3, 0 disables) — top-N results to pre-fetch
  format       string  (optional, enum: "markdown" [default], "json")
  refresh      boolean (optional, default false) — bypass the per-query cache
```

## Output shape

1. **Inline result list.** `format: markdown` renders:

   ```markdown
   # Search: <query>

   1. **<title>**
      <url>
      <snippet>
   ```

   Prefetched entries get a `[prefetched]` marker; failed prefetches get
   a one-line annotation (`[prefetch failed: <reason>]`) so the agent
   can tell whether a plain `web-fetch` retry would help.

   `format: json` renders instead:

   ```json
   {
     "query": "...",
     "results": [
       {
         "title": "...",
         "url": "...",
         "snippet": "...",
         "prefetched": true,
         "prefetch_error": null
       }
     ]
   }
   ```

   Both formats render from the same cached `[]Result`; a `format`
   switch never re-searches.

2. **One `resource_link` per successfully prefetched page** — URI
   `web-fetch://<url>` (same scheme web-fetch emits), description
   carries the result title, readable via `read-resource`.

**Zero-results case:** inline text distinguishing "DDG returned a page
but no results matched the parser" (probable markup drift — surfaced
loudly, never silently empty) from "DDG returned an actual empty result
set".

## Error handling

| Failure                                          | Behavior                                                                                    |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------- |
| DDG unreachable / network error                  | Tool error, wrapped underlying error                                                        |
| DDG non-2xx (rate limit, block page)             | Tool error with status code + first-1KB body preview (mirrors web-fetch's HTTP-error shape) |
| SERP parses to zero results but page has content | Inline diagnostic flagging probable markup drift                                            |
| Individual prefetch fails                        | Best-effort: annotate that result, search still succeeds                                    |
| Empty query                                      | Tool error before any network call                                                          |

Search errors are not cached (same as web-fetch: only success is
stored).

## Caching

- `searchCache sync.Map` in `main.go` beside `fetchCache`, keyed by raw
  query string, storing `searchCacheEntry{Results []websearch.Result,
FetchedAt time.Time}`.
- Key is query-only — `max_results` trims and `prefetch`/`format`
  render from the cached entry, so differing knobs on a repeat query
  don't re-hit DDG. Exception: a repeat call asking to prefetch a
  result not yet in `fetchCache` runs just those prefetches (search
  cache hit, fetch cache fill).
- `refresh: true` re-searches and re-prefetches with `refresh`
  semantics passed through to the fetch path.

## Rollback strategy

This adds a new tool rather than replacing infrastructure, so there is
no dual-architecture period in the web-fetch sense. The backend seam
(`CHREST_WEB_SEARCH_BACKEND`) is the forward-compatibility story.
Rollback for a misbehaving tool: agents simply don't call it; hard kill
is removing the registration, one commit. The DDG-markup-drift failure
mode is mitigated by the loud zero-results diagnostic plus fixture
tests.

## Tuning levers

| Lever                 | Current value            | Rationale                                                            | Change signal                                                                                      |
| --------------------- | ------------------------ | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `prefetch` default    | 3                        | One-round-trip benefit without long sequential Firefox waits         | Prefetches routinely wasted (agents read 1 result) → lower; agents always re-fetching #4-5 → raise |
| `max_results` default | 10                       | One DDG HTML page worth, compact context                             | Agents paginating often → raise                                                                    |
| Sequential prefetch   | sequential               | Matches one-session-at-a-time architecture                           | Wall-clock pain on prefetch=3 → revisit concurrent sessions                                        |
| SERP fetch User-Agent | normal browser UA string | DDG HTML serves no-JS clients; honest-ish UA avoids block heuristics | DDG blocks/captchas → revisit (Firefox-driven SERP fetch is the fallback)                          |
| Search cache scope    | session-lifetime         | Symmetric with fetchCache                                            | Stale-results complaints → TTL                                                                     |

## Testing

1. **Unit (fixture-driven):** a real saved DDG HTML SERP under
   `go/internal/<level>/websearch/testdata/`; tests cover parsing
   (titles, `uddg=` decoding, snippets, maxResults cap), zero-result
   detection, and drift diagnostics. Runs in `nix build` checkPhase.
2. **MCP surface:** extend `just test-mcp` — `web-search` appears in
   tools/list with `readOnlyHint: true`.
3. **BATS (network-dependent):** a `web_search.bats` case in
   `zz-tests_bats/` exercising a live query end-to-end, consistent with
   `capture_firefox.bats`'s accepted network dependence. Gate behind a
   tag if live-DDG flakiness becomes a problem.

// Package websearch implements web-search backends for the web-search
// MCP tool. The DDGHTML backend queries DuckDuckGo's no-JS HTML
// endpoint via plain net/http; the Backend interface is the seam for
// future backends (SearXNG, full-JS engine scraping). See
// docs/plans/2026-06-06-web-search-tool-design.md.
//
// Implementation is PARKED at the package-skeleton stage: see chrest#93
// for the full plan, the verified DDG SERP selectors, and the
// bot-challenge findings (testdata/serp_anomaly.html). Nothing imports
// this package yet.
package websearch

import "context"

// Result is a single parsed search result.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Backend performs a search and returns parsed results. maxResults <= 0
// means "everything the backend's result page yields".
type Backend interface {
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}

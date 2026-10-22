package bookmark_plugin

import (
	"context"
	"net/url"

	cgp "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
)

// FacetCounts answers facets.counts in one call (RFC 0012 §4): the whole
// tree is already in memory, so counting a subtree is a walk over the index.
//
// This is the fast path. Since cutting-garden 19bdee4 the host can fold
// Node.Facets over ListRoots itself for a plugin that omits facet-counts
// (cutting-garden#124); before that, read_facets on any bookmark: container
// failed with "no facet summary available at this node". The plugin keeps its
// own because the fold costs up to 200 ListRoots round trips per read. The two
// must stay semantically equal; there is no parity test yet.
//
// A bookmark leaf is not summarized (ok false); every container is, covering
// its whole subtree, so Complete is always true.
func (p *Plugin) FacetCounts(
	ctx context.Context,
	node *url.URL,
	filter cgp.FacetFilter,
) (result cgp.FacetResult, ok bool, err error) {
	// Arms date-kind predicates for prefix matching ("2026-08" matches
	// "2026-08-15"); the wire server hands the filter over unvalidated.
	if err = cgp.ValidateFilterFor(filter, p); err != nil {
		return result, false, err
	}

	idx, err := p.index(ctx, node)
	if err != nil {
		return result, false, err
	}

	start, ok, err := idx.summaryStart(node)
	if err != nil || !ok {
		return result, false, err
	}

	summary := cgp.FacetSummary{}
	tallyFacets(start, filter, summary)

	return cgp.FacetResult{Summary: summary, Complete: true}, true, nil
}

// summaryStart returns the entries whose subtrees a summary of node covers.
// ok is false when node is a bookmark, which has no subtree to summarize.
func (idx *treeIndex) summaryStart(node *url.URL) (start []*entry, ok bool, err error) {
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

// tallyFacets adds every bookmark under es that matches filter to summary.
func tallyFacets(es []*entry, filter cgp.FacetFilter, summary cgp.FacetSummary) {
	for _, e := range es {
		if e.node.Kind != browser_items.BookmarkKindBookmark {
			tallyFacets(e.children, filter, summary)
			continue
		}

		facets := facetsFor(e)
		if !filter.Matches(facets) {
			continue
		}

		for dim, values := range facets {
			if summary[dim] == nil {
				summary[dim] = cgp.FacetHistogram{}
			}

			for _, v := range values {
				summary[dim][v.Key]++
			}
		}
	}
}

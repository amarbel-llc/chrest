package bookmark_plugin

import (
	"context"
	"net/url"

	cgp "code.linenisgreat.com/cutting-garden/pkgs/cutting_garden_plugins"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
)

// FacetCounts answers facets.counts in one call (RFC 0012 §4): the whole
// tree is already in memory, so counting a subtree is a walk over the index.
// The host has no framework fold yet (cutting-garden#124), so without this a
// read_facets on any bookmark: container fails.
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

	var start []*entry

	if isBrowserRoot(node) {
		start = idx.roots
	} else {
		target, resolveErr := idx.resolve(node)
		if resolveErr != nil {
			return result, false, resolveErr
		}

		if target.node.Kind != browser_items.BookmarkKindFolder {
			return result, false, nil
		}

		start = target.children
	}

	summary := cgp.FacetSummary{}

	var walk func(es []*entry)
	walk = func(es []*entry) {
		for _, e := range es {
			if e.node.Kind == browser_items.BookmarkKindBookmark {
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

				continue
			}

			walk(e.children)
		}
	}

	walk(start)

	return cgp.FacetResult{Summary: summary, Complete: true}, true, nil
}

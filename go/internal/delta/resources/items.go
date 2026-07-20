package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
	"code.linenisgreat.com/chrest/go/internal/charlie/proxy"
	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/protocol"
)

const (
	itemsURI         = "chrest://items"
	itemsTemplateURI = "chrest://items/{page}"
	pageSize         = 100
	cacheTTL         = 30 * time.Second
)

// ItemResources implements server.ResourceProvider for paginated browser items.
type ItemResources struct {
	proxy      *proxy.BrowserProxy
	itemsProxy browser_items.BrowserProxy

	mu       sync.Mutex
	cached   []browser_items.Item
	cachedAt time.Time
}

func NewItemResources(p *proxy.BrowserProxy, itemsProxy browser_items.BrowserProxy) *ItemResources {
	return &ItemResources{proxy: p, itemsProxy: itemsProxy}
}

func (r *ItemResources) ListResources(ctx context.Context) ([]protocol.Resource, error) {
	return []protocol.Resource{
		{
			URI:         itemsURI,
			Name:        "Browser Items Index",
			Description: "Paginated index of all browser items (tabs, bookmarks, history)",
			MimeType:    "application/json",
		},
	}, nil
}

func (r *ItemResources) ListResourceTemplates(ctx context.Context) ([]protocol.ResourceTemplate, error) {
	return []protocol.ResourceTemplate{
		{
			URITemplate: itemsTemplateURI,
			Name:        "Browser Items Page",
			Description: "A page of browser items (100 per page)",
			MimeType:    "application/json",
		},
	}, nil
}

func (r *ItemResources) ReadResource(ctx context.Context, uri string) (*protocol.ResourceReadResult, error) {
	switch {
	case uri == itemsURI:
		return r.readIndex(ctx, uri)
	case strings.HasPrefix(uri, "chrest://items/"):
		return r.readPage(ctx, uri)
	default:
		return nil, fmt.Errorf("unknown resource: %s", uri)
	}
}

func (r *ItemResources) fetchAll(ctx context.Context) ([]browser_items.Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cached != nil && time.Since(r.cachedAt) < cacheTTL {
		return r.cached, nil
	}

	socks, err := r.proxy.GetSockets()
	if err != nil {
		return nil, err
	}

	resp, err := r.itemsProxy.GetForSockets(ctx, browser_items.BrowserRequestGet{}, socks)
	if err != nil {
		return nil, err
	}

	r.cached = resp.RequestPayloadGet
	r.cachedAt = time.Now()

	return r.cached, nil
}

type pageInfo struct {
	URI   string `json:"uri"`
	Page  int    `json:"page"`
	Count int    `json:"count"`
}

type indexResponse struct {
	Total    int        `json:"total"`
	PageSize int        `json:"page_size"`
	Pages    []pageInfo `json:"pages"`
}

func (r *ItemResources) readIndex(ctx context.Context, uri string) (*protocol.ResourceReadResult, error) {
	items, err := r.fetchAll(ctx)
	if err != nil {
		return nil, err
	}

	total := len(items)
	numPages := int(math.Ceil(float64(total) / float64(pageSize)))

	pages := make([]pageInfo, 0, numPages)
	for i := range numPages {
		start := i * pageSize
		end := start + pageSize
		if end > total {
			end = total
		}
		pages = append(pages, pageInfo{
			URI:   fmt.Sprintf("chrest://items/%d", i+1),
			Page:  i + 1,
			Count: end - start,
		})
	}

	idx := indexResponse{
		Total:    total,
		PageSize: pageSize,
		Pages:    pages,
	}

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return nil, err
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{
			{
				URI:      uri,
				MimeType: "application/json",
				Text:     string(data),
			},
		},
	}, nil
}

func (r *ItemResources) readPage(ctx context.Context, uri string) (*protocol.ResourceReadResult, error) {
	pageStr := strings.TrimPrefix(uri, "chrest://items/")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		return nil, fmt.Errorf("invalid page number: %s", pageStr)
	}

	items, err := r.fetchAll(ctx)
	if err != nil {
		return nil, err
	}

	total := len(items)
	start := (page - 1) * pageSize
	if start >= total {
		return nil, fmt.Errorf("page %d out of range (total items: %d)", page, total)
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	data, err := json.MarshalIndent(items[start:end], "", "  ")
	if err != nil {
		return nil, err
	}

	return &protocol.ResourceReadResult{
		Contents: []protocol.ResourceContent{
			{
				URI:      uri,
				MimeType: "application/json",
				Text:     string(data),
			},
		},
	}, nil
}

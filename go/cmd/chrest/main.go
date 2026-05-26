package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"code.linenisgreat.com/chrest/go/internal/alfa/prompter"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/command"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/protocol"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/server"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/transport"

	"code.linenisgreat.com/chrest/go/internal/0/markdown"
	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
	"code.linenisgreat.com/chrest/go/internal/alfa/rawfetch"
	"code.linenisgreat.com/chrest/go/internal/bravo/config"
	"code.linenisgreat.com/chrest/go/internal/charlie/browser_items"
	"code.linenisgreat.com/chrest/go/internal/charlie/proxy"
	"code.linenisgreat.com/chrest/go/internal/delta/resources"
	"code.linenisgreat.com/chrest/go/internal/delta/tools"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/stack_frame"
	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/ui"
)

// Populated at link time via `-X main.version` / `-X main.commit`
// (auto-injected by the amarbel-llc/nixpkgs fork's buildGoApplication
// when `version` and `commit` are passed to the derivation).
var (
	version = "dev"
	commit  = "unknown"
)

func init() {
	log.SetPrefix("chrest ")
}

func main() {
	ctx := errors.MakeContextDefault()
	ctx.SetCancelOnSignals(syscall.SIGTERM)

	if err := ctx.Run(
		func(ctx errors.Context) {
			if err := run(ctx); err != nil {
				ctx.Cancel(err)
			}
		},
	); err != nil {
		var normalError stack_frame.ErrorStackTracer

		if errors.As(err, &normalError) && !normalError.ShouldShowStackTrace() {
			ui.Err().Printf("%s", normalError.Error())
		} else {
			ui.Err().Print(err)
		}
		// Fall through to a non-zero exit so shell callers and CI can
		// detect the failure. Without this, `chrest capture ... > out`
		// quietly produced a zero-byte file with exit 0 when the browser
		// binary was missing.
		os.Exit(1)
	}
}

func run(ctx errors.Context) (err error) {
	var c config.Config

	if c, err = config.Default(); err != nil {
		err = errors.Wrap(err)
		return
	}

	if err = c.Read(); err != nil {
		err = errors.Wrap(err)
		return
	}

	p := &proxy.BrowserProxy{Config: c}

	app := command.NewUtility("chrest", "Manage browsers via REST")
	app.Version = version
	app.MCPArgs = []string{"mcp"}

	tools.RegisterAll(app, p)
	registerClientCommand(app, c)
	registerReloadExtensionCommand(app, c)
	registerInitCommand(app)
	registerInstallMCPCommand(app)
	registerGeneratePluginCommand(app)
	registerCaptureBatchCommand(app)
	registerVersionCommand(app)

	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err = runMCP(ctx, app, p); err != nil {
			err = errors.Wrap(err)
			return
		}
		return
	}

	// Bypass dewey for client command — variadic StringArg
	// serialization is broken (purse-first#44)
	if len(os.Args) > 1 && os.Args[1] == "client" {
		if err = cmdClient(c, os.Getenv("CHREST_BROWSER"), false, os.Args[2:]); err != nil {
			err = errors.Wrap(err)
			return
		}
		return
	}

	// Bypass dewey for init command — flag parsing may also
	// be affected (purse-first#44)
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err = cmdInitDirect(ctx); err != nil {
			err = errors.Wrap(err)
			return
		}
		return
	}

	// Bypass dewey for capture: the Result/TextResult path in golf/command
	// buffers everything and appends a trailing newline via fmt.Println,
	// which corrupts binary output and bloats memory. See chrest#21 and the
	// upstream dewey gap in amarbel-llc/purse-first#55.
	if len(os.Args) > 1 && os.Args[1] == "capture" {
		if err = cmdCapture(ctx, os.Args[2:]); err != nil {
			err = errors.Wrap(err)
			return
		}
		return
	}

	// Bypass dewey for capture-batch: the contract is JSON-on-stdin and
	// JSON-on-stdout per RFC 0001, neither of which fits the Result path.
	// Batch-level failures surface as non-zero exit via the outer error
	// handler in main() so the orchestrator distinguishes them from
	// per-capture errors in the output JSON.
	if len(os.Args) > 1 && os.Args[1] == "capture-batch" {
		if err = cmdCaptureBatch(ctx, app.Version, os.Args[2:]); err != nil {
			err = errors.Wrap(err)
			return
		}
		return
	}

	if err = app.RunCLI(ctx, os.Args[1:], prompter.Prompter{}); err != nil {
		err = errors.Wrap(err)
		return
	}

	return
}

func runMCP(ctx context.Context, app *command.Utility, p *proxy.BrowserProxy) error {
	t := transport.NewStdio(os.Stdin, os.Stdout)
	registry := server.NewToolRegistryV1()
	app.RegisterMCPToolsV1(registry)

	itemsProxy := browser_items.BrowserProxy{Config: p.Config}
	itemResources := resources.NewItemResources(p, itemsProxy)

	const (
		mimeText     = "text/plain; charset=utf-8"
		mimeMarkdown = "text/markdown; charset=utf-8"
		mimeHTML     = "text/html; charset=utf-8"
	)

	var fetchCache sync.Map // URL (no fragment) → *fetchCacheEntry

	// Bridge tool: exposes resources as a tool for subagent access
	registry.Register(
		protocol.ToolV1{
			Name:        "read-resource",
			Description: "Read a chrest resource by URI (e.g. chrest://items, chrest://items/1)",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"uri":{"type":"string","description":"Resource URI to read"}},"required":["uri"]}`),
			Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		},
		func(ctx context.Context, args json.RawMessage) (*protocol.ToolCallResultV1, error) {
			var p0 struct {
				URI string `json:"uri"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}
			if strings.HasPrefix(p0.URI, "web-fetch://") {
				url, fragment := splitWebFetchURI(p0.URI)
				if url == "" {
					return protocol.ErrorResultV1("invalid web-fetch URI: " + p0.URI), nil
				}
				v, ok := fetchCache.Load(url)
				if !ok {
					return protocol.ErrorResultV1("resource not found (page may need to be fetched first): " + p0.URI), nil
				}
				entry := v.(*fetchCacheEntry)
				switch fragment {
				case "text":
					return &protocol.ToolCallResultV1{
						Content: []protocol.ContentBlockV1{
							protocol.TextContentV1(string(entry.Text)),
						},
					}, nil
				case "markdown":
					return &protocol.ToolCallResultV1{
						Content: []protocol.ContentBlockV1{
							protocol.TextContentV1(string(entry.MarkdownReader)),
						},
					}, nil
				case "html":
					return &protocol.ToolCallResultV1{
						Content: []protocol.ContentBlockV1{
							protocol.TextContentV1(string(entry.HTML)),
						},
					}, nil
				case "toc":
					return &protocol.ToolCallResultV1{
						Content: []protocol.ContentBlockV1{
							protocol.TextContentV1(markdown.FormatTOC(entry.TOC, url, "")),
						},
					}, nil
				case "markdown-selector":
					return protocol.ErrorResultV1("selector-derived resources are not re-readable; call web-fetch with the selector arg to regenerate"), nil
				default:
					return protocol.ErrorResultV1("unknown web-fetch fragment: " + fragment + " (expected text, markdown, html, or toc)"), nil
				}
			}
			result, err := itemResources.ReadResource(ctx, p0.URI)
			if err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}
			if len(result.Contents) == 0 {
				return protocol.ErrorResultV1("no content"), nil
			}
			return &protocol.ToolCallResultV1{
				Content: []protocol.ContentBlockV1{
					protocol.TextContentV1(result.Contents[0].Text),
				},
			}, nil
		},
	)

	registry.Register(
		protocol.ToolV1{
			Name: "web-fetch",
			Description: "Fetch a web page via headless Firefox and return its content " +
				"as reader-mode Markdown (default). All three base formats (text, markdown, html) " +
				"are always rendered; non-selected formats are returned as resource_link URIs " +
				"that can be read via read-resource. A TOC of `#id` anchors is included as the " +
				"first content block when no selector is supplied (or when a selector misses) — " +
				"pass one of those ids as `selector` to trim the returned markdown to that " +
				"section only. When a selector matches, the TOC is returned as a resource_link " +
				"(`web-fetch://<url>#toc`) instead of inline so it doesn't dwarf the section. " +
				"Results are cached per URL for the lifetime of the MCP session; pass " +
				"`refresh: true` to force a re-fetch. Use instead of the built-in WebFetch when " +
				"you need complete, unsummarized content or when the page requires JavaScript " +
				"to render.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"URL to fetch"},"format":{"type":"string","description":"Format to return inline: 'markdown' (default), 'text', or 'html'. Other formats are returned as resource_link URIs.","enum":["markdown","text","html"]},"selector":{"type":"string","description":"Optional CSS selector to trim the returned markdown to a specific section (e.g. '#chap-stdenv', 'article#main'). Requires format=markdown. On a miss, the tool returns a diagnostic plus a resource_link to the full page - it never silently returns empty. The TOC of #id selectors is included inline when no selector is supplied or a selector misses; when a selector matches, the TOC is returned as a resource_link (web-fetch://<url>#toc) to avoid dwarfing the matched section."},"refresh":{"type":"boolean","description":"Force a re-fetch even if this URL was fetched earlier in the session. Default false: reuse cached HTML. Selector-only re-runs against the same URL never relaunch Firefox."}},"required":["url"]}`),
			Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		},
		func(ctx context.Context, args json.RawMessage) (*protocol.ToolCallResultV1, error) {
			var p0 struct {
				URL      string `json:"url"`
				Format   string `json:"format"`
				Selector string `json:"selector"`
				Refresh  bool   `json:"refresh"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}
			if p0.URL == "" {
				return protocol.ErrorResultV1("web-fetch: url is required"), nil
			}
			if p0.Format == "" {
				p0.Format = "markdown"
			}
			if p0.Selector != "" && p0.Format != "markdown" {
				return protocol.ErrorResultV1("selector is only supported with format=markdown; got format=" + p0.Format), nil
			}

			var entry *fetchCacheEntry
			if !p0.Refresh {
				if v, ok := fetchCache.Load(p0.URL); ok {
					entry = v.(*fetchCacheEntry)
				}
			}
			if entry == nil {
				dispatchMode := os.Getenv("CHREST_WEB_FETCH_DISPATCH")
				if dispatchMode == "" {
					dispatchMode = "bidi-intercept"
				}

				var err error
				switch dispatchMode {
				case "firefox-only":
					entry, err = fetchViaFirefox(ctx, p0.URL)
				case "bidi-intercept":
					entry, err = fetchViaDispatch(ctx, p0.URL)
				default:
					return protocol.ErrorResultV1(
						"unknown CHREST_WEB_FETCH_DISPATCH=" + dispatchMode +
							" (expected bidi-intercept or firefox-only)",
					), nil
				}

				if err != nil {
					return protocol.ErrorResultV1(err.Error()), nil
				}
				if entry == nil {
					return protocol.ErrorResultV1("web-fetch: empty result"), nil
				}
				fetchCache.Store(p0.URL, entry)
			}

			urlFragment := ""
			if parsed, parseErr := url.Parse(p0.URL); parseErr == nil {
				urlFragment = parsed.Fragment
			}
			tocBlock := protocol.TextContentV1(markdown.FormatTOC(entry.TOC, p0.URL, urlFragment))

			textURI := fmt.Sprintf("web-fetch://%s#text", p0.URL)
			markdownURI := fmt.Sprintf("web-fetch://%s#markdown", p0.URL)
			htmlURI := fmt.Sprintf("web-fetch://%s#html", p0.URL)

			if p0.Selector == "" {
				blocks := []protocol.ContentBlockV1{tocBlock}
				switch p0.Format {
				case "markdown":
					if len(entry.MarkdownReader) == 0 {
						blocks = append(
							blocks,
							protocol.TextContentV1(emptyExtractionDiagnostic(p0.URL, "markdown")),
							protocol.ResourceLinkContent(htmlURI, "Raw HTML", "", mimeHTML),
							protocol.ResourceLinkContent(textURI, "Plain text", "", mimeText),
						)
					} else {
						blocks = append(
							blocks,
							protocol.EmbeddedTextResourceContent(markdownURI, string(entry.MarkdownReader), mimeMarkdown),
							protocol.ResourceLinkContent(textURI, "Plain text", "", mimeText),
							protocol.ResourceLinkContent(htmlURI, "Raw HTML", "", mimeHTML),
						)
					}
				case "text":
					if len(entry.Text) == 0 {
						blocks = append(
							blocks,
							protocol.TextContentV1(emptyExtractionDiagnostic(p0.URL, "text")),
							protocol.ResourceLinkContent(htmlURI, "Raw HTML", "", mimeHTML),
							protocol.ResourceLinkContent(markdownURI, "Reader-mode Markdown", "", mimeMarkdown),
						)
					} else {
						blocks = append(
							blocks,
							protocol.EmbeddedTextResourceContent(textURI, string(entry.Text), mimeText),
							protocol.ResourceLinkContent(markdownURI, "Reader-mode Markdown", "", mimeMarkdown),
							protocol.ResourceLinkContent(htmlURI, "Raw HTML", "", mimeHTML),
						)
					}
				case "html":
					blocks = append(
						blocks,
						protocol.EmbeddedTextResourceContent(htmlURI, string(entry.HTML), mimeHTML),
						protocol.ResourceLinkContent(markdownURI, "Reader-mode Markdown", "", mimeMarkdown),
						protocol.ResourceLinkContent(textURI, "Plain text", "", mimeText),
					)
				default:
					return protocol.ErrorResultV1("unknown format: " + p0.Format), nil
				}
				return &protocol.ToolCallResultV1{Content: blocks}, nil
			}

			// Selector path. Use the section-expanding variant so an
			// `#id` selector that matches a heading returns the whole
			// section (heading + following siblings up to the next
			// heading of equal-or-higher level) rather than just the
			// heading element.
			rc, err := markdown.ConvertSelectorSection(bytes.NewReader(entry.HTML), p0.Selector)
			if err != nil {
				if errors.Is(err, markdown.ErrSelectorNoMatch) {
					diag := fmt.Sprintf(
						"selector %q matched no element on %s.\nThe TOC above lists the anchors that are present on this page.\nFull markdown is available via the resource_link below; call read-resource on web-fetch://%s#markdown to fetch it.",
						p0.Selector, p0.URL, p0.URL,
					)
					return &protocol.ToolCallResultV1{
						Content: []protocol.ContentBlockV1{
							tocBlock,
							protocol.TextContentV1(diag),
							protocol.ResourceLinkContent(markdownURI, "Reader-mode Markdown", "", mimeMarkdown),
							protocol.ResourceLinkContent(textURI, "Plain text", "", mimeText),
							protocol.ResourceLinkContent(htmlURI, "Raw HTML", "", mimeHTML),
						},
					}, nil
				}
				return protocol.ErrorResultV1(err.Error()), nil
			}
			trimmed, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}

			// Selector matched and we have the section the caller asked
			// for. The full TOC (potentially huge — the nixpkgs manual
			// renders to ~150 KB) would dwarf the section payload, so
			// link to it instead of inlining. Callers can read-resource
			// the toc URI if they need to pick a different section.
			selectorURI := fmt.Sprintf("web-fetch://%s#markdown-selector", p0.URL)
			tocURI := fmt.Sprintf("web-fetch://%s#toc", p0.URL)
			return &protocol.ToolCallResultV1{
				Content: []protocol.ContentBlockV1{
					protocol.EmbeddedTextResourceContent(selectorURI, string(trimmed), mimeMarkdown),
					protocol.ResourceLinkContent(tocURI, "Table of Contents", "", mimeMarkdown),
					protocol.ResourceLinkContent(markdownURI, "Reader-mode Markdown", "", mimeMarkdown),
					protocol.ResourceLinkContent(textURI, "Plain text", "", mimeText),
					protocol.ResourceLinkContent(htmlURI, "Raw HTML", "", mimeHTML),
				},
			}, nil
		},
	)

	srv, err := server.New(t, server.Options{
		ServerName:    app.Name,
		ServerVersion: app.Version,
		Tools:         registry,
		Resources:     itemResources,
		// V0-protocol clients still get V1-shape responses (resource_link,
		// embedded resource, tool annotations). Without this the V0 handler
		// downgrades V1 ContentBlocks and drops Annotations, breaking the
		// web_fetch_* bats suite. See pkgs/server Options doc.
		PreferV1Providers: true,
	})
	if err != nil {
		return errors.Wrap(err)
	}

	return srv.Run(ctx)
}

// emptyExtractionDiagnostic builds the user-facing message we surface
// when the named format extracted no content from the fetched page.
// Common cause is a meta-refresh redirect or a page whose body is
// rendered entirely by JavaScript (SPA shells). The caller still has
// the raw HTML available via a resource_link in the same response —
// we promote it to position content[2] so the diagnostic and the
// fallback are adjacent.
func emptyExtractionDiagnostic(url, format string) string {
	return fmt.Sprintf(
		"No %s content extracted from %s. The page may be a redirect, "+
			"a single-page app shell, or have no readable body. "+
			"The raw HTML is available via the resource_link below; "+
			"call read-resource on web-fetch://%s#html to fetch it.",
		format, url, url,
	)
}

// splitWebFetchURI parses "web-fetch://<url>#<fragment>" into its url and
// fragment halves. The URL itself may contain '#' characters if the caller
// passed a fragmented URL, so the last '#' is treated as the delimiter
// (matching the format emitted by the web-fetch handler). Returns empty
// strings if the URI doesn't carry the web-fetch:// prefix or lacks a '#'.
func splitWebFetchURI(uri string) (url, fragment string) {
	const prefix = "web-fetch://"
	if !strings.HasPrefix(uri, prefix) {
		return "", ""
	}
	rest := uri[len(prefix):]
	idx := strings.LastIndex(rest, "#")
	if idx < 0 {
		return "", ""
	}
	return rest[:idx], rest[idx+1:]
}

// fetchCacheEntry is the cached payload for a single web-fetch URL.
// Path identifies which dispatch branch produced the entry
// ("firefox-only", "html", "text", or "unknown") and is set for
// observability when investigating slot population issues.
type fetchCacheEntry struct {
	HTML           []byte
	Text           []byte
	MarkdownReader []byte
	TOC            []markdown.Heading
	FetchedAt      time.Time
	Path           string
}

// fetchViaFirefox preserves the legacy all-Firefox path. Used when
// CHREST_WEB_FETCH_DISPATCH=firefox-only.
func fetchViaFirefox(ctx context.Context, url string) (*fetchCacheEntry, error) {
	results, err := tools.MultiExtract(ctx, tools.MultiExtractParams{
		URL:     url,
		Formats: []string{"text", "markdown-reader", "html-outer"},
	})
	if err != nil {
		return nil, err
	}
	entry := &fetchCacheEntry{FetchedAt: time.Now(), Path: "firefox-only"}
	var errs []string
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", r.Format, r.Err))
			continue
		}
		switch r.Format {
		case "text":
			entry.Text = r.Data
		case "markdown-reader":
			entry.MarkdownReader = r.Data
		case "html-outer":
			entry.HTML = r.Data
		}
	}
	if entry.Text == nil && entry.MarkdownReader == nil && entry.HTML == nil {
		return nil, fmt.Errorf("all formats failed: %s", strings.Join(errs, "; "))
	}
	if entry.HTML != nil {
		toc, tocErr := markdown.ExtractTOC(bytes.NewReader(entry.HTML))
		if tocErr != nil {
			log.Printf("web-fetch: ExtractTOC failed for %s: %v", url, tocErr)
		} else {
			entry.TOC = toc
		}
	}
	return entry, nil
}

// fetchViaDispatch implements the content-type-aware path: register
// a BiDi response intercept, navigate, classify, and either continue
// (for HTML/Text) or fail (for Binary/HTTPError) the request.
func fetchViaDispatch(ctx context.Context, urlStr string) (*fetchCacheEntry, error) {
	// Bound the entire dispatch — including the file://, data: short-circuit
	// — so a Navigate that errors before any intercept event fires (e.g. DNS
	// failure) cannot deadlock the goroutine + main goroutine waiting on each
	// other. 60s matches the `chrest capture --timeout` default.
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	// file:// and data: URLs have no hostname, so the BiDi urlPattern
	// hostname field is meaningless. These are local content (no binary
	// surprises) so route them through the legacy path.
	if parsed.Scheme == "file" || parsed.Scheme == "data" {
		return fetchViaFirefox(ctx, urlStr)
	}

	session, err := firefox.NewSession(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	// Pass empty (protocol, hostname) so the intercept is scoped only
	// to the (fresh, per-call) browsing context — this catches
	// cross-host redirects (e.g. github.com → codeload.github.com on
	// release tarball downloads). Browser-context scoping is enough
	// because the session navigates exactly one URL chain per call.
	interceptID, events, err := session.AddResponseIntercept(ctx, "", "")
	if err != nil {
		return nil, err
	}
	defer session.RemoveIntercept(ctx, interceptID)

	type dispatchOutcome struct {
		entry              *fetchCacheEntry
		err                error
		failedDeliberately bool // true if we explicitly failRequest'd
	}
	outcome := make(chan dispatchOutcome, 1)

	go func() {
		// Once we've classified the top-level navigation we keep
		// looping to auto-continue subresources — they arrive on the
		// same `events` channel after the nav event, and if we don't
		// drain them the BiDi server keeps them paused, the page
		// never fires `load`, and Navigate deadlocks on its
		// wait:complete promise.
		var navHandled bool
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					// Intercept producer closed the channel — happens
					// when sub.Events closes, i.e. the BiDi connection
					// died (browser crashed or was killed) or the
					// session was torn down. If nav was already
					// classified, the main goroutine has already
					// received its outcome and is waiting on
					// Navigate; nothing to do here. If nav was NOT
					// classified, the main goroutine will block
					// forever on <-outcome unless we write something.
					if !navHandled {
						outcome <- dispatchOutcome{
							err: fmt.Errorf("web-fetch: intercept channel closed before navigation event for %s", scrubURL(urlStr)),
						}
					}
					return
				}

				// Subresource (CSS / JS / image / XHR) — not part of
				// the top-level navigation chain. The intercept fires
				// for every response in this browsing context (the
				// urlPatterns scoping was dropped to follow cross-host
				// redirects), so we must explicitly continue every
				// non-navigation response. We swallow continue errors
				// here: a subresource we couldn't release is the
				// browser's problem, not the dispatch's, and the page
				// will still reach `load` for everything else.
				if ev.Navigation == "" {
					_ = session.ContinueResponse(ctx, ev.RequestID)
					continue
				}

				// Defensive: if a second top-level navigation event
				// shows up after we've already classified, just let
				// it through. Should not happen in practice.
				if navHandled {
					_ = session.ContinueResponse(ctx, ev.RequestID)
					continue
				}

				// 3xx redirect: let it follow and wait for the next event.
				// Without this, the dispatcher would misclassify the redirect
				// as an HTTPError and surface "HTTP 301" to the user even
				// though following the redirect would have succeeded.
				if ev.Status >= 300 && ev.Status < 400 {
					if err := session.ContinueResponse(ctx, ev.RequestID); err != nil {
						outcome <- dispatchOutcome{err: err}
						return
					}
					continue
				}

				ct := headerValue(ev.Headers, "Content-Type")
				class := rawfetch.Classify(httpHeaderFrom(ev.Headers), urlStr, ev.Status)

				log.Printf("web-fetch: dispatch=bidi-intercept class=%v ct=%s status=%d url=%s",
					class, ct, ev.Status, scrubURL(urlStr))

				switch class {
				case rawfetch.ClassHTML, rawfetch.ClassText:
					// Mark nav handled up front so the rest of the
					// case reads in execution order: classify → release
					// → notify main → loop on for subresources. We
					// don't return from this branch — we stay in the
					// outer for-loop to drain subresources while the
					// main goroutine waits on Navigate.
					navHandled = true
					if err := session.ContinueResponse(ctx, ev.RequestID); err != nil {
						outcome <- dispatchOutcome{err: err}
						return
					}
					outcome <- dispatchOutcome{
						entry: &fetchCacheEntry{
							FetchedAt: time.Now(),
							Path:      classPathLabel(class),
						},
					}
				case rawfetch.ClassBinary:
					_ = session.FailRequest(ctx, ev.RequestID)
					outcome <- dispatchOutcome{
						err: fmt.Errorf("web-fetch refused binary content-type %q from %s; use `chrest capture` to save binary downloads",
							ct, ev.URL),
						failedDeliberately: true,
					}
					return
				case rawfetch.ClassHTTPError:
					_ = session.FailRequest(ctx, ev.RequestID)
					outcome <- dispatchOutcome{
						err:                fmt.Errorf("web-fetch: HTTP %d from %s", ev.Status, ev.URL),
						failedDeliberately: true,
					}
					return
				default:
					// rawfetch.Classify currently never returns ClassUnknown,
					// but guard against future drift so a zero-value Class
					// can't silently produce an empty result.
					_ = session.FailRequest(ctx, ev.RequestID)
					outcome <- dispatchOutcome{
						err:                fmt.Errorf("web-fetch: unhandled class %v for %s", class, ev.URL),
						failedDeliberately: true,
					}
					return
				}

			case <-ctx.Done():
				if !navHandled {
					outcome <- dispatchOutcome{err: ctx.Err()}
					return
				}
				// Nav was already classified, but the dispatch ctx
				// expired before extraction finished. Drain any
				// subresource events still buffered in `events` and
				// release their paused requests so they don't outlive
				// session.Close. We use a fresh background ctx because
				// the dispatch ctx is dead; bound it tightly so we
				// don't extend the fetch's effective deadline by much.
				drainCtx, drainCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer drainCancel()
				for {
					select {
					case ev, ok := <-events:
						if !ok {
							return
						}
						_ = session.ContinueResponse(drainCtx, ev.RequestID)
					case <-drainCtx.Done():
						return
					default:
						return
					}
				}
			}
		}
	}()

	navErr := session.Navigate(ctx, urlStr)
	out := <-outcome

	if out.failedDeliberately {
		// Navigate's NS_ERROR_ABORT is expected.
		if navErr != nil && !firefox.IsAbortedNavigation(navErr) {
			return nil, navErr
		}
		return nil, out.err
	}
	if navErr != nil {
		return nil, navErr
	}
	if out.err != nil {
		return nil, out.err
	}
	if out.entry == nil {
		return nil, fmt.Errorf("web-fetch: dispatcher produced no entry")
	}

	// Now actually fill the entry. For HTML, extract from the existing
	// intercept session — it's already navigated, so calling MultiExtract
	// (which opens a fresh session and navigates again) would double-
	// fetch the page (chrest#64). For Text, read the body and use
	// rawfetch.BuildFromText.
	switch out.entry.Path {
	case "html":
		results, err := tools.MultiExtractFromSession(ctx, session, tools.MultiExtractParams{
			URL:     urlStr,
			Formats: []string{"text", "markdown-reader", "html-outer"},
		})
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			switch r.Format {
			case "text":
				out.entry.Text = r.Data
			case "markdown-reader":
				out.entry.MarkdownReader = r.Data
			case "html-outer":
				out.entry.HTML = r.Data
			}
		}
		if out.entry.HTML != nil {
			if toc, err := markdown.ExtractTOC(bytes.NewReader(out.entry.HTML)); err == nil {
				out.entry.TOC = toc
			}
		}

	case "text":
		// Pull the text body via Firefox; it's the easiest way to get
		// what the user sees regardless of charset handling.
		rc, err := session.GetDocumentHTML(ctx)
		if err != nil {
			return nil, err
		}
		domBytes, _ := io.ReadAll(rc)
		rc.Close()
		body := stripPreWrapper(domBytes)
		ct := guessContentTypeFromURL(urlStr)
		r := rawfetch.BuildFromText(body, ct, urlStr)
		out.entry.Text = r.Text
		out.entry.MarkdownReader = r.Markdown
		out.entry.HTML = r.HTML
		out.entry.TOC = r.TOC
	}

	return out.entry, nil
}

func httpHeaderFrom(headers []firefox.HTTPHeader) http.Header {
	h := http.Header{}
	for _, hh := range headers {
		h.Set(hh.Name, hh.Value)
	}
	return h
}

func headerValue(headers []firefox.HTTPHeader, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func classPathLabel(c rawfetch.Class) string {
	switch c {
	case rawfetch.ClassHTML:
		return "html"
	case rawfetch.ClassText:
		return "text"
	default:
		return "unknown"
	}
}

// stripPreWrapper removes Firefox's auto-rendering of text/plain in a
// <pre> wrapper, returning the raw body. If the input doesn't have a
// <pre> wrapper, returns it unchanged.
func stripPreWrapper(domBytes []byte) []byte {
	s := string(domBytes)
	// Look for <pre>...</pre> tag, possibly with attributes.
	open := strings.Index(s, "<pre")
	if open < 0 {
		return domBytes
	}
	openEnd := strings.Index(s[open:], ">")
	if openEnd < 0 {
		return domBytes
	}
	bodyStart := open + openEnd + 1
	closeIdx := strings.LastIndex(s, "</pre>")
	if closeIdx < bodyStart {
		return domBytes
	}
	return []byte(s[bodyStart:closeIdx])
}

// scrubURL returns the URL with the query string and fragment redacted
// for logging. The path is preserved (it's part of the route and
// usually not sensitive).
func scrubURL(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "<unparseable>"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

// guessContentTypeFromURL infers a content-type from a URL extension
// for use with rawfetch.BuildFromText when the response Content-Type
// isn't directly threaded through to this point. (BuildFromText also
// considers URL ext for the markdown branch, so this is mostly a
// best-effort label.)
func guessContentTypeFromURL(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	ext := strings.ToLower(path.Ext(parsed.Path))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".yaml", ".yml":
		return "application/yaml"
	}
	return "text/plain"
}

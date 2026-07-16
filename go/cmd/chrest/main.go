package main

import (
	"bytes"
	"context"
	"encoding/base64"
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
	registerCaptureServeCommand(app)
	registerVersionCommand(app)

	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err = runMCP(ctx, app, p); err != nil {
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

	// Bypass dewey for capture-serve: RFC 0008's contract is a persistent
	// JSON-RPC session over a self-created rendezvous socket, announced on
	// stdout as a single protocol-only line — none of which fits the
	// Result path.
	if len(os.Args) > 1 && os.Args[1] == "capture-serve" {
		if err = cmdCaptureServe(ctx, app.Version, os.Args[2:]); err != nil {
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
			if strings.HasPrefix(p0.URI, "capture://") {
				url, fragment := splitCaptureURI(p0.URI)
				if url == "" {
					return protocol.ErrorResultV1("invalid capture URI: " + p0.URI), nil
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
					return protocol.ErrorResultV1("selector-derived resources are not re-readable; call capture with the selector arg to regenerate"), nil
				default:
					return protocol.ErrorResultV1("unknown capture fragment: " + fragment + " (expected text, markdown, html, or toc)"), nil
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
			Name: "capture",
			Description: "Fetch or capture a web page via headless Firefox. Five formats: " +
				"'markdown' (default, reader-mode extraction), 'text', 'html', 'pdf', and " +
				"'screenshot-png'.\n\n" +
				"For markdown/text/html: all three base formats are always rendered; " +
				"non-selected formats are returned as resource_link URIs that can be read via " +
				"read-resource. A TOC of `#id` anchors is included as the first content block " +
				"when no selector is supplied (or when a selector misses) — pass one of those " +
				"ids as `selector` to trim the returned markdown to that section only. When a " +
				"selector matches, the TOC is returned as a resource_link (`capture://<url>#toc`) " +
				"instead of inline so it doesn't dwarf the section. Results are cached per URL " +
				"for the lifetime of the MCP session; pass `refresh: true` to force a re-fetch. " +
				"Use instead of the built-in WebFetch when you need complete, unsummarized " +
				"content or when the page requires JavaScript to render.\n\n" +
				"For pdf/screenshot-png: renders the page and returns the bytes inline as " +
				"base64 (an image content block for screenshot-png, a base64 blob resource for " +
				"pdf) — not cached, a fresh capture every call. `full-page` (screenshot-png only) " +
				"captures the full scrollable page instead of just the viewport. `landscape`, " +
				"`no-headers`, `background`, `paper-width`, `paper-height`, and the four " +
				"`margin-*` flags (inches) are pdf-only page-setup options; unset values use the " +
				"browser default (typically US Letter, ~0.4\" margins, headers on, no background).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"URL to fetch or capture"},"format":{"type":"string","description":"'markdown' (default) / 'text' / 'html' return inline; other formats are resource_link URIs. 'pdf' / 'screenshot-png' render and return bytes inline as base64.","enum":["markdown","text","html","pdf","screenshot-png"]},"selector":{"type":"string","description":"Optional CSS selector to trim the returned markdown to a specific section (e.g. '#chap-stdenv', 'article#main'). Requires format=markdown. On a miss, the tool returns a diagnostic plus a resource_link to the full page - it never silently returns empty. The TOC of #id selectors is included inline when no selector is supplied or a selector misses; when a selector matches, the TOC is returned as a resource_link (capture://<url>#toc) to avoid dwarfing the matched section."},"refresh":{"type":"boolean","description":"markdown/text/html only: force a re-fetch even if this URL was fetched earlier in the session. Default false: reuse cached HTML. Selector-only re-runs against the same URL never relaunch Firefox."},"landscape":{"type":"boolean","description":"pdf only: use landscape orientation"},"no-headers":{"type":"boolean","description":"pdf only: disable header and footer"},"background":{"type":"boolean","description":"pdf only: print background graphics"},"paper-width":{"type":"number","description":"pdf only: page width in inches (default: browser default, typically 8.5)"},"paper-height":{"type":"number","description":"pdf only: page height in inches (default: browser default, typically 11)"},"margin-top":{"type":"number","description":"pdf only: top margin in inches (0 for borderless)"},"margin-bottom":{"type":"number","description":"pdf only: bottom margin in inches (0 for borderless)"},"margin-left":{"type":"number","description":"pdf only: left margin in inches (0 for borderless)"},"margin-right":{"type":"number","description":"pdf only: right margin in inches (0 for borderless)"},"full-page":{"type":"boolean","description":"screenshot-png only: capture the full scrollable page instead of just the viewport"}},"required":["url"]}`),
			Annotations: &protocol.ToolAnnotations{ReadOnlyHint: protocol.BoolPtr(true)},
		},
		func(ctx context.Context, args json.RawMessage) (*protocol.ToolCallResultV1, error) {
			var p0 struct {
				tools.CaptureParams
				Refresh bool `json:"refresh"`
			}
			if err := json.Unmarshal(args, &p0); err != nil {
				return protocol.ErrorResultV1(err.Error()), nil
			}
			if p0.URL == "" {
				return protocol.ErrorResultV1("capture: url is required"), nil
			}
			if p0.Format == "" {
				p0.Format = "markdown"
			}
			if p0.Selector != "" && p0.Format != "markdown" {
				return protocol.ErrorResultV1("selector is only supported with format=markdown; got format=" + p0.Format), nil
			}

			if p0.Format == tools.FormatPDF || p0.Format == tools.FormatScreenshotPNG {
				return captureBinaryFormat(ctx, p0.CaptureParams)
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
					return protocol.ErrorResultV1("capture: empty result"), nil
				}
				fetchCache.Store(p0.URL, entry)
			}

			urlFragment := ""
			if parsed, parseErr := url.Parse(p0.URL); parseErr == nil {
				urlFragment = parsed.Fragment
			}
			tocBlock := protocol.TextContentV1(markdown.FormatTOC(entry.TOC, p0.URL, urlFragment))

			textURI := fmt.Sprintf("capture://%s#text", p0.URL)
			markdownURI := fmt.Sprintf("capture://%s#markdown", p0.URL)
			htmlURI := fmt.Sprintf("capture://%s#html", p0.URL)

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
						"selector %q matched no element on %s.\nThe TOC above lists the anchors that are present on this page.\nFull markdown is available via the resource_link below; call read-resource on capture://%s#markdown to fetch it.",
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

			// Link the TOC instead of inlining it: for large manuals
			// (nixpkgs renders to ~150 KB) the inline TOC dwarfs the
			// matched section. Callers can read-resource the toc URI
			// if they need to pick a different section.
			selectorURI := fmt.Sprintf("capture://%s#markdown-selector", p0.URL)
			tocURI := fmt.Sprintf("capture://%s#toc", p0.URL)
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
		// capture_mcp bats suite. See pkgs/server Options doc.
		PreferV1Providers: true,
	})
	if err != nil {
		return errors.Wrap(err)
	}

	return srv.Run(ctx)
}

// captureBinaryFormat handles the pdf and screenshot-png branches of the
// capture tool: render a fresh capture (no caching — every call re-renders)
// and return the bytes inline as base64. screenshot-png comes back as an
// image content block; pdf comes back as an embedded blob resource (MCP has
// no "type": "pdf" content block, so resource+blob is the closest fit).
func captureBinaryFormat(ctx context.Context, p tools.CaptureParams) (*protocol.ToolCallResultV1, error) {
	if err := p.Validate(); err != nil {
		return protocol.ErrorResultV1(err.Error()), nil
	}

	results, err := tools.MultiExtract(ctx, p.ToMultiExtractParams())
	if err != nil {
		return protocol.ErrorResultV1(err.Error()), nil
	}
	if len(results) != 1 {
		return protocol.ErrorResultV1(fmt.Sprintf("capture: expected 1 result, got %d", len(results))), nil
	}
	r := results[0]
	if r.Err != nil {
		return protocol.ErrorResultV1(r.Err.Error()), nil
	}
	if len(r.Data) == 0 {
		return protocol.ErrorResultV1("capture: empty result"), nil
	}

	b64 := base64.StdEncoding.EncodeToString(r.Data)

	if p.Format == tools.FormatScreenshotPNG {
		return &protocol.ToolCallResultV1{
			Content: []protocol.ContentBlockV1{protocol.ImageContentV1(b64, "image/png")},
		}, nil
	}
	return &protocol.ToolCallResultV1{
		Content: []protocol.ContentBlockV1{
			protocol.EmbeddedBlobResourceContent(fmt.Sprintf("capture://%s#pdf", p.URL), b64, "application/pdf"),
		},
	}, nil
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
			"call read-resource on capture://%s#html to fetch it.",
		format, url, url,
	)
}

// splitCaptureURI parses "capture://<url>#<fragment>" into its url and
// fragment halves. The URL itself may contain '#' characters if the caller
// passed a fragmented URL, so the last '#' is treated as the delimiter
// (matching the format emitted by the capture handler). Returns empty
// strings if the URI doesn't carry the capture:// prefix or lacks a '#'.
func splitCaptureURI(uri string) (url, fragment string) {
	const prefix = "capture://"
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

// fetchCacheEntry is the cached payload for a single capture URL.
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
		Formats: []string{tools.FormatText, tools.FormatMarkdownReader, tools.FormatHTMLOuter},
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
		case tools.FormatText:
			entry.Text = r.Data
		case tools.FormatMarkdownReader:
			entry.MarkdownReader = r.Data
		case tools.FormatHTMLOuter:
			entry.HTML = r.Data
		}
	}
	if entry.Text == nil && entry.MarkdownReader == nil && entry.HTML == nil {
		return nil, fmt.Errorf("all formats failed: %s", strings.Join(errs, "; "))
	}
	if entry.HTML != nil {
		toc, tocErr := markdown.ExtractTOC(bytes.NewReader(entry.HTML))
		if tocErr != nil {
			log.Printf("capture: ExtractTOC failed for %s: %v", url, tocErr)
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
					if firefox.BiDiDebug() {
						log.Printf("bidi-debug: intercept channel closed navHandled=%v url=%s", navHandled, scrubURL(urlStr))
					}
					if !navHandled {
						outcome <- dispatchOutcome{
							err: fmt.Errorf("capture: intercept channel closed before navigation event for %s", scrubURL(urlStr)),
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
					if firefox.BiDiDebug() {
						log.Printf("bidi-debug: continuing subresource status=%d url=%s", ev.Status, ev.URL)
					}
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

				log.Printf("capture: dispatch=bidi-intercept class=%v ct=%s status=%d url=%s",
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
						err: fmt.Errorf("capture refused binary content-type %q from %s; use `chrest capture` to save binary downloads",
							ct, ev.URL),
						failedDeliberately: true,
					}
					return
				case rawfetch.ClassHTTPError:
					_ = session.FailRequest(ctx, ev.RequestID)
					outcome <- dispatchOutcome{
						err:                fmt.Errorf("capture: HTTP %d from %s", ev.Status, ev.URL),
						failedDeliberately: true,
					}
					return
				default:
					// rawfetch.Classify currently never returns ClassUnknown,
					// but guard against future drift so a zero-value Class
					// can't silently produce an empty result.
					_ = session.FailRequest(ctx, ev.RequestID)
					outcome <- dispatchOutcome{
						err:                fmt.Errorf("capture: unhandled class %v for %s", class, ev.URL),
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
		return nil, fmt.Errorf("capture: dispatcher produced no entry")
	}

	// Now actually fill the entry. For HTML, extract from the existing
	// intercept session — it's already navigated, so calling MultiExtract
	// (which opens a fresh session and navigates again) would double-
	// fetch the page. For Text, read the body and use
	// rawfetch.BuildFromText.
	switch out.entry.Path {
	case "html":
		results, err := tools.MultiExtractFromSession(ctx, session, tools.MultiExtractParams{
			URL:     urlStr,
			Formats: []string{tools.FormatText, tools.FormatMarkdownReader, tools.FormatHTMLOuter},
		})
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			if r.Err != nil {
				continue
			}
			switch r.Format {
			case tools.FormatText:
				out.entry.Text = r.Data
			case tools.FormatMarkdownReader:
				out.entry.MarkdownReader = r.Data
			case tools.FormatHTMLOuter:
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

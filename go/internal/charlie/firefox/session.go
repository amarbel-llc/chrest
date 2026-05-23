package firefox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"strings"
	"sync"

	"github.com/amarbel-llc/purse-first/libs/dewey/pkgs/errors"
	"code.linenisgreat.com/chrest/go/internal/bravo/bidi"
	"code.linenisgreat.com/chrest/go/internal/charlie/launcher"
)

// Session drives a headless Firefox process via WebDriver BiDi.
type Session struct {
	firefox      *launcher.Process
	conn         *bidi.Conn
	contextID    string
	capabilities bidiCapabilities

	// networkSub delivers network.responseCompleted events during
	// navigations. Registered once in initSession and drained by each
	// Navigate call to capture top-level document HTTP metadata for
	// the envelope artifact (RFC 0001). Closed in Close().
	networkSub *bidi.Subscription
	lastHTTP   *HTTPResponse

	intercepts sync.Map // intercept-id → *bidi.Subscription
}

// responseCompletedEvent is the subset of the WebDriver BiDi
// network.responseCompleted event params we need for the envelope.
type responseCompletedEvent struct {
	Context    string `json:"context"`
	Navigation string `json:"navigation"`
	Request    struct {
		Timings struct {
			RequestTime float64 `json:"requestTime"`
			FetchStart  float64 `json:"fetchStart"`
			ResponseEnd float64 `json:"responseEnd"`
		} `json:"timings"`
	} `json:"request"`
	Response struct {
		URL     string `json:"url"`
		Status  int    `json:"status"`
		Headers []struct {
			Name  string `json:"name"`
			Value struct {
				Value string `json:"value"`
			} `json:"value"`
		} `json:"headers"`
	} `json:"response"`
}

// bidiCapabilities captures the subset of the session.new response we
// carry forward into BrowserInfo. See WebDriver BiDi §6.4.1 for the
// full shape.
type bidiCapabilities struct {
	BrowserName    string `json:"browserName"`
	BrowserVersion string `json:"browserVersion"`
	PlatformName   string `json:"platformName"`
	UserAgent      string `json:"userAgent"`
}

// NewSession launches headless Firefox and connects via BiDi.
func NewSession(ctx context.Context) (*Session, error) {
	return newSessionFromLaunch(ctx, Launch)
}

// NewSessionWithProfile launches headless Firefox against an explicit
// profile directory and connects via BiDi.
func NewSessionWithProfile(ctx context.Context, profilePath string) (*Session, error) {
	return newSessionFromLaunch(ctx, func(ctx context.Context) (*launcher.Process, error) {
		return LaunchWithProfile(ctx, profilePath)
	})
}

func newSessionFromLaunch(ctx context.Context, launchFn func(context.Context) (*launcher.Process, error)) (*Session, error) {
	ff, err := launchFn(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := bidi.Dial(ff.WSURL())
	if err != nil {
		ff.Close()
		return nil, err
	}

	s := &Session{firefox: ff, conn: conn}

	if err := s.initSession(); err != nil {
		s.Close()
		return nil, err
	}

	return s, nil
}

// initSession creates a BiDi session and discovers the default browsing context.
func (s *Session) initSession() error {
	// Create session.
	result, err := s.conn.Send("session.new", map[string]any{
		"capabilities": map[string]any{},
	})
	if err != nil {
		return errors.Wrap(err)
	}
	// Preserve capabilities for BrowserInfo. Non-fatal if shape surprises us.
	var sessResp struct {
		Capabilities bidiCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(result, &sessResp); err == nil {
		s.capabilities = sessResp.Capabilities
	}

	// Get the default browsing context.
	result, err = s.conn.Send("browsingContext.getTree", map[string]any{})
	if err != nil {
		return errors.Wrap(err)
	}

	var tree struct {
		Contexts []struct {
			Context string `json:"context"`
		} `json:"contexts"`
	}
	if err := json.Unmarshal(result, &tree); err != nil {
		return errors.Wrap(err)
	}

	if len(tree.Contexts) == 0 {
		return errors.Errorf("no browsing contexts found")
	}

	s.contextID = tree.Contexts[0].Context

	// Register a local subscription first, then tell BiDi to start
	// emitting the event — this order prevents losing early events on
	// fast navigations. session.subscribe failures are non-fatal:
	// captures still work, we just won't populate envelope.http.*.
	//
	// The filter drops sub-resource events (those without a navigation
	// field) before they reach the channel. Heavy pages emit hundreds
	// of network.responseCompleted events for sub-resources; without
	// the filter the 64-slot buffer overflows on complex pages (#33).
	s.networkSub = s.conn.SubscribeWithFilter(
		[]string{"network.responseCompleted"},
		func(ev bidi.EventFrame) bool {
			var peek struct {
				Navigation string `json:"navigation"`
			}
			if err := json.Unmarshal(ev.Params, &peek); err != nil {
				return false
			}
			return peek.Navigation != ""
		},
	)
	if _, err := s.conn.Send("session.subscribe", map[string]any{
		"events":   []string{"network.responseCompleted"},
		"contexts": []string{s.contextID},
	}); err != nil {
		log.Printf("bidi: session.subscribe failed, envelope http.* will be absent: %v", err)
		s.networkSub.Close()
		s.networkSub = nil
	}
	return nil
}

func (s *Session) SetViewport(ctx context.Context, width, height int) error {
	_, err := s.conn.Send("browsingContext.setViewport", map[string]any{
		"context": s.contextID,
		"viewport": map[string]any{
			"width":  width,
			"height": height,
		},
	})
	return errors.Wrap(err)
}

// SetCookie writes a cookie into Firefox via BiDi storage.setCookie.
// The cookie lives in the in-memory cookie jar only; Firefox does not
// flush BiDi-set cookies to cookies.sqlite, even on graceful shutdown.
func (s *Session) SetCookie(ctx context.Context, name, value, domain, path string) error {
	_, err := s.conn.Send("storage.setCookie", map[string]any{
		"cookie": map[string]any{
			"name":   name,
			"value":  map[string]any{"type": "string", "value": value},
			"domain": domain,
			"path":   path,
		},
	})
	return errors.Wrap(err)
}

func (s *Session) Navigate(ctx context.Context, url string) error {
	// Drain any stale events buffered from a prior navigation before
	// issuing this one, so only the current navigation's events inform
	// lastHTTP.
	s.drainNetworkEvents()
	s.lastHTTP = nil

	_, err := s.conn.Send("browsingContext.navigate", map[string]any{
		"context": s.contextID,
		"url":     url,
		"wait":    "complete",
	})
	if err != nil {
		return errors.Wrap(err)
	}

	// `wait: "complete"` returns only after the load event fires, so
	// all navigation-tagged response events have already been queued
	// onto the subscription channel. Drain synchronously and keep the
	// last event for this context with a navigation id — that's the
	// final hop if the response chain had redirects.
	if s.networkSub != nil {
		s.lastHTTP = s.pickLastNavigationHTTP()
	}
	return nil
}

// drainNetworkEvents removes any events buffered on the subscription
// without blocking.
func (s *Session) drainNetworkEvents() {
	if s.networkSub == nil {
		return
	}
	for {
		select {
		case <-s.networkSub.Events:
		default:
			return
		}
	}
}

// pickLastNavigationHTTP drains the subscription non-blockingly and
// returns the HTTPResponse for the final navigation-tagged event
// belonging to this session's browsing context. Returns nil if no
// matching event was seen, including when the subscription channel
// closes (BiDi connection died) before any event arrived.
func (s *Session) pickLastNavigationHTTP() *HTTPResponse {
	var picked *responseCompletedEvent
	for {
		select {
		case ev, ok := <-s.networkSub.Events:
			if !ok {
				// Channel closed. A bare `break` would exit only the
				// select and the for-loop would re-select the same
				// closed-channel case forever (busy-loop, 100% CPU).
				return responseCompletedToHTTP(picked)
			}
			var decoded responseCompletedEvent
			if err := json.Unmarshal(ev.Params, &decoded); err != nil {
				log.Printf("bidi: unparsable network.responseCompleted: %v", err)
				continue
			}
			if decoded.Context != s.contextID || decoded.Navigation == "" {
				continue
			}
			picked = &decoded
		default:
			return responseCompletedToHTTP(picked)
		}
	}
}

// responseCompletedToHTTP converts a captured BiDi
// network.responseCompleted event into the typed HTTPResponse the
// caller cares about. Returns nil if no event was captured.
func responseCompletedToHTTP(picked *responseCompletedEvent) *HTTPResponse {
	if picked == nil {
		return nil
	}
	headers := make([]HTTPHeader, 0, len(picked.Response.Headers))
	for _, h := range picked.Response.Headers {
		headers = append(headers, HTTPHeader{Name: h.Name, Value: h.Value.Value})
	}
	var timing int64
	if picked.Request.Timings.ResponseEnd > 0 && picked.Request.Timings.FetchStart > 0 {
		timing = int64(picked.Request.Timings.ResponseEnd - picked.Request.Timings.FetchStart)
	}
	return &HTTPResponse{
		URL:      picked.Response.URL,
		Status:   picked.Response.Status,
		Headers:  headers,
		TimingMs: timing,
	}
}

// LastNavigationHTTP returns the response captured during the most
// recent Navigate, or (zero, false) if none was captured.
func (s *Session) LastNavigationHTTP() (HTTPResponse, bool) {
	if s.lastHTTP == nil {
		return HTTPResponse{}, false
	}
	return *s.lastHTTP, true
}

func (s *Session) PrintToPDF(ctx context.Context, opts PDFOptions) (io.ReadCloser, error) {
	// BiDi browsingContext.print does not support DisplayHeaderFooter.
	if opts.DisplayHeaderFooter {
		log.Printf("bidi: DisplayHeaderFooter is not supported by Firefox/BiDi, ignoring")
	}

	params := map[string]any{
		"context":     s.contextID,
		"shrinkToFit": true,
	}

	if opts.Landscape {
		params["orientation"] = "landscape"
	}
	if opts.PrintBackground {
		params["background"] = true
	}
	if opts.PageRanges != "" {
		params["pageRanges"] = []string{opts.PageRanges}
	}

	// BiDi uses centimeters; CDP uses inches. Convert when set.
	// nil = unset (use browser default), non-nil = explicit (0 is valid for borderless).
	page := map[string]any{}
	if opts.PaperWidth != nil {
		page["width"] = *opts.PaperWidth * 2.54
	}
	if opts.PaperHeight != nil {
		page["height"] = *opts.PaperHeight * 2.54
	}
	if len(page) > 0 {
		params["page"] = page
	}

	margin := map[string]any{}
	if opts.MarginTop != nil {
		margin["top"] = *opts.MarginTop * 2.54
	}
	if opts.MarginBottom != nil {
		margin["bottom"] = *opts.MarginBottom * 2.54
	}
	if opts.MarginLeft != nil {
		margin["left"] = *opts.MarginLeft * 2.54
	}
	if opts.MarginRight != nil {
		margin["right"] = *opts.MarginRight * 2.54
	}
	if len(margin) > 0 {
		params["margin"] = margin
	}

	result, err := s.conn.Send("browsingContext.print", params)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return base64Field(result, "data")
}

func (s *Session) CaptureScreenshot(ctx context.Context, opts ScreenshotOptions) (io.ReadCloser, error) {
	params := map[string]any{
		"context": s.contextID,
	}

	if opts.Format == "jpeg" {
		params["format"] = map[string]any{
			"type":    "image/jpeg",
			"quality": opts.Quality,
		}
	}

	if opts.FullPage {
		params["origin"] = "document"
	}

	result, err := s.conn.Send("browsingContext.captureScreenshot", params)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return base64Field(result, "data")
}

func (s *Session) CaptureSnapshot(ctx context.Context) (io.ReadCloser, error) {
	return nil, errors.Errorf("MHTML capture is not supported with Firefox/BiDi (Chrome-only)")
}

func (s *Session) AccessibilityTree(ctx context.Context) (io.ReadCloser, error) {
	return nil, errors.Errorf("accessibility tree capture is not supported with Firefox/BiDi (Chrome-only)")
}

func (s *Session) ExtractText(ctx context.Context) (io.ReadCloser, error) {
	return s.evaluateString(ctx, "document.body.innerText")
}

func (s *Session) GetDocumentHTML(ctx context.Context) (io.ReadCloser, error) {
	return s.evaluateString(ctx, "document.documentElement.outerHTML")
}

// evaluateString runs a BiDi script.evaluate expecting a string result
// and returns it as a reader. Shared by ExtractText and GetDocumentHTML.
func (s *Session) evaluateString(ctx context.Context, expression string) (io.ReadCloser, error) {
	result, err := s.conn.Send("script.evaluate", map[string]any{
		"expression":      expression,
		"target":          map[string]string{"context": s.contextID},
		"awaitPromise":    false,
		"resultOwnership": "none",
	})
	if err != nil {
		return nil, errors.Wrap(err)
	}

	var parsed struct {
		Result struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrapf(err, "raw BiDi result: %s", string(result))
	}

	return io.NopCloser(strings.NewReader(parsed.Result.Value)), nil
}

func (s *Session) BrowserInfo(ctx context.Context) (BrowserInfo, error) {
	info := BrowserInfo{
		Name:      "firefox",
		Version:   s.capabilities.BrowserVersion,
		UserAgent: s.capabilities.UserAgent,
		Platform:  s.capabilities.PlatformName,
		JSEngine:  "SpiderMonkey",
	}
	if s.firefox != nil {
		info.CommandLine = s.firefox.Args()
	}
	return info, nil
}

func (s *Session) Close() error {
	if s.networkSub != nil {
		s.networkSub.Close()
		s.networkSub = nil
	}
	// Send network.removeIntercept to the BiDi server for each
	// registered intercept BEFORE closing the local subscription. The
	// caller's defer-RemoveIntercept normally handles this, but if a
	// caller forgot — or aborts mid-fetch and skips its own cleanup
	// — leaving the intercept registered means any in-flight responses
	// remain paused until the connection is torn down. We then close
	// the subscription locally. Errors are swallowed: the connection
	// is presumably about to die anyway, and forcing Close() to fail
	// would mask the more important error path.
	s.intercepts.Range(func(k, v any) bool {
		if s.conn != nil {
			if id, ok := k.(string); ok {
				_, _ = s.conn.Send("network.removeIntercept", map[string]any{
					"intercept": id,
				})
			}
		}
		v.(*bidi.Subscription).Close()
		s.intercepts.Delete(k)
		return true
	})
	if s.conn != nil {
		// Best-effort session end.
		_, _ = s.conn.Send("session.end", map[string]any{})
		s.conn.Close()
	}
	if s.firefox != nil {
		s.firefox.Close()
	}
	return nil
}

// BrowserClose asks Firefox to exit on its own via BiDi browser.close,
// allowing it to flush on-disk state (cookies.sqlite, localStorage)
// before termination — Session.Close() SIGKILLs, which skips the flush.
// Callers MUST still invoke Close() afterward to release subscription,
// connection, and process resources.
func (s *Session) BrowserClose(ctx context.Context) error {
	if s.conn == nil {
		return nil
	}
	_, err := s.conn.Send("browser.close", map[string]any{})
	return errors.Wrap(err)
}

// base64Field extracts a base64-encoded field from a JSON result and
// returns a reader that decodes it.
func base64Field(result json.RawMessage, field string) (io.ReadCloser, error) {
	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, errors.Wrap(err)
	}

	data, ok := parsed[field]
	if !ok {
		return nil, errors.Errorf("BiDi response missing %q field", field)
	}

	return io.NopCloser(
		base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)),
	), nil
}

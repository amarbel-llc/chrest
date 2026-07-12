package capturebatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"code.linenisgreat.com/chrest/go/internal/0/markdown"
	"code.linenisgreat.com/chrest/go/internal/0/monolith"
	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
	"code.linenisgreat.com/chrest/go/internal/delta/tools"
)

// PayloadMediaTypes enumerates the supported capture formats (the value
// is the format's IANA media type; the type is implied by the payload
// node's RFC 0003 type string). Membership doubles as the format-validity
// check in runOne.
var PayloadMediaTypes = map[string]string{
	"text":              "text/plain; charset=utf-8",
	"pdf":               "application/pdf",
	"screenshot":        "image/png",
	"mhtml":             "multipart/related",
	"a11y":              "application/json",
	"html-monolith":     "text/html; charset=utf-8",
	"html-outer":        "text/html; charset=utf-8",
	"markdown-full":     "text/markdown; charset=utf-8",
	"markdown-reader":   "text/markdown; charset=utf-8",
	"markdown-selector": "text/markdown; charset=utf-8",
}

// Options configure the runner; most come from BatchInput.
type Options struct {
	CapturerVersion string
	Writer          WriterSpec
	Target          string
	Defaults        *Defaults
}

// Run executes every capture in order and returns the batch output. The
// runner never fails fatally on per-capture errors — they become
// CaptureResult.Error entries. Batch-level failures (e.g. an empty
// writer.cmd or target) are returned as errors.
func Run(ctx context.Context, captures []CaptureSpec, opts Options) (BatchOutput, error) {
	if len(opts.Writer.Cmd) == 0 {
		return BatchOutput{}, fmt.Errorf("writer.cmd MUST have at least one element")
	}
	if opts.Target == "" {
		return BatchOutput{}, fmt.Errorf("target MUST be a non-empty string")
	}

	out := BatchOutput{
		Schema: BatchSchema,
		Plugin: PluginInfo{
			Name:    CapturerName,
			Version: opts.CapturerVersion,
		},
		Errors:   []ProtocolError{},
		Captures: make([]CaptureResult, 0, len(captures)),
	}

	for _, c := range captures {
		resolved := Resolve(c, opts.Defaults)
		out.Captures = append(out.Captures, runOne(ctx, resolved, opts))
	}

	return out, nil
}

func runOne(ctx context.Context, r Resolved, opts Options) CaptureResult {
	w := newSizeTrackingWriter(newCmdWriter(opts.Writer.Cmd))
	return runOneWithWriter(ctx, r, opts.Target, opts.CapturerVersion, w)
}

// runOneWithWriter drives one capture end-to-end — open a session,
// navigate, capture the format's bytes, optionally normalize, assemble
// the receipt — against an already-constructed sizeTrackingWriter. Shared
// by the v1 capture-batch transport (runOne, wrapping a cmdWriter bound to
// writer.cmd) and the v2 capture-serve transport (wrapping the
// capture_plugin.Writer Serve hands the batch handler): the receipt
// assembly itself has no transport-specific logic.
func runOneWithWriter(ctx context.Context, r Resolved, target, capturerVersion string, w *sizeTrackingWriter) CaptureResult {
	entry := CaptureResult{Name: r.Name}

	if _, ok := PayloadMediaTypes[r.Format]; !ok {
		entry.Error = &ProtocolError{
			Kind:    "bad-format",
			Message: fmt.Sprintf("unknown capture format %q", r.Format),
		}
		return entry
	}

	session, err := openSession(ctx, r.Browser)
	if err != nil {
		entry.Error = &ProtocolError{Kind: "session-open-failed", Message: err.Error()}
		return entry
	}
	defer session.Close()

	if err := session.Navigate(ctx, target); err != nil {
		entry.Error = &ProtocolError{Kind: "navigate-failed", Message: err.Error()}
		return entry
	}

	rc, err := runCaptureFormat(ctx, session, r, target)
	if err != nil {
		entry.Error = &ProtocolError{Kind: "capture-failed", Message: err.Error()}
		return entry
	}
	defer rc.Close()

	// Normalization is applied for formats whose normalizer is implemented
	// when the batch requests it; the residue is recorded into the outcome
	// subtree. Formats without a normalizer are deterministic as-captured
	// and stream through verbatim.
	var payload io.Reader = rc
	var stripped map[string]any
	if r.Normalize && hasNormalizer(r.Format) {
		normalized, s, err := NormalizeStream(r.Format, rc)
		if err != nil {
			entry.Error = &ProtocolError{Kind: "normalize-failed", Message: err.Error()}
			return entry
		}
		payload = normalized
		stripped = s
	}

	var httpResp *firefox.HTTPResponse
	if resp, ok := session.LastNavigationHTTP(); ok {
		httpResp = &resp
	}

	browserInfo, _ := session.BrowserInfo(ctx) // best-effort; empty fields are fine
	if browserInfo.Name == "" {
		browserInfo.Name = r.Browser
	}

	receiptDigest, err := buildReceipt(ctx, w, r, browserInfo, httpResp, stripped, payload, target, capturerVersion)
	if err != nil {
		entry.Error = &ProtocolError{Kind: "receipt-write-failed", Message: err.Error()}
		return entry
	}
	entry.Receipt = &ReceiptRef{ID: receiptDigest, Size: w.sizeOf(receiptDigest)}
	return entry
}

// hasNormalizer reports whether a format has an implemented byte-stability
// normalizer (the `normalize` request applies only to these formats; the
// rest are deterministic as-captured).
func hasNormalizer(format string) bool {
	switch format {
	case "text", "screenshot", "pdf", "mhtml":
		return true
	default:
		return false
	}
}

func openSession(ctx context.Context, browser string) (*firefox.Session, error) {
	switch browser {
	case "firefox", "":
		return firefox.NewSession(ctx)
	default:
		return nil, fmt.Errorf("unknown browser backend %q; only firefox is supported", browser)
	}
}

// runCaptureFormat dispatches to the session method matching format.
// baseURL is forwarded to encoders that need it for relative-asset
// resolution (currently only html-monolith).
func runCaptureFormat(ctx context.Context, s *firefox.Session, r Resolved, baseURL string) (io.ReadCloser, error) {
	var opts tools.CaptureParams
	if len(r.Options) > 0 {
		_ = json.Unmarshal(r.Options, &opts) // best-effort field copy
	}
	switch r.Format {
	case "text":
		return s.ExtractText(ctx)
	case "pdf":
		return s.PrintToPDF(ctx, firefox.PDFOptions{
			Landscape:           opts.Landscape,
			DisplayHeaderFooter: !opts.NoHeaders,
			PrintBackground:     opts.Background,
			PaperWidth:          opts.PaperWidth.Value,
			PaperHeight:         opts.PaperHeight.Value,
			MarginTop:           opts.MarginTop.Value,
			MarginBottom:        opts.MarginBottom.Value,
			MarginLeft:          opts.MarginLeft.Value,
			MarginRight:         opts.MarginRight.Value,
		})
	case "screenshot":
		return s.CaptureScreenshot(ctx, firefox.ScreenshotOptions{
			Format:   "png",
			FullPage: opts.FullPage,
		})
	case "mhtml":
		return s.CaptureSnapshot(ctx)
	case "a11y":
		return s.AccessibilityTree(ctx)
	case "html-outer":
		return s.GetDocumentHTML(ctx)
	case "html-monolith":
		dom, err := s.GetDocumentHTML(ctx)
		if err != nil {
			return nil, err
		}
		defer dom.Close()
		return monolith.Process(ctx, dom, baseURL)
	case "markdown-full":
		dom, err := s.GetDocumentHTML(ctx)
		if err != nil {
			return nil, err
		}
		defer dom.Close()
		return markdown.ConvertFull(ctx, dom)
	case "markdown-reader":
		dom, err := s.GetDocumentHTML(ctx)
		if err != nil {
			return nil, err
		}
		defer dom.Close()
		return markdown.ConvertReader(ctx, dom, baseURL)
	case "markdown-selector":
		if opts.Selector == "" {
			return nil, fmt.Errorf("markdown-selector requires capture.options.selector to be set")
		}
		dom, err := s.GetDocumentHTML(ctx)
		if err != nil {
			return nil, err
		}
		defer dom.Close()
		return markdown.ConvertSelector(ctx, dom, opts.Selector)
	default:
		return nil, fmt.Errorf("unknown capture format %q", r.Format)
	}
}

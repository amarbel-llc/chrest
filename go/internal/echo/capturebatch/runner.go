package capturebatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"code.linenisgreat.com/chrest/go/internal/0/markdown"
	"code.linenisgreat.com/chrest/go/internal/0/monolith"
	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
	"code.linenisgreat.com/chrest/go/internal/delta/tools"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
)

// Options configure the runner; most come from Input.
type Options struct {
	CapturerVersion string
	Writer          WriterSpec
	Target          string
	Defaults        *Defaults
}

// Run executes every capture in order and returns the batch output. The
// runner never fails fatally on per-capture errors — they become
// OutputCapture.Error entries. Batch-level failures (empty writer.cmd /
// target, or a failed capabilities write) are returned as errors and the
// caller exits non-zero.
func Run(ctx context.Context, inputCaptures []InputCapture, opts Options) (Output, error) {
	if len(opts.Writer.Cmd) == 0 {
		return Output{}, fmt.Errorf("writer.cmd MUST have at least one element")
	}
	if opts.Target == "" {
		return Output{}, fmt.Errorf("target MUST be a non-empty string")
	}

	w := newSubprocessWriter(opts.Writer.Cmd)
	host := capture_plugin.GatherHost()

	// The capabilities blob is identical across every capture in the
	// batch, so write it once and reuse the markl id (RFC 0002 §dedup).
	capID, err := writeCapabilities(ctx, w)
	if err != nil {
		return Output{}, fmt.Errorf("write capabilities artifact: %w", err)
	}

	out := Output{
		Schema:   OutputSchema,
		Plugin:   PluginInfo{Name: CapturerName, Version: opts.CapturerVersion},
		Errors:   []Error{},
		Captures: make([]OutputCapture, 0, len(inputCaptures)),
	}

	for _, raw := range inputCaptures {
		r := Resolve(raw, opts.Defaults)
		out.Captures = append(out.Captures, runOne(ctx, r, opts, w, host, capID))
	}

	return out, nil
}

func runOne(
	ctx context.Context,
	r Resolved,
	opts Options,
	w *subprocessWriter,
	host capture_plugin.HostInfo,
	capID string,
) OutputCapture {
	entry := OutputCapture{Name: r.Name}

	if !knownFormat(r.Format) {
		entry.Error = &CaptureError{Kind: "bad-format", Message: fmt.Sprintf("unknown capture format %q", r.Format)}
		return entry
	}
	if r.Normalize && !normalizeSupported(r.Format) {
		entry.Error = &CaptureError{
			Kind:    "not-implemented",
			Message: fmt.Sprintf("normalization for %q is not implemented (RFC 0003 deferred); pass normalize=false", r.Format),
		}
		return entry
	}

	session, err := openSession(ctx, r.Browser)
	if err != nil {
		entry.Error = &CaptureError{Kind: "session-open-failed", Message: err.Error()}
		return entry
	}
	defer session.Close()

	if err := session.Navigate(ctx, opts.Target); err != nil {
		entry.Error = &CaptureError{Kind: "navigate-failed", Message: err.Error()}
		return entry
	}

	// Payload subtree first (post-order): write the captured (optionally
	// normalized) bytes as a raw leaf and keep the typed reference.
	payloadRef, stripped, err := writePayload(ctx, w, session, r, opts.Target)
	if err != nil {
		entry.Error = &CaptureError{Kind: "payload-write-failed", Message: err.Error()}
		return entry
	}

	browserInfo, _ := session.BrowserInfo(ctx) // best-effort; empty fields fine
	if browserInfo.Name == "" {
		browserInfo.Name = r.Browser
	}
	var httpResp *firefox.HTTPResponse
	if resp, ok := session.LastNavigationHTTP(); ok {
		httpResp = &resp
	}

	receiptID, receiptSize, err := writeReceipt(ctx, w, r, opts, host, capID, browserInfo, httpResp, stripped, payloadRef)
	if err != nil {
		entry.Error = &CaptureError{Kind: "receipt-write-failed", Message: err.Error()}
		return entry
	}

	entry.Receipt = &ReceiptRef{ID: receiptID, Size: receiptSize}
	return entry
}

// writeCapabilities materializes the jcs-chrest-capture-capabilities-v1
// node and returns its markl id.
func writeCapabilities(ctx context.Context, w *subprocessWriter) (string, error) {
	body, err := capture_plugin.JCS(capabilitiesBody())
	if err != nil {
		return "", err
	}
	node := capture_plugin.BuildNode(capType, nil, body)
	id, _, err := w.WriteBlob(ctx, bytes.NewReader(node))
	return id, err
}

// writePayload runs the capture, optionally normalizes, streams the bytes
// to the writer as a raw payload leaf, and returns the typed reference
// plus any stripped residue for the outcome node. The payload is a raw
// leaf (not a hyphence node) so binary formats round-trip byte-exactly on
// restore — the type travels on the receipt's reference, as with the git
// binding's object leaves.
func writePayload(
	ctx context.Context,
	w *subprocessWriter,
	session *firefox.Session,
	r Resolved,
	target string,
) (capture_plugin.Ref, map[string]any, error) {
	rc, err := runCaptureFormat(ctx, session, r, target)
	if err != nil {
		return capture_plugin.Ref{}, nil, err
	}
	defer rc.Close()

	var src io.Reader = rc
	var stripped map[string]any
	if r.Normalize {
		normalized, st, nerr := NormalizeStream(r.Format, rc)
		if nerr != nil {
			return capture_plugin.Ref{}, nil, nerr
		}
		src, stripped = normalized, st
	}

	digest, _, err := w.WriteBlob(ctx, src)
	if err != nil {
		return capture_plugin.Ref{}, nil, err
	}
	return capture_plugin.LockedRef("payload", digest, payloadType(r.Format)), stripped, nil
}

// writeReceipt assembles the RFC 0002 merkle tree via the shared
// WriteReceipt, threading the web-binding plugin nodes (environment,
// outcome http.*) and the payload reference. It returns the root receipt
// markl id and its size (the writer's last write — the receipt is the
// final node WriteReceipt emits).
func writeReceipt(
	ctx context.Context,
	w *subprocessWriter,
	r Resolved,
	opts Options,
	host capture_plugin.HostInfo,
	capID string,
	browserInfo firefox.BrowserInfo,
	httpResp *firefox.HTTPResponse,
	stripped map[string]any,
	payloadRef capture_plugin.Ref,
) (string, int64, error) {
	op := outcomePlugin(opts.Target, httpResp)

	digest, err := capture_plugin.WriteReceipt(ctx, w, capture_plugin.ReceiptParams{
		Kind: captureKind,
		Invocation: capture_plugin.Invocation{
			Target:    opts.Target,
			Format:    r.Format,
			Normalize: r.Normalize,
			Options:   optionsMap(r.Options),
		},
		Host: host,
		Binary: capture_plugin.BinaryInfo{
			Name:           CapturerName,
			Version:        opts.CapturerVersion,
			CapabilitiesId: capID,
		},
		PluginEnv: capture_plugin.PluginEnv{
			TypeString: envType,
			Body:       environmentBody(browserInfo, r.Isolation),
		},
		OutcomePlugin:   &op,
		OutcomeStripped: stripped,
		PayloadRefs:     []capture_plugin.Ref{payloadRef},
	})
	if err != nil {
		return "", 0, err
	}
	return digest, w.lastSize, nil
}

// optionsMap decodes captures[].options into the object echoed verbatim
// into invocation.options ({} when absent).
func optionsMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m) // best-effort; options is an object per RFC
	}
	return m
}

func openSession(ctx context.Context, browser string) (*firefox.Session, error) {
	switch browser {
	case "firefox", "":
		return firefox.NewSession(ctx)
	default:
		return nil, fmt.Errorf("unknown browser backend %q; only firefox is supported", browser)
	}
}

// runCaptureFormat dispatches to the session method matching format and
// returns the raw capture stream. baseURL is forwarded to encoders that
// resolve relative assets (html-monolith, markdown-reader).
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

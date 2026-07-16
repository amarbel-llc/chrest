package tools

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
)

type mockSession struct {
	htmlBody      string
	htmlErr       error
	textBody      string
	textErr       error
	htmlCallCount int
	textCallCount int
}

func (m *mockSession) Navigate(ctx context.Context, url string, opts firefox.NavigateOptions) error {
	return nil
}
func (m *mockSession) SetViewport(ctx context.Context, width, height int) error { return nil }

func (m *mockSession) GetDocumentHTML(ctx context.Context) (io.ReadCloser, error) {
	m.htmlCallCount++
	if m.htmlErr != nil {
		return nil, m.htmlErr
	}
	return io.NopCloser(strings.NewReader(m.htmlBody)), nil
}

func (m *mockSession) ExtractText(ctx context.Context) (io.ReadCloser, error) {
	m.textCallCount++
	if m.textErr != nil {
		return nil, m.textErr
	}
	return io.NopCloser(strings.NewReader(m.textBody)), nil
}

func (m *mockSession) PrintToPDF(ctx context.Context, opts firefox.PDFOptions) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not supported in mock")
}

func (m *mockSession) CaptureScreenshot(ctx context.Context, opts firefox.ScreenshotOptions) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not supported in mock")
}

func (m *mockSession) CaptureSnapshot(ctx context.Context) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not supported in mock")
}

func (m *mockSession) AccessibilityTree(ctx context.Context) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not supported in mock")
}

func (m *mockSession) BrowserInfo(ctx context.Context) (firefox.BrowserInfo, error) {
	return firefox.BrowserInfo{}, nil
}

func (m *mockSession) LastNavigationHTTP() (firefox.HTTPResponse, bool) {
	return firefox.HTTPResponse{}, false
}

func (m *mockSession) Close() error { return nil }

func TestMultiExtract_HappyPath(t *testing.T) {
	m := &mockSession{
		htmlBody: "<html><body><h1>Hello</h1></body></html>",
		textBody: "Hello",
	}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"text", "html-outer"},
	})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("format %s: unexpected error: %v", r.Format, r.Err)
		}
		if len(r.Data) == 0 {
			t.Errorf("format %s: empty data", r.Format)
		}
	}
	if results[0].Format != "text" {
		t.Errorf("expected first result format text, got %s", results[0].Format)
	}
	if string(results[0].Data) != "Hello" {
		t.Errorf("text data: got %q, want %q", results[0].Data, "Hello")
	}
	if results[1].Format != "html-outer" {
		t.Errorf("expected second result format html-outer, got %s", results[1].Format)
	}
	if string(results[1].Data) != m.htmlBody {
		t.Errorf("html-outer data: got %q, want %q", results[1].Data, m.htmlBody)
	}
}

func TestMultiExtract_DOMHoisting(t *testing.T) {
	m := &mockSession{
		htmlBody: "<html><body><h1>Hello</h1></body></html>",
	}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"html-outer", "markdown-full"},
	})
	if m.htmlCallCount != 1 {
		t.Errorf("GetDocumentHTML called %d times, want 1", m.htmlCallCount)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("format %s: unexpected error: %v", r.Format, r.Err)
		}
		if len(r.Data) == 0 {
			t.Errorf("format %s: empty data", r.Format)
		}
	}
}

func TestMultiExtract_MixedFamilies(t *testing.T) {
	m := &mockSession{
		htmlBody: "<html><body>content</body></html>",
		textBody: "content",
	}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"text", "html-outer"},
	})
	if m.htmlCallCount != 1 {
		t.Errorf("GetDocumentHTML called %d times, want 1", m.htmlCallCount)
	}
	if m.textCallCount != 1 {
		t.Errorf("ExtractText called %d times, want 1", m.textCallCount)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("format %s: unexpected error: %v", r.Format, r.Err)
		}
	}
}

func TestMultiExtract_TextErrorIsolation(t *testing.T) {
	m := &mockSession{
		htmlBody: "<html><body>works</body></html>",
		textErr:  fmt.Errorf("ExtractText failed"),
	}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"text", "html-outer"},
	})
	if results[0].Err == nil {
		t.Error("expected text to fail")
	}
	if results[1].Err != nil {
		t.Errorf("html-outer should succeed, got error: %v", results[1].Err)
	}
	if string(results[1].Data) != m.htmlBody {
		t.Errorf("html-outer data: got %q, want %q", results[1].Data, m.htmlBody)
	}
}

func TestMultiExtract_DOMErrorPropagation(t *testing.T) {
	m := &mockSession{
		htmlErr:  fmt.Errorf("GetDocumentHTML failed"),
		textBody: "still works",
	}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"html-outer", "markdown-full", "text"},
	})
	if results[0].Err == nil {
		t.Error("expected html-outer to fail")
	}
	if results[1].Err == nil {
		t.Error("expected markdown-full to fail")
	}
	if results[2].Err != nil {
		t.Errorf("text should succeed, got error: %v", results[2].Err)
	}
	if string(results[2].Data) != "still works" {
		t.Errorf("text data: got %q, want %q", results[2].Data, "still works")
	}
}

func TestMultiExtract_ValidationEmptyURL(t *testing.T) {
	_, err := MultiExtract(context.Background(), MultiExtractParams{
		Formats: []string{"text"},
	})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestMultiExtract_ValidationEmptyFormats(t *testing.T) {
	_, err := MultiExtract(context.Background(), MultiExtractParams{
		URL: "https://example.com",
	})
	if err == nil {
		t.Fatal("expected error for empty formats")
	}
}

func TestMultiExtract_ValidationUnknownFormat(t *testing.T) {
	_, err := MultiExtract(context.Background(), MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"text", "bogus"},
	})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the bad format, got: %v", err)
	}
}

func TestMultiExtract_SingleFormat(t *testing.T) {
	m := &mockSession{textBody: "single"}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:     "https://example.com",
		Formats: []string{"text"},
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("unexpected error: %v", results[0].Err)
	}
	if string(results[0].Data) != "single" {
		t.Errorf("got %q, want %q", results[0].Data, "single")
	}
	if m.htmlCallCount != 0 {
		t.Errorf("GetDocumentHTML should not be called for text-only, got %d", m.htmlCallCount)
	}
}

func TestMultiExtract_AllDOMFormatsShareOneCall(t *testing.T) {
	m := &mockSession{
		htmlBody: "<html><body><article><h1>Article</h1><p>Content</p></article></body></html>",
	}
	results := multiExtractFromSession(context.Background(), m, MultiExtractParams{
		URL:      "https://example.com",
		Formats:  []string{"html-outer", "markdown-full", "markdown-reader", "markdown-selector"},
		Selector: "article",
	})
	if m.htmlCallCount != 1 {
		t.Errorf("GetDocumentHTML called %d times, want 1", m.htmlCallCount)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("format %s: unexpected error: %v", r.Format, r.Err)
		}
		if len(r.Data) == 0 {
			t.Errorf("format %s: empty data", r.Format)
		}
	}
}

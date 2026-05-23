package capturebatch

import (
	"time"

	"code.linenisgreat.com/chrest/go/internal/charlie/firefox"
)

// EnvelopeSchemaPreview is emitted when the backend cannot populate
// the RFC-required `http.status` + `http.headers` fields (CDP /
// headless-Chrome backend today, chrest#24 follow-up work). Marked
// `-preview` so v1-strict consumers reject it per RFC forward-compat
// rules, while preview-tolerant consumers opt in knowingly.
const EnvelopeSchemaPreview = "web-capture-archive.envelope/v1-preview"

// EnvelopeSchemaV1 is emitted when http.* is fully populated. Today
// this is produced by the Firefox/BiDi backend via
// network.responseCompleted event subscription.
const EnvelopeSchemaV1 = "web-capture-archive.envelope/v1"

// EnvelopeMediaType is the Content-Type of the canonicalized envelope
// bytes. Stable across schema versions — the discriminator is the
// `schema` field inside the blob.
const EnvelopeMediaType = "application/vnd.web-capture-archive.envelope+json"

// BuildEnvelope assembles the envelope artifact for a resolved
// capture and returns the JCS-canonicalized bytes. When http is
// non-nil, emits the full v1 schema with http.* fields populated;
// when nil, emits v1-preview with the http key omitted.
//
// Per RFC 0001 §Envelope Artifact:
//   - `schema`, `url`, `captured_at` are required.
//   - `http.status`, `http.headers` are required by the RFC v1 but
//     only present when the backend supports network-event capture.
//   - `stripped.<format>` is optional; the format normalizer returns
//     what it removed, or nil if nothing.
func BuildEnvelope(url string, capturedAt time.Time, stripped map[string]any, http *firefox.HTTPResponse) ([]byte, error) {
	schema := EnvelopeSchemaPreview
	if http != nil {
		schema = EnvelopeSchemaV1
	}
	doc := map[string]any{
		"schema":      schema,
		"url":         url,
		"captured_at": capturedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if http != nil {
		headers := make([]any, 0, len(http.Headers))
		for _, h := range http.Headers {
			headers = append(headers, map[string]any{
				"name":  h.Name,
				"value": h.Value,
			})
		}
		httpEntry := map[string]any{
			"status":  int64(http.Status),
			"headers": headers,
		}
		if http.URL != "" && http.URL != url {
			httpEntry["final_url"] = http.URL
		}
		if http.TimingMs > 0 {
			httpEntry["timing_ms"] = http.TimingMs
		}
		doc["http"] = httpEntry
	}
	if len(stripped) > 0 {
		doc["stripped"] = stripped
	}
	return Canonicalize(doc)
}

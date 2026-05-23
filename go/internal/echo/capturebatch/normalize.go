package capturebatch

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Normalize produces the payload bytes that the writer should store
// when split=true. Each format has its own normalization rules
// specified in RFC 0001 §Payload Artifact. Unsupported formats return
// a not-implemented error so the runner can surface it as a
// per-capture error.
//
// MVP scope: "text", "screenshot", "pdf", and "mhtml" are implemented.
// "a11y" is blocked on chrest#14 (Chrome SIGTRAP on kernel 6.17) and
// returns the not-implemented error until that lifts.
func Normalize(format string, raw []byte) (normalized []byte, stripped map[string]any, err error) {
	switch format {
	case "text":
		return normalizeText(raw), nil, nil
	case "screenshot":
		return normalizePNG(raw)
	case "pdf":
		return normalizePDF(raw)
	case "mhtml":
		return normalizeMHTML(raw)
	case "a11y":
		return nil, nil, fmt.Errorf("split=true normalization not implemented for %s (tracked in chrest#22 follow-up)", format)
	default:
		return nil, nil, fmt.Errorf("unknown capture format %q", format)
	}
}

// NormalizeStream is the streaming counterpart to Normalize. It reads
// the full input into memory, normalizes, and returns a reader plus
// the stripped map. Normalization is unavoidably buffering for most
// formats (need to see the whole document), so streaming here is
// about interface symmetry with StreamCapture rather than memory.
func NormalizeStream(format string, src io.Reader) (io.Reader, map[string]any, error) {
	raw, err := io.ReadAll(src)
	if err != nil {
		return nil, nil, err
	}
	normalized, stripped, err := Normalize(format, raw)
	if err != nil {
		return nil, nil, err
	}
	return bytes.NewReader(normalized), stripped, nil
}

// normalizeText applies RFC 0001 §text normalization rules:
//   - line endings collapsed to a single LF
//   - trailing whitespace (spaces and tabs) removed from each line
//
// The result is stable for identical-content pages regardless of the
// browser's line-ending conventions or trailing-whitespace quirks.
func normalizeText(raw []byte) []byte {
	// Collapse CRLF and lone CR into LF first so the per-line loop
	// only needs to look for LF delimiters.
	s := string(raw)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	var out bytes.Buffer
	out.Grow(len(s))
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		out.WriteString(strings.TrimRight(line, " \t"))
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}

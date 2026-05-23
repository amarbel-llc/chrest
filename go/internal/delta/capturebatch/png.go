package capturebatch

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// pngSignature is the 8-byte PNG file signature.
var pngSignature = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// nonDeterministicTextKeywords identifies well-known PNG tEXt/zTXt/iTXt
// keywords whose payloads vary across captures of the same image. When
// split=true, the normalizer strips these so two captures of the same
// page produce identical payload bytes.
//
// The list is a subset of PNG §11.3.4 "Textual Information" — kept
// narrow so we don't accidentally strip text the user explicitly added
// to the image.
var nonDeterministicTextKeywords = map[string]bool{
	"Creation Time": true,
	"Modify Time":   true,
	"Software":      true,
	"Source":        true,
	"Comment":       true,
}

// normalizePNG strips per-run metadata chunks from a PNG per
// RFC 0001 §screenshot. Returns the normalized bytes and a
// description of what was removed for the envelope's
// `stripped.screenshot` field.
//
// Stripped:
//   - tIME chunk (image modification time).
//   - tEXt/zTXt/iTXt chunks whose keyword is in
//     nonDeterministicTextKeywords.
//
// Not done here (SHOULD per RFC, deferred):
//   - Re-encoding IDAT with a deterministic zlib level. That requires
//     decode + re-compress of pixel data, which the minimal-dep
//     normalizer avoids. Two captures from the same browser version
//     typically produce identical IDAT data anyway.
func normalizePNG(raw []byte) ([]byte, map[string]any, error) {
	if len(raw) < len(pngSignature) || !bytes.Equal(raw[:8], pngSignature) {
		return nil, nil, fmt.Errorf("png: missing signature")
	}

	var out bytes.Buffer
	out.Write(pngSignature)

	type strippedChunk struct {
		Type    string `json:"type"`
		Keyword string `json:"keyword,omitempty"`
		Raw     string `json:"raw"` // hex of the original data section
	}
	var stripped []strippedChunk

	pos := 8
	for pos < len(raw) {
		if pos+12 > len(raw) {
			return nil, nil, fmt.Errorf("png: truncated chunk header at offset %d", pos)
		}
		length := binary.BigEndian.Uint32(raw[pos : pos+4])
		chunkType := string(raw[pos+4 : pos+8])
		dataStart := pos + 8
		dataEnd := dataStart + int(length)
		crcEnd := dataEnd + 4
		if crcEnd > len(raw) {
			return nil, nil, fmt.Errorf("png: truncated chunk %q at offset %d", chunkType, pos)
		}
		data := raw[dataStart:dataEnd]

		skip := false
		var keyword string
		switch chunkType {
		case "tIME":
			skip = true
		case "tEXt", "zTXt", "iTXt":
			// Keyword is the bytes up to the first NUL. Length 1..79.
			if idx := bytes.IndexByte(data, 0); idx > 0 {
				keyword = string(data[:idx])
			}
			if nonDeterministicTextKeywords[keyword] {
				skip = true
			}
		}

		if skip {
			stripped = append(stripped, strippedChunk{
				Type:    chunkType,
				Keyword: keyword,
				Raw:     hex.EncodeToString(data),
			})
		} else {
			out.Write(raw[pos:crcEnd])
		}

		pos = crcEnd
		if chunkType == "IEND" {
			break
		}
	}

	var strippedMap map[string]any
	if len(stripped) > 0 {
		items := make([]any, 0, len(stripped))
		for _, s := range stripped {
			obj := map[string]any{
				"type": s.Type,
				"raw":  s.Raw,
			}
			if s.Keyword != "" {
				obj["keyword"] = s.Keyword
			}
			items = append(items, obj)
		}
		strippedMap = map[string]any{"screenshot": map[string]any{"chunks": items}}
	}

	return out.Bytes(), strippedMap, nil
}

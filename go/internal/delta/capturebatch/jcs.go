package capturebatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Canonicalize encodes v as JCS (RFC 8785) bytes.
//
// Our schema uses strings, integers, booleans, objects, arrays, and
// null — no floating-point numbers. This implementation is correct for
// that subset:
//
//   - map keys are sorted by UTF-16 code units (same as alphabetical
//     for ASCII-only keys, which our schema uses);
//   - objects and arrays emit with no whitespace;
//   - strings are escaped per RFC 8785 §3.2.2.2 (only required control
//     chars are escaped; Go's default json.Encoder escapes more);
//   - booleans and nulls emit as `true` / `false` / `null`;
//   - integers (int, int64, json.Number) emit in base 10 with no
//     leading zeros or `+`.
//
// If the schema ever grows floating-point fields, this will need the
// ES6 ToString semantics from RFC 8785 §3.2.2.3; that is intentionally
// out of MVP scope.
func Canonicalize(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encode(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		encodeString(buf, x)
	case int:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
	case json.Number:
		// We treat json.Number as an integer literal. Reject anything
		// that doesn't parse as int64 — no floats in our schema.
		n, err := x.Int64()
		if err != nil {
			return fmt.Errorf("jcs: non-integer number %q unsupported", x)
		}
		buf.WriteString(strconv.FormatInt(n, 10))
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encode(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sortKeysUTF16(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			encodeString(buf, k)
			buf.WriteByte(':')
			if err := encode(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("jcs: unsupported type %T", v)
	}
	return nil
}

// sortKeysUTF16 sorts keys by UTF-16 code unit sequence per RFC 8785.
// For ASCII-only keys this is identical to lexicographic byte order.
func sortKeysUTF16(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		return lessUTF16(keys[i], keys[j])
	})
}

func lessUTF16(a, b string) bool {
	ra, rb := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	n := len(ra)
	if len(rb) < n {
		n = len(rb)
	}
	for i := 0; i < n; i++ {
		if ra[i] != rb[i] {
			return ra[i] < rb[i]
		}
	}
	return len(ra) < len(rb)
}

// encodeString writes a JCS-compliant JSON string literal.
// Per RFC 8785 §3.2.2.2: escape only the required control chars and
// `\"` / `\\`. Non-ASCII code points pass through as UTF-8 bytes.
func encodeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u`)
				buf.WriteString(fmtHex4(uint16(r)))
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

func fmtHex4(u uint16) string {
	var b strings.Builder
	const hex = "0123456789abcdef"
	b.WriteByte(hex[(u>>12)&0xF])
	b.WriteByte(hex[(u>>8)&0xF])
	b.WriteByte(hex[(u>>4)&0xF])
	b.WriteByte(hex[u&0xF])
	return b.String()
}

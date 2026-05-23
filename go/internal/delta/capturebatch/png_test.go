package capturebatch

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"testing"
)

// buildChunk assembles a PNG chunk with correct CRC32.
func buildChunk(chunkType string, data []byte) []byte {
	var b bytes.Buffer
	length := uint32(len(data))
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, length)
	b.Write(lengthBytes)
	b.WriteString(chunkType)
	b.Write(data)
	crc := crc32.ChecksumIEEE(append([]byte(chunkType), data...))
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	b.Write(crcBytes)
	return b.Bytes()
}

// makePNG assembles a minimal PNG with the given chunks wrapped by
// IHDR and IEND. IHDR data is fixed; callers provide what goes between.
func makePNG(middle ...[]byte) []byte {
	var b bytes.Buffer
	b.Write(pngSignature)
	// 1x1 grayscale IHDR (13 bytes of data).
	ihdr := []byte{
		0, 0, 0, 1, // width
		0, 0, 0, 1, // height
		8,    // bit depth
		0,    // color type (grayscale)
		0, 0, // compression/filter
		0, // interlace
	}
	b.Write(buildChunk("IHDR", ihdr))
	for _, c := range middle {
		b.Write(c)
	}
	b.Write(buildChunk("IEND", nil))
	return b.Bytes()
}

func TestNormalizePNG_StripsTIME(t *testing.T) {
	time_ := buildChunk("tIME", []byte{0x07, 0xE8, 0x01, 0x02, 0x03, 0x04, 0x05}) // Jan 2, 2024 03:04:05
	idat := buildChunk("IDAT", []byte{0x78, 0x9c, 0x62, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01})
	raw := makePNG(time_, idat)

	out, stripped, err := normalizePNG(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bytes.Contains(out, []byte("tIME")) {
		t.Errorf("normalized output still contains tIME chunk")
	}
	if !bytes.Contains(out, []byte("IDAT")) {
		t.Errorf("normalized output missing IDAT (we must only remove, not corrupt, unrelated chunks)")
	}
	chunks, _ := stripped["screenshot"].(map[string]any)["chunks"].([]any)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 stripped chunk, got %d", len(chunks))
	}
	first := chunks[0].(map[string]any)
	if first["type"] != "tIME" {
		t.Errorf("stripped chunk type = %v, want tIME", first["type"])
	}
}

func TestNormalizePNG_StripsNonDeterministicText(t *testing.T) {
	mkText := func(keyword, text string) []byte {
		return buildChunk("tEXt", append(append([]byte(keyword), 0), []byte(text)...))
	}
	idat := buildChunk("IDAT", []byte{0x78, 0x9c, 0x62, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01})
	raw := makePNG(
		mkText("Software", "Firefox headless"),
		mkText("Creation Time", "2026-04-19"),
		mkText("Author", "Test"), // should NOT be stripped — not in the list.
		idat,
	)

	out, stripped, err := normalizePNG(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out, []byte("Author")) {
		t.Error("Author tEXt was stripped but shouldn't have been")
	}
	if bytes.Contains(out, []byte("Software")) {
		t.Error("Software tEXt should have been stripped")
	}
	if bytes.Contains(out, []byte("Creation Time")) {
		t.Error("Creation Time tEXt should have been stripped")
	}

	chunks := stripped["screenshot"].(map[string]any)["chunks"].([]any)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 stripped chunks, got %d", len(chunks))
	}
	keywords := map[string]bool{}
	for _, c := range chunks {
		m := c.(map[string]any)
		if kw, ok := m["keyword"].(string); ok {
			keywords[kw] = true
		}
	}
	if !keywords["Software"] || !keywords["Creation Time"] {
		t.Errorf("stripped keyword set = %v, want Software + Creation Time", keywords)
	}
}

func TestNormalizePNG_IdempotentForDifferingTIME(t *testing.T) {
	// Two PNGs identical except for tIME content should normalize to the same bytes.
	idat := buildChunk("IDAT", []byte{0x78, 0x9c, 0x62, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01})
	a := makePNG(buildChunk("tIME", []byte{0x07, 0xE8, 1, 1, 1, 1, 1}), idat)
	b := makePNG(buildChunk("tIME", []byte{0x07, 0xE9, 2, 2, 2, 2, 2}), idat)
	outA, _, _ := normalizePNG(a)
	outB, _, _ := normalizePNG(b)
	if !bytes.Equal(outA, outB) {
		t.Fatalf("normalized outputs differ:\n a=%s\n b=%s", hex.EncodeToString(outA), hex.EncodeToString(outB))
	}
}

func TestNormalizePNG_RejectsBadSignature(t *testing.T) {
	_, _, err := normalizePNG([]byte("not a png"))
	if err == nil {
		t.Fatal("expected signature error; got nil")
	}
}

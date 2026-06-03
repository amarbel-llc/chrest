package capturebatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.linenisgreat.com/chrest/go/internal/alfa/firefox"
	"github.com/amarbel-llc/cutting-garden/pkgs/capture_plugin"
)

// fakeWriter installs a writer.cmd shim that content-addresses each blob
// it receives into a directory (id = "blake2b256-<sha256hex>"), so the
// test can walk the merkle tree the runner assembles without a browser or
// a real blob store. Returns the writer argv and the blob directory.
func fakeWriter(t *testing.T) (cmd []string, blobDir string) {
	t.Helper()
	dir := t.TempDir()
	blobDir = filepath.Join(dir, "blobs")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "writer.sh")
	body := "#!/bin/sh\n" +
		"tmp=$(mktemp)\n" +
		"cat > \"$tmp\"\n" +
		"size=$(wc -c < \"$tmp\")\n" +
		"hash=$(sha256sum \"$tmp\" | cut -d' ' -f1)\n" +
		"cp \"$tmp\" \"" + blobDir + "/blake2b256-$hash\"\n" +
		"printf '{\"id\":\"blake2b256-%s\",\"size\":%s}\\n' \"$hash\" \"$size\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"/bin/sh", script}, blobDir
}

func TestReceiptTreeAssembly(t *testing.T) {
	cmd, blobDir := fakeWriter(t)
	w := newSubprocessWriter(cmd)
	ctx := context.Background()

	read := func(id string) capture_plugin.Node {
		t.Helper()
		f, err := os.Open(filepath.Join(blobDir, id))
		if err != nil {
			t.Fatalf("open blob %s: %v", id, err)
		}
		defer f.Close()
		n, err := capture_plugin.ParseNode(f)
		if err != nil {
			t.Fatalf("parse node %s: %v", id, err)
		}
		return n
	}
	ref := func(n capture_plugin.Node, alias string) capture_plugin.Ref {
		t.Helper()
		r, ok := n.RefByAlias(alias)
		if !ok {
			t.Fatalf("node %q missing %q reference", n.Type, alias)
		}
		return r
	}

	// Capabilities (written once per batch) + a raw payload leaf.
	capID, err := writeCapabilities(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	payloadDigest, _, err := w.WriteBlob(ctx, strings.NewReader("%PDF-1.7 fake"))
	if err != nil {
		t.Fatal(err)
	}
	payloadRef := capture_plugin.LockedRef("payload", payloadDigest, payloadType("pdf"))

	httpResp := &firefox.HTTPResponse{
		URL:      "https://example.com/final",
		Status:   200,
		Headers:  []firefox.HTTPHeader{{Name: "Content-Type", Value: "text/html"}},
		TimingMs: 42,
	}
	r := Resolved{Name: "doc", Format: "pdf", Normalize: true}
	opts := Options{CapturerVersion: "test-1.2.3", Target: "https://example.com"}
	browser := firefox.BrowserInfo{Name: "firefox", Version: "140.0", UserAgent: "UA", Platform: "Linux x86_64"}

	receiptID, receiptSize, err := writeReceipt(
		ctx, w, r, opts, capture_plugin.GatherHost(), capID, browser, httpResp,
		map[string]any{"pdf": map[string]any{}}, payloadRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receiptID == "" || receiptSize == 0 {
		t.Fatalf("receipt id=%q size=%d", receiptID, receiptSize)
	}

	// receipt → identity, outcome, payload
	receipt := read(receiptID)
	if kind, ok := capture_plugin.KindFromReceiptType(receipt.Type); !ok || kind != "web" {
		t.Fatalf("receipt kind: type=%q", receipt.Type)
	}
	if got := ref(receipt, "payload"); got.Digest != payloadDigest {
		t.Errorf("payload ref digest = %q, want %q", got.Digest, payloadDigest)
	}
	if got := ref(receipt, "payload").TypeString; got != "chrest-capture-payload-pdf-v1" {
		t.Errorf("payload type = %q", got)
	}

	// identity → invocation, environment
	identity := read(ref(receipt, "identity").Digest)
	inv := read(ref(identity, "invocation").Digest)
	var invBody struct {
		Format    string `json:"format"`
		Normalize bool   `json:"normalize"`
		Target    string `json:"target"`
	}
	if err := json.Unmarshal(inv.Body, &invBody); err != nil {
		t.Fatal(err)
	}
	if invBody.Format != "pdf" || !invBody.Normalize || invBody.Target != "https://example.com" {
		t.Errorf("invocation body = %+v", invBody)
	}

	// environment → host, binary, plugin; binary.capabilities_id == capID
	env := read(ref(identity, "environment").Digest)
	_ = ref(env, "host")
	bin := read(ref(env, "binary").Digest)
	var binBody struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		CapabilitiesId string `json:"capabilities_id"`
	}
	if err := json.Unmarshal(bin.Body, &binBody); err != nil {
		t.Fatal(err)
	}
	if binBody.Name != "chrest" || binBody.Version != "test-1.2.3" {
		t.Errorf("binary body = %+v", binBody)
	}
	if binBody.CapabilitiesId != capID {
		t.Errorf("binary.capabilities_id = %q, want %q", binBody.CapabilitiesId, capID)
	}

	// environment.plugin is the chrest environment node
	pluginEnv := read(ref(env, "plugin").Digest)
	if pluginEnv.Type != envType {
		t.Errorf("plugin env type = %q, want %q", pluginEnv.Type, envType)
	}

	// outcome → plugin (http.*)
	outcome := read(ref(receipt, "outcome").Digest)
	op := read(ref(outcome, "plugin").Digest)
	if op.Type != outcomeType {
		t.Errorf("outcome plugin type = %q, want %q", op.Type, outcomeType)
	}
	var opBody struct {
		HTTP struct {
			Status   int              `json:"status"`
			FinalURL string           `json:"final_url"`
			Headers  []map[string]any `json:"headers"`
			TimingMs map[string]any   `json:"timing_ms"`
		} `json:"http"`
	}
	if err := json.Unmarshal(op.Body, &opBody); err != nil {
		t.Fatal(err)
	}
	if opBody.HTTP.Status != 200 {
		t.Errorf("http.status = %d", opBody.HTTP.Status)
	}
	if opBody.HTTP.FinalURL != "https://example.com/final" {
		t.Errorf("http.final_url = %q", opBody.HTTP.FinalURL)
	}
	if len(opBody.HTTP.Headers) != 1 || opBody.HTTP.Headers[0]["name"] != "content-type" {
		t.Errorf("http.headers = %+v (want lowercased content-type)", opBody.HTTP.Headers)
	}
	if opBody.HTTP.TimingMs["load"] == nil {
		t.Errorf("http.timing_ms = %+v (want object with load)", opBody.HTTP.TimingMs)
	}

	// capabilities node resolves and is the chrest capabilities type
	caps := read(capID)
	if caps.Type != capType {
		t.Errorf("capabilities type = %q, want %q", caps.Type, capType)
	}
}

func TestOutcomePluginPreviewWhenNoHTTP(t *testing.T) {
	op := outcomePlugin("https://example.com", nil)
	if op.TypeString != outcomeTypePreview {
		t.Errorf("preview type = %q, want %q", op.TypeString, outcomeTypePreview)
	}
}

func TestPayloadTypeSegments(t *testing.T) {
	cases := map[string]string{
		"pdf":               "chrest-capture-payload-pdf-v1",
		"screenshot":        "chrest-capture-payload-screenshot-v1",
		"markdown-reader":   "chrest-capture-payload-markdown_reader-v1",
		"html-monolith":     "chrest-capture-payload-html_monolith-v1",
		"markdown-selector": "chrest-capture-payload-markdown_selector-v1",
	}
	for format, want := range cases {
		if got := payloadType(format); got != want {
			t.Errorf("payloadType(%q) = %q, want %q", format, got, want)
		}
	}
}

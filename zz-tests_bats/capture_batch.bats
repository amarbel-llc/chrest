#!/usr/bin/env bats

# bats file_tags=firefox

# Integration tests for `chrest capture-batch` — the reference `web`-kind
# capturer of the Capture Plugin Protocol (cutting-garden RFC 0002) under
# the web-archive binding (RFC 0003). Every test launches headless
# Firefox; the `firefox` tag steers them into the --no-sandbox lane.
#
# chrest assembles an RFC 0002 receipt merkle tree per capture and streams
# every node blob to writer.cmd. The stub writer below content-addresses
# each blob into $BLOBDIR (id = "blake2b256-<sha256hex>") so a test can
# resolve the receipt blob and inspect the tree it roots.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"

  cat >"$BATS_TEST_TMPDIR/test.html" <<'EOF'
<!doctype html>
<html><head><title>Test</title></head>
<body><h1>Hello from chrest</h1></body>
</html>
EOF
  FIXTURE="file://$BATS_TEST_TMPDIR/test.html"

  # Content-addressing stub writer: store stdin under its hash and emit
  # the {"id","size"} line. Stands in for `cutting-garden __write-blob`
  # without requiring a blob store in the test closure.
  BLOBDIR="$BATS_TEST_TMPDIR/blobs"
  mkdir -p "$BLOBDIR"
  cat >"$BATS_TEST_TMPDIR/stub-writer.sh" <<EOF
#!/usr/bin/env bash
tmp=\$(mktemp)
cat > "\$tmp"
size=\$(wc -c < "\$tmp")
hash=\$(sha256sum "\$tmp" | cut -d' ' -f1)
cp "\$tmp" "$BLOBDIR/blake2b256-\$hash"
printf '{"id":"blake2b256-%s","size":%s}\n' "\$hash" "\$size"
EOF
  chmod +x "$BATS_TEST_TMPDIR/stub-writer.sh"
  STUB_WRITER="$BATS_TEST_TMPDIR/stub-writer.sh"

  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi
}

# blob_for echoes the path of the stored blob for a given markl id.
blob_for() { echo "$BLOBDIR/$1"; }

function capture_batch_rejects_bad_schema { # @test
  input='{"schema":"wrong/v1","writer":{"cmd":["/bin/true"]},"target":"about:blank","captures":[]}'
  run bash -c "echo '$input' | timeout 10 '$CHREST_BIN' capture-batch"
  [ "$status" -ne 0 ]
  echo "$output" | grep -qi "schema"
}

function capture_batch_pdf_emits_web_receipt { # @test
  input=$(
    cat <<JSON
{
  "schema": "capture-plugin/v1",
  "writer": {"cmd": ["$STUB_WRITER"]},
  "target": "$FIXTURE",
  "defaults": {"normalize": true, "plugin": {"browser": "firefox"}},
  "captures": [{"name": "doc", "format": "pdf"}]
}
JSON
  )
  result=$(echo "$input" | timeout 60 "$CHREST_BIN" capture-batch)

  # Output envelope: capture-plugin/v1, chrest, one receipt, no error.
  echo "$result" | jq -e '.schema == "capture-plugin/v1"'
  echo "$result" | jq -e '.plugin.name == "chrest"'
  echo "$result" | jq -e '.captures[0].error == null'
  echo "$result" | jq -e '.captures[0].receipt.id | startswith("blake2b256-")'
  echo "$result" | jq -e '.captures[0].receipt.size > 0'

  # The receipt blob resolves and is a web receipt referencing identity,
  # outcome, and a pdf payload.
  receipt_id=$(echo "$result" | jq -r '.captures[0].receipt.id')
  receipt="$(blob_for "$receipt_id")"
  [ -f "$receipt" ]
  grep -q '^! cutting_garden-capture-receipt-web-v1$' "$receipt"
  grep -q '^- identity <' "$receipt"
  grep -q '^- outcome <' "$receipt"
  grep -q '^- payload < .* !chrest-capture-payload-pdf-v1' "$receipt"
}

function capture_batch_normalize_false_markdown_emits_receipt { # @test
  # markdown-* has no normalizer (RFC 0003 deferred); normalize=false must
  # still produce a receipt with as-captured payload bytes.
  input=$(
    cat <<JSON
{
  "schema": "capture-plugin/v1",
  "writer": {"cmd": ["$STUB_WRITER"]},
  "target": "$FIXTURE",
  "defaults": {"normalize": false, "plugin": {"browser": "firefox"}},
  "captures": [{"name": "md", "format": "markdown-full"}]
}
JSON
  )
  result=$(echo "$input" | timeout 60 "$CHREST_BIN" capture-batch)
  echo "$result" | jq -e '.captures[0].error == null'
  receipt_id=$(echo "$result" | jq -r '.captures[0].receipt.id')
  grep -q '^- payload < .* !chrest-capture-payload-markdown_full-v1' "$(blob_for "$receipt_id")"
}

function capture_batch_normalize_true_markdown_returns_not_implemented { # @test
  input=$(
    cat <<JSON
{
  "schema": "capture-plugin/v1",
  "writer": {"cmd": ["$STUB_WRITER"]},
  "target": "$FIXTURE",
  "defaults": {"normalize": true, "plugin": {"browser": "firefox"}},
  "captures": [{"name": "md", "format": "markdown-reader"}]
}
JSON
  )
  result=$(echo "$input" | timeout 60 "$CHREST_BIN" capture-batch)
  echo "$result" | jq -e '.captures[0].receipt == null'
  echo "$result" | jq -e '.captures[0].error.kind == "not-implemented"'
}

function capture_batch_unknown_format_returns_bad_format { # @test
  input=$(
    cat <<JSON
{
  "schema": "capture-plugin/v1",
  "writer": {"cmd": ["$STUB_WRITER"]},
  "target": "$FIXTURE",
  "captures": [{"name": "x", "format": "nonsense"}]
}
JSON
  )
  result=$(echo "$input" | timeout 60 "$CHREST_BIN" capture-batch)
  echo "$result" | jq -e '.captures[0].error.kind == "bad-format"'
}

function capture_batch_text_outcome_has_http_status { # @test
  # Firefox/BiDi populates http.* via network.responseCompleted, so the
  # outcome's plugin node is the non-preview type carrying http.status.
  input=$(
    cat <<JSON
{
  "schema": "capture-plugin/v1",
  "writer": {"cmd": ["$STUB_WRITER"]},
  "target": "$FIXTURE",
  "defaults": {"normalize": true, "plugin": {"browser": "firefox"}},
  "captures": [{"name": "text", "format": "text"}]
}
JSON
  )
  result=$(echo "$input" | timeout 60 "$CHREST_BIN" capture-batch)
  receipt_id=$(echo "$result" | jq -r '.captures[0].receipt.id')
  receipt="$(blob_for "$receipt_id")"

  # outcome → plugin reference, and the plugin node is the non-preview
  # chrest outcome type with an http object.
  outcome_id=$(grep -oE '^- outcome < @[^ ]+' "$receipt" | sed 's/^- outcome < @//')
  outcome="$(blob_for "$outcome_id")"
  grep -q '^- plugin < .* !jcs-chrest-capture-outcome-v1$' "$outcome"
  plugin_id=$(grep -oE '^- plugin < @[^ ]+' "$outcome" | sed 's/^- plugin < @//')
  grep -q '"http"' "$(blob_for "$plugin_id")"
}

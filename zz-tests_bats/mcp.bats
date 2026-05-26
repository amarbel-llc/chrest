#!/usr/bin/env bats

# Exploratory tests for chrest MCP server behavior under various socket
# conditions.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  setup_test_home
}

teardown() {
  teardown_test_home
}

function initialize_responds_with_no_sockets { # @test
  result=$(printf '%s\n' "$INIT_MSG" | run_mcp)
  echo "$result" | jq -e 'select(.id == 1) | .result.serverInfo.name == "chrest"'
}

function tools_list_returns_tools_with_no_sockets { # @test
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | run_mcp)
  echo "$result" | grep '"id":2' | jq -e '.result.tools | length > 0'
}

function resources_list_returns_resources_with_no_sockets { # @test
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"resources/list","params":{}}' | run_mcp)
  echo "$result" | grep '"id":2' | jq -e '.result.resources | length > 0'
}

function tools_call_list_windows_returns_quickly_with_no_sockets { # @test
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list-windows","arguments":{}}}' | run_mcp)
  echo "$result" | grep '"id":2' | jq -e '.result or .error'
}

function web_fetch_appears_in_tools_list { # @test
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | run_mcp)
  echo "$result" | grep '"id":2' | jq -e '
    [.result.tools[] | select(.name == "web-fetch")] | length == 1
  '
}

# V1 negotiation exposes annotations (readOnlyHint). The shared INIT_MSG
# uses protocol 2025-03-26 which falls back to V0 where annotations are
# dropped, so this test uses the V1 version string explicitly.
function web_fetch_annotations_visible_under_v1 { # @test
  v1_init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}'
  result=$(printf '%s\n' "$v1_init" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' | run_mcp)
  tool=$(echo "$result" | grep '"id":2' | jq '[.result.tools[] | select(.name == "web-fetch")] | first')
  echo "DEBUG V1 tool: $tool" >&2
  echo "$tool" | jq -e '.annotations.readOnlyHint == true'
}

function web_fetch_rejects_missing_url { # @test
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"web-fetch","arguments":{}}}' | run_mcp)
  echo "$result" | grep '"id":2' | jq -e '.result.isError == true'
}

# bats test_tags=firefox
function web_fetch_defaults_to_markdown_with_resource_links { # @test
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/fetch.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Fetch Test</title></head>
<body><h1>Hello web-fetch</h1><p>Body text.</p></body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/fetch.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\"}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  # 4 blocks: TOC text + 1 embedded resource (markdown) + 2 resource_links (text, html)
  echo "$resp" | jq -e '.result.content | length == 4'
  # First block is the TOC (text)
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  # The embedded resource is markdown (fragment #markdown in URI)
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.uri | test("#markdown$")'
  # The other two are resource_links
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource_link")] | length == 2'
}

# bats test_tags=firefox
function web_fetch_format_text_returns_text_inline { # @test
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/fetch.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Fetch Test</title></head>
<body><h1>Hello web-fetch</h1><p>Body text.</p></body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/fetch.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\",\"format\":\"text\"}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  # 4 blocks: TOC text + 1 embedded resource (text) + 2 resource_links (markdown, html)
  echo "$resp" | jq -e '.result.content | length == 4'
  # First block is the TOC (text)
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  # The embedded resource is text (fragment #text in URI)
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.uri | test("#text$")'
  # Markdown and html are resource_links
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource_link")] | length == 2'
}

# bats test_tags=firefox
function web_fetch_toc_lists_anchors { # @test
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/anchors.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Anchor Fixture</title></head>
<body>
  <h1 id="top">Top Heading</h1>
  <h2 id="intro">Introduction</h2>
  <p>Intro body text.</p>
  <h2 id="details">Details</h2>
  <p>Details body text.</p>
</body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/anchors.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\"}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  echo "$resp" | jq -e '.result.content[0].text | contains("#top")'
  echo "$resp" | jq -e '.result.content[0].text | contains("#intro")'
  echo "$resp" | jq -e '.result.content[0].text | contains("#details")'
}

# bats test_tags=firefox
function web_fetch_selector_hit_trims_body { # @test
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/selector-hit.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Anchor Fixture</title></head>
<body>
  <h1 id="top">Top Heading</h1>
  <h2 id="intro">Introduction</h2>
  <p>Intro body text.</p>
  <h2 id="details">Details</h2>
  <p>Details body text.</p>
</body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/selector-hit.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\",\"selector\":\"#intro\"}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  # 5 blocks on selector hit: the embedded selector match leads (so the
  # caller sees what they asked for first), followed by a resource_link
  # to the TOC (kept out-of-band so it doesn't dwarf the section), then
  # the three full-page format links.
  echo "$resp" | jq -e '.result.content | length == 5'
  echo "$resp" | jq -e '.result.content[0].type == "resource"'
  echo "$resp" | jq -e '.result.content[0].resource.uri | test("#markdown-selector$")'
  echo "$resp" | jq -e '.result.content[0].resource.text | contains("Introduction")'
  # Section-expansion: the paragraph under #intro is a sibling of the h2 and
  # precedes the next h2, so it MUST be included in the trimmed body.
  echo "$resp" | jq -e '.result.content[0].resource.text | contains("Intro body text.")'
  # But the next h2 (#details) and its body must stop the walk.
  echo "$resp" | jq -e '.result.content[0].resource.text | contains("Details") | not'
  echo "$resp" | jq -e '.result.content[0].resource.text | contains("Details body text.") | not'
  # TOC moves to a resource_link to keep the response small.
  echo "$resp" | jq -e '.result.content[1].type == "resource_link"'
  echo "$resp" | jq -e '.result.content[1].uri | test("#toc$")'
}

# bats test_tags=firefox
function web_fetch_selector_miss_returns_diagnostic { # @test
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/selector-miss.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Anchor Fixture</title></head>
<body>
  <h1 id="top">Top Heading</h1>
  <h2 id="intro">Introduction</h2>
  <p>Intro body text.</p>
  <h2 id="details">Details</h2>
  <p>Details body text.</p>
</body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/selector-miss.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\",\"selector\":\"#does-not-exist\"}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  # 5 blocks: TOC text + diagnostic text + 3 resource_links (no embedded resource)
  echo "$resp" | jq -e '.result.content | length == 5'
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  echo "$resp" | jq -e '.result.content[1].type == "text"'
  echo "$resp" | jq -e '.result.content[1].text | contains("matched no element")'
  echo "$resp" | jq -e '.result.content[1].text | contains("#does-not-exist")'
  # Miss path does not embed a resource block
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource")] | length == 0'
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource_link")] | length == 3'
}

# bats test_tags=firefox
function web_fetch_selector_rejected_with_non_markdown_format { # @test
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/selector-reject.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Anchor Fixture</title></head>
<body>
  <h1 id="top">Top Heading</h1>
  <h2 id="intro">Introduction</h2>
</body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/selector-reject.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\",\"format\":\"text\",\"selector\":\"#intro\"}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError == true'
  echo "$resp" | jq -e '.result.content[0].text | contains("selector is only supported with format=markdown")'
}

# bats test_tags=firefox
function web_fetch_refresh_param_accepted { # @test
  # full cache-vs-refresh behavior requires a single-session driver; this just
  # validates the refresh param is accepted and produces a valid envelope.
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi

  cat >"$BATS_TEST_TMPDIR/refresh.html" <<'FIXTURE'
<!doctype html>
<html><head><title>Refresh Fixture</title></head>
<body><h1 id="top">Hello refresh</h1><p>Body text.</p></body>
</html>
FIXTURE
  url="file://$BATS_TEST_TMPDIR/refresh.html"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"web-fetch\",\"arguments\":{\"url\":\"$url\",\"refresh\":true}}}" |
    timeout 20 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.content | length == 4'
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.uri | test("#markdown$")'
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource_link")] | length == 2'
}

function tools_call_list_windows_handles_stale_socket { # @test
  # Override XDG_STATE_HOME to a short /tmp path to avoid AF_UNIX 108-char limit
  short_state="$(mktemp -d /tmp/chrest-bats.XXXXXX)"
  export XDG_STATE_HOME="$short_state"
  mkdir -p "$short_state/chrest"
  sock_path="$short_state/chrest/firefox-stale.sock"

  # Create a socket file that nothing is listening on
  python3 -c "
import socket
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.bind('$sock_path')
s.close()
"

  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list-windows","arguments":{}}}' | run_mcp)
  rm -rf "$short_state"
  echo "$result" | grep '"id":2' | jq -e '.result or .error'
}

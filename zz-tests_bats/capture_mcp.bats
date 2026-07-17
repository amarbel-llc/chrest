#!/usr/bin/env bats

# bats file_tags=firefox

# End-to-end coverage for the capture tool's content-type-aware dispatcher
# (markdown/text/html formats). Pins the contract of the
# CHREST_WEB_FETCH_DISPATCH=bidi-intercept path (default) against real
# upstream URLs:
#   - HTML (example.com)         → existing TOC + body (regression).
#   - Raw .md (anthropic SDK)    → body + TOC built from raw markdown.
#   - Raw .toml (anthropic SDK)  → text body, no schema crash.
#   - 404 URL                    → structured HTTP 404 error.
#   - Binary tarball             → structured "binary content-type"
#                                  refusal, no MCP schema crash.
#
# These tests need real Firefox + network reachability. They skip
# cleanly when Firefox is missing on PATH.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/common.bash"
  setup_test_home
}

teardown() {
  teardown_test_home
}

# Common skip gate; mirrors the pattern used by the capture_* tests
# in mcp.bats.
require_firefox() {
  firefox="$(command -v firefox || command -v firefox-esr || true)"
  if [ -z "$firefox" ]; then
    skip "no Firefox found on PATH"
  fi
  if ! timeout 5 "$firefox" --headless --version >/dev/null 2>&1; then
    skip "headless Firefox not functional"
  fi
}

function capture_html_url_regression { # @test
  require_firefox

  # example.com is too minimal for readability to extract a non-empty
  # article, so we ask for format=text (document.body.innerText). The
  # response still travels the HTML-class branch of fetchViaDispatch
  # since the response is text/html.
  url="https://example.com"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  # Default text layout: TOC + embedded text resource + 2 resource_links.
  echo "$resp" | jq -e '.result.content | length == 4'
  echo "$resp" | jq -e '.result.content[0].type == "text"'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.uri | test("#text$")'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("Example Domain")'
}

function capture_raw_md_url_returns_body_and_toc { # @test
  require_firefox

  url="https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/78c73600b714fcb036893768df8ee122f33d4cb3/README.md"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"markdown"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  # Embedded markdown resource carries the raw .md body verbatim.
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("# Claude SDK for Python")'
  # New capability: TOC populated with real heading anchors built from
  # the raw markdown text (Firefox would otherwise wrap the body in
  # <pre> with no <h*> elements and yield an empty TOC).
  echo "$resp" | jq -e '.result.content[0].text | (contains("#installation") or contains("#getting-started"))'
}

function capture_raw_toml_url_returns_text_body { # @test
  require_firefox

  url="https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/78c73600b714fcb036893768df8ee122f33d4cb3/pyproject.toml"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  # The text slot contains the raw toml.
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.uri | test("#text$")'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("[project]")'
  # No headings in toml; TOC block should be present (text type) but not crash.
  echo "$resp" | jq -e '.result.content[0].type == "text"'
}

function capture_404_url_returns_structured_http_error { # @test
  require_firefox

  url="https://raw.githubusercontent.com/anthropics/anthropic-sdk-python/78c73600b714fcb036893768df8ee122f33d4cb3/THIS-DOES-NOT-EXIST"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  # Structured error envelope, not a synthesized "page" body.
  echo "$resp" | jq -e '.result.isError == true'
  echo "$resp" | jq -e '.result.content[0].text | contains("HTTP 404")'
  # MCP schema validation must NOT have fired — no resource_link / resource
  # blocks should be present in an error envelope.
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource_link")] | length == 0'
  echo "$resp" | jq -e '[.result.content[] | select(.type == "resource")] | length == 0'
  echo "$resp" | jq -e '.result.content[0].text | contains("invalid_union") | not'
}

function capture_binary_url_returns_structured_refusal { # @test
  require_firefox

  url="https://github.com/anthropics/anthropic-sdk-python/archive/refs/tags/v0.97.0.tar.gz"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  resp=$(echo "$result" | grep '"id":2')
  # Structured error, not a parsed document.
  echo "$resp" | jq -e '.result.isError == true'
  # Refusal mentions the binary content-type so the caller knows why.
  echo "$resp" | jq -e '.result.content[0].text | contains("binary content-type")'
  # Crucially: MCP schema validation did not fire on a malformed
  # content block. The "invalid_union" string is what shows up in the
  # client when SDK schema parsing rejects the result.
  echo "$resp" | jq -e '.result.content[0].text | contains("invalid_union") | not'
}

function capture_subresource_heavy_page_completes { # @test
  require_firefox

  # Regression test for the BiDi navigate timeout on subresource-heavy
  # pages, observed against docs.github.com (every fetch hits a
  # deterministic 30s "bidi: timed out waiting for response to
  # browsingContext.navigate" error).
  #
  # Hypothesis: after commit 47f9578 dropped urlPatterns scoping on the
  # BiDi intercept, network.addIntercept now matches every response in
  # the browsing context. Subresource responses (CSS/JS/img) are paused
  # at responseStarted but the consumer-side filter in
  # firefox/intercept.go (peek.Navigation != "") drops them, so the
  # dispatcher never calls ContinueResponse on them. The page never
  # reaches the `load` event and `wait: "complete"` in
  # firefox/session.go blocks indefinitely.
  #
  # Reproduced locally with a throwaway Python HTTP server serving an
  # HTML page with a CSS link, a script tag, and an <img>. The server
  # self-terminates via `timeout 60`, so no EXIT trap is needed —
  # overriding bats' EXIT trap was observed to swallow the TAP
  # not-ok line for the test.

  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')

  cat >"$BATS_TEST_TMPDIR/index.html" <<'EOF'
<!doctype html>
<html>
  <head>
    <title>subresource-heavy</title>
    <link rel="stylesheet" href="style.css">
    <script src="script.js"></script>
  </head>
  <body>
    <h1>subresource page</h1>
    <p>UNIQUE_BODY_MARKER</p>
    <img src="image.png" alt="">
  </body>
</html>
EOF
  printf 'body { color: #333; }\n' >"$BATS_TEST_TMPDIR/style.css"
  printf 'console.log("hi");\n' >"$BATS_TEST_TMPDIR/script.js"
  # Minimal 1x1 transparent PNG.
  printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82' >"$BATS_TEST_TMPDIR/image.png"

  (cd "$BATS_TEST_TMPDIR" && timeout 60 python3 -m http.server "$port" </dev/null >/dev/null 2>&1) &
  srv_pid=$!
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/index.html" >/dev/null; then break; fi
    sleep 0.1
  done

  url="http://127.0.0.1:$port/index.html"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  kill "$srv_pid" 2>/dev/null || true
  wait "$srv_pid" 2>/dev/null || true

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("UNIQUE_BODY_MARKER")'
}

function capture_many_subresources_overflow_buffer { # @test
  require_firefox

  # Regression test for chrest#66: when many subresources arrive in a
  # burst, the producer goroutine in firefox/intercept.go used to drop
  # events on the `default:` branch of its select without releasing
  # the corresponding paused BiDi request. Each dropped event left a
  # request paused at the BiDi server; if any of them was on the
  # critical path to the `load` event, Navigate deadlocked at the 30s
  # BiDi RPC timeout.
  #
  # We force the overflow by loading 24 <img> subresources from the
  # same origin. The intercept's consumer-facing channel has buffer=4
  # and the dispatcher is serialized on ContinueResponse RPCs, so a
  # 6-concurrent burst (Firefox's default per-origin limit) overflows
  # the buffer and drops on the producer side without the fix.

  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')

  {
    printf '<!doctype html><html><head><title>overflow</title></head><body>\n'
    printf '<p>OVERFLOW_BODY_MARKER</p>\n'
    for i in $(seq 1 24); do
      printf '<img src="image-%d.png" alt="">\n' "$i"
    done
    printf '</body></html>\n'
  } >"$BATS_TEST_TMPDIR/index.html"

  # Same minimal 1x1 transparent PNG, served under 24 distinct paths
  # so each is a distinct subresource request.
  for i in $(seq 1 24); do
    printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82' >"$BATS_TEST_TMPDIR/image-$i.png"
  done

  (cd "$BATS_TEST_TMPDIR" && timeout 60 python3 -m http.server "$port" </dev/null >/dev/null 2>&1) &
  srv_pid=$!
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/index.html" >/dev/null; then break; fi
    sleep 0.1
  done

  url="http://127.0.0.1:$port/index.html"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  kill "$srv_pid" 2>/dev/null || true
  wait "$srv_pid" 2>/dev/null || true

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("OVERFLOW_BODY_MARKER")'
}

# Regression test for the graceful-degradation design
# (docs/plans/2026-07-16-graceful-navigate-timeout-design.md).
# The fixture's page embeds an <img> whose request hangs forever (no
# RST, no response, no clean failure) — an <img> is a render-blocking
# subresource that the `load` event genuinely waits on, unlike a
# fire-and-forget fetch() from a <script> (verified empirically: an
# initial draft of this fixture using fetch() did NOT reproduce a hang
# under wait-strategy=strict, since fetch() doesn't hold `load` open).
# This mirrors the confirmed web.dev shape: a subresource-style request
# that never completes a response cycle at all. wait-strategy=graceful
# must still return real content once idle-timeout-ms of silence
# elapses after the top-level document classifies; wait-strategy=strict
# must still hard-timeout waiting for `load`.
function capture_graceful_wait_strategy_survives_hanging_request { # @test
  require_firefox

  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')

  cat >"$BATS_TEST_TMPDIR/index.html" <<'EOF'
<!doctype html>
<html>
  <head><title>hanging-beacon</title></head>
  <body>
    <p>GRACEFUL_BODY_MARKER</p>
    <!--
      10.255.255.1 is a non-routable RFC 5737-adjacent address; a
      request to it hangs (no RST, no response) rather than failing
      cleanly. An <img> is render-blocking for the `load` event, so
      this actually reproduces the "page never settles" condition
      (a fire-and-forget fetch() does not).
    -->
    <img src="http://10.255.255.1/never-responds" alt="">
  </body>
</html>
EOF

  (cd "$BATS_TEST_TMPDIR" && timeout 60 python3 -m http.server "$port" </dev/null >/dev/null 2>&1) &
  srv_pid=$!
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/index.html" >/dev/null; then break; fi
    sleep 0.1
  done

  url="http://127.0.0.1:$port/index.html"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text","wait-strategy":"graceful","idle-timeout-ms":3000}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 30 "$CHREST_BIN" mcp)

  kill "$srv_pid" 2>/dev/null || true
  wait "$srv_pid" 2>/dev/null || true

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("GRACEFUL_BODY_MARKER")'
}

# Strict counterpart to capture_graceful_wait_strategy_survives_hanging_request:
# proves wait-strategy=strict retains today's exact hard-timeout contract
# against the SAME hanging-<img> shape, so "graceful" becoming the default
# didn't silently change strict's behavior too. Wall-clock note: this test
# takes ~30s+ (the real BiDi navigate timeout under strict mode).
function capture_strict_wait_strategy_times_out_on_hanging_request { # @test
  require_firefox

  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')

  cat >"$BATS_TEST_TMPDIR/index.html" <<'EOF'
<!doctype html>
<html>
  <head><title>hanging-beacon-strict</title></head>
  <body>
    <p>STRICT_BODY_MARKER</p>
    <img src="http://10.255.255.1/never-responds" alt="">
  </body>
</html>
EOF

  (cd "$BATS_TEST_TMPDIR" && timeout 60 python3 -m http.server "$port" </dev/null >/dev/null 2>&1) &
  srv_pid=$!
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/index.html" >/dev/null; then break; fi
    sleep 0.1
  done

  url="http://127.0.0.1:$port/index.html"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text","wait-strategy":"strict"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 45 "$CHREST_BIN" mcp)

  kill "$srv_pid" 2>/dev/null || true
  wait "$srv_pid" 2>/dev/null || true

  resp=$(echo "$result" | grep '"id":2')
  # Strict mode must still hard-fail — this is the regression guard
  # that "graceful" becoming the default didn't silently change
  # strict's contract too.
  echo "$resp" | jq -e '.result.isError == true'
}

# Regression guard against false-triggering the idle-timeout diagnostic
# on an ordinary page that settles normally. Uses wait-strategy at its
# default (omitted -> "graceful" per Task 3). capture_mcp.bats has no
# shared $FIXTURE from setup() (unlike capture_firefox.bats /
# capture_batch.bats), so this inlines a minimal static fixture the
# same way the other local-server tests in this file do.
function capture_graceful_wait_strategy_no_diagnostic_on_normal_page { # @test
  require_firefox

  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')

  cat >"$BATS_TEST_TMPDIR/index.html" <<'EOF'
<!doctype html>
<html>
  <head><title>normal-page</title></head>
  <body>
    <p>NORMAL_BODY_MARKER</p>
  </body>
</html>
EOF

  (cd "$BATS_TEST_TMPDIR" && timeout 60 python3 -m http.server "$port" </dev/null >/dev/null 2>&1) &
  srv_pid=$!
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/index.html" >/dev/null; then break; fi
    sleep 0.1
  done

  url="http://127.0.0.1:$port/index.html"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"text"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 30 "$CHREST_BIN" mcp)

  kill "$srv_pid" 2>/dev/null || true
  wait "$srv_pid" 2>/dev/null || true

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  echo "$resp" | jq -e '.result.content[] | select(.type == "resource") | .resource.text | contains("NORMAL_BODY_MARKER")'
  if echo "$resp" | jq -e '.result.content[] | select(.type == "text") | .text | contains("did not settle")' >/dev/null 2>&1; then
    echo "false-triggered idle-timeout diagnostic on a normal page:" >&2
    echo "$resp" >&2
    return 1
  fi
}

function capture_empty_extraction_returns_diagnostic { # @test
  require_firefox

  # Regression test for chrest#74: when readability/text extraction
  # returns nothing (e.g. on a meta-refresh redirect page with no
  # <body> content), the response substitutes a text diagnostic at
  # content[1] explaining why and pointing at the raw-HTML
  # resource_link. Earlier this case returned an embedded resource
  # block with empty `text`; chrest#65 made that MCP-valid but it's
  # still bad UX — the caller sees nothing and can't tell why.
  #
  # The marshal contract that closed chrest#65 is now exercised by
  # a Go unit test in golf/protocol/content_v1_test.go; this BATS
  # test asserts the user-facing behavior of the capture tool.

  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')

  cat >"$BATS_TEST_TMPDIR/index.html" <<'EOF'
<!doctype html>
<html>
  <head>
    <title>empty</title>
    <meta http-equiv="refresh" content="999; url=elsewhere/">
  </head>
  <body></body>
</html>
EOF

  (cd "$BATS_TEST_TMPDIR" && timeout 60 python3 -m http.server "$port" </dev/null >/dev/null 2>&1) &
  srv_pid=$!
  for _ in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$port/index.html" >/dev/null; then break; fi
    sleep 0.1
  done

  url="http://127.0.0.1:$port/index.html"
  call=$(jq -nc --arg url "$url" '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"capture",arguments:{url:$url,format:"markdown"}}}')
  result=$(printf '%s\n' "$INIT_MSG" "$INITIALIZED_MSG" "$call" |
    timeout 60 "$CHREST_BIN" mcp)

  kill "$srv_pid" 2>/dev/null || true
  wait "$srv_pid" 2>/dev/null || true

  resp=$(echo "$result" | grep '"id":2')
  echo "$resp" | jq -e '.result.isError != true'
  # content[1] is now a text diagnostic, not an empty resource block.
  echo "$resp" | jq -e '.result.content[1].type == "text"'
  echo "$resp" | jq -e '.result.content[1].text | contains("No markdown content extracted")'
  echo "$resp" | jq -e '.result.content[1].text | contains("read-resource")'
  # The raw-HTML resource_link is promoted to content[2] (adjacent to
  # the diagnostic) so the caller can see how to recover the body.
  echo "$resp" | jq -e '.result.content[2].type == "resource_link"'
  echo "$resp" | jq -e '.result.content[2].uri | endswith("#html")'
}


default: build check-nix dagnabit-check test

# `nix build` runs the chrest derivation, which builds chrest +
# chrest-server + chrest-jcs and runs the Go unit suite in checkPhase.
# A clean `just build` therefore proves both compile and unit tests.
build:
  nix build --no-link

# Devshell rapid-iteration: builds all three binaries into
# go/build/release/ so the explore-* recipes (which reference
# go/build/release/chrest by path) keep working. Skips checkPhase and
# the firefox/monolith wrap. Mirrors madder's `build-go`.
[group("dev")]
build-go:
  cd go && go build -o build/release/ ./cmd/...

build-extension:
  just extension/build

# Evaluate flake outputs for every supported system. Catches malformed
# fixed-output hashes on non-host platforms before they surface in
# flakehub-push's inspect wrapper (see chrest#50).
#
# Pins cross-system devShell + package .drvs as GC roots first. The
# all-systems-eval check deep-seqs through their drvPaths, which
# transitively imports a bun2nix FOD source (deps.nix) from inside
# amarbel-llc/nixpkgs's build-support overlay. The FOD's output is
# substituted from cache.flakehub.com, but its .drv is not — nix
# substitution doesn't carry .drv files. When eval tries to realise
# the string context of the imported path, it needs the .drv to
# exist locally; if it doesn't, eval fails with "path '...source.drv'
# is not valid".
#
# `nix-instantiate --add-root <symlink> --indirect <expr>` writes the
# .drv to the store, creates a symlink in .nix-gcroots/, and
# registers the symlink in /nix/var/nix/gcroots/auto/ so the .drv
# survives `nix-collect-garbage`. Re-running this recipe overwrites
# the symlinks (and re-registers), picking up any new .drv hash
# after a flake.lock change.
check-nix:
  #!/usr/bin/env bash
  set -euo pipefail
  mkdir -p .nix-gcroots
  flake_root=$(pwd)
  for sys in x86_64-linux aarch64-linux x86_64-darwin aarch64-darwin; do
    for kind in devShells packages; do
      nix-instantiate \
        --add-root ".nix-gcroots/$sys-$kind-default" --indirect \
        --expr "(builtins.getFlake \"$flake_root\").$kind.$sys.default" \
        >/dev/null
    done
  done
  nix flake check --no-build

# Reinstalls the native-messaging-host manifest pointing at the
# nix-built (firefox-wrapped) chrest, then reloads the running
# extension. Uses the nix output rather than go/build/release/ so the
# manifest points at a binary with firefox + monolith on PATH.
reload:
  #!/usr/bin/env bash
  set -euo pipefail
  out=$(nix build --no-link --print-out-paths)
  "$out/bin/chrest" install jbcogiaaaaikinoljmplilmcnicpfoek
  chrest reload-extension

# Bats + MCP-inspector integration on top of the nix-built chrest.
# Unit tests already ran inside the nix sandbox during `build`'s
# checkPhase, so this layer only adds integration coverage. Matches
# madder, where `nix build` covers unit tests and `just test` runs
# the integration lanes.
test: test-mcp test-mcp-bats

# Devshell rapid-iteration. Mirrors madder's `test-go *flags`.
[group("dev")]
test-go *flags:
  cd go && go test {{flags}} ./...

# Regenerate gomod2nix.toml from go.mod / go.sum. Run after
# `just go/add-dep <pkg>` (or any manual go.mod edit); stage the
# updated gomod2nix.toml alongside go.mod and go.sum. `nix build`
# will fail loudly if the manifest is out of sync — that's the
# drift signal now, not a justfile drift-guard.
[group("maint")]
gomod2nix:
  cd go && gomod2nix

dagnabit := "nix run github:amarbel-llc/purse-first#dagnabit --"

# Regenerate pkgs/<leaf>/main.go facades from `//go:generate dagnabit
# export` directives in go/internal/. Stage the regenerated pkgs/
# tree alongside any source changes; external consumers (e.g.
# dodder) import via pkgs/<leaf>, so the facade is the API contract.
[group("maint")]
dagnabit-export:
  cd go && {{dagnabit}} export

# CI drift gate: pkgs/ must match what `dagnabit export` would emit
# right now. Re-runs the exporter into a sibling dir (dagnabit's
# -output-dir is always module-root-relative, so we can't use a
# /tmp path) and diffs against the committed pkgs/. Wired into the
# default lane so merges catch facades that fell behind their
# internal package's exported surface.
[group("maint")]
dagnabit-check:
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  tmp_rel="pkgs.dagnabit-check.tmp"
  trap 'rm -rf "$tmp_rel"' EXIT
  {{dagnabit}} export -output-dir "$tmp_rel"
  diff -ru pkgs "$tmp_rel"

# Re-tier packages under go/internal/<level>/<leaf> by current
# dependency height. Dry-runs by default; pass `apply` to commit
# moves. Re-run when a dependency change bumps a leaf into a
# different NATO tier (rare).
[group("maint")]
dagnabit-reposition apply="":
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  if [ "{{apply}}" = "apply" ]; then
    {{dagnabit}} internal
  else
    {{dagnabit}} -n -v internal
  fi

mcp-inspect := "npx @modelcontextprotocol/inspector --cli"

test-mcp:
  #!/usr/bin/env bash
  set -euo pipefail
  out=$(nix build --no-link --print-out-paths)
  mcp_bin="$out/bin/chrest mcp"
  tools=$({{mcp-inspect}} --method tools/list $mcp_bin)
  resources=$({{mcp-inspect}} --method resources/list $mcp_bin)
  templates=$({{mcp-inspect}} --method resources/templates/list $mcp_bin)
  # Verify listings return valid JSON
  echo "$tools" | jq -e '.tools | length > 0'
  echo "$resources" | jq -e '.resources | length > 0'
  echo "$templates" | jq -e '.resourceTemplates | length > 0'
  # Verify readOnlyHint annotations
  for tool in browser-info list-windows get-window list-tabs get-tab list-extensions items-get state-get read-resource web-fetch; do
    echo "$tools" | jq -e --arg t "$tool" '.tools[] | select(.name == $t) | .annotations.readOnlyHint == true' \
      || { echo "FAIL: $tool missing readOnlyHint"; exit 1; }
  done
  # Verify destructiveHint annotations
  for tool in close-window close-tab state-restore items-put; do
    echo "$tools" | jq -e --arg t "$tool" '.tools[] | select(.name == $t) | .annotations.destructiveHint == true' \
      || { echo "FAIL: $tool missing destructiveHint"; exit 1; }
  done
  echo "All MCP validations passed"

test-mcp-bats:
  #!/usr/bin/env bash
  # Two bats lanes against the same nix-built chrest:
  #
  # 1. Fence lane: --filter-tags '!firefox'. Runs the pure-MCP tests
  #    under fence (network denied, /tmp-only writes, credential dirs
  #    blocked) for free baseline isolation.
  # 2. Firefox lane: --no-sandbox --filter-tags 'firefox'. Runs the
  #    capture / web-fetch tests that launch headless Firefox.
  #    Firefox's content-process sandbox wants to write its own
  #    /proc/self/uid_map, which fails inside fence's userns
  #    (EACCES → ECONNRESET → per-test timeout). --no-sandbox
  #    bypasses fence so the inner Firefox sandbox can do its work.
  #    The chrest tests do their own per-test profile-dir isolation.
  #
  # Each lane runs under a wall-clock timeout (bats has been observed
  # to hang on post-test shutdown after several Firefox captures in
  # bwrap --unshare-pid; root cause open). We validate the TAP output
  # ourselves: if the plan line `1..N` is present and every line is
  # `ok`, the suite succeeded regardless of bats's own exit code.
  #
  # Uses amarbel-llc/bats's fork via the devshell. The fork dropped
  # `--bin-dir`; tests find the chrest binary via CHREST_BIN env var
  # (see zz-tests_bats/lib/common.bash).
  set -e
  out_path=$(nix build --no-link --print-out-paths)
  set +e

  run_lane() {
    local label=$1; shift
    local logfile
    logfile=$(mktemp)
    timeout --preserve-status 180 \
      env CHREST_BIN="$out_path/bin/chrest" \
      "$@" > >(tee "$logfile") 2>&1
    local rc=$?
    local expected passing failing
    expected=$(grep -m1 -E '^1\.\.[0-9]+$' "$logfile" | sed 's/^1\.\.//')
    passing=$(grep -cE '^ok [0-9]+ ' "$logfile")
    failing=$(grep -cE '^not ok [0-9]+ ' "$logfile")
    rm -f "$logfile"
    if [ -z "$expected" ]; then
      echo "FAIL [$label]: no TAP plan line (bats exit $rc)"; return 1
    fi
    if [ "$failing" -gt 0 ]; then
      echo "FAIL [$label]: $failing test(s) failed"; return 1
    fi
    if [ "$passing" -ne "$expected" ]; then
      echo "FAIL [$label]: expected $expected, saw $passing ok (bats exit $rc)"; return 1
    fi
    echo "PASS [$label]: $expected tests ok (bats exit $rc)"
    return 0
  }

  rc=0
  run_lane fence bats --filter-tags '!firefox' zz-tests_bats/ || rc=1
  run_lane firefox bats --no-sandbox --filter-tags 'firefox' zz-tests_bats/ || rc=1
  exit $rc

dev-install-mcp:
  #!/usr/bin/env bash
  # `dev-mcp` writes a project-local .mcp.json with a `chrest-dev`
  # server key pointing at the nix store path. That gives us a
  # separate MCP entry alongside the production `chrest`, so we can
  # A/B against the dev binary without overwriting the global
  # ~/.claude.json. The .mcp.json must be generated by the same
  # binary it points at because dev-mcp resolves its own executable
  # path.
  set -euo pipefail
  out=$(nix build --no-link --print-out-paths)
  "$out/bin/chrest" dev-mcp

demo:
  vhs demo/demo.tape

# Tag a Go module release. The "go/v" prefix is added for you, so pass
# the semver without it. Usage: just tag 0.0.2 "feat: release tooling"
[group('release')]
tag version message:
  #!/usr/bin/env bash
  set -euo pipefail
  tag="go/v{{version}}"
  prev=$(git tag --sort=-v:refname -l "go/v*" | head -1)
  if [[ -n "$prev" ]]; then
    echo "==> Previous: $prev"
    git log --oneline "$prev"..HEAD -- go/
  fi
  git tag -s -m "{{message}}" "$tag"
  echo "==> Created tag: $tag"
  git push origin "$tag"
  echo "==> Pushed $tag"
  git tag -v "$tag"

# Sed-rewrite chrestVersion in flake.nix to the given semver. The
# version string is burnt into the binary at build time via the fork's
# auto-injected -ldflags (see go/cmd/chrest/main.go version/commit
# vars), so flake.nix is the single source of truth. No-op if already
# at the target version. Usage: just bump-version 0.0.2
[group('release')]
bump-version new_version:
  #!/usr/bin/env bash
  set -euo pipefail
  current=$(grep 'chrestVersion = ' flake.nix | sed 's/.*"\(.*\)".*/\1/')
  if [[ "$current" == "{{new_version}}" ]]; then
    echo "==> already at {{new_version}}"
    exit 0
  fi
  sed -i.bak 's/chrestVersion = "'"$current"'"/chrestVersion = "{{new_version}}"/' flake.nix && rm flake.nix.bak
  echo "==> bumped chrestVersion: $current -> {{new_version}}"

# Cut a release: must be run on master. Bumps chrestVersion in
# flake.nix, commits the bump with a changelog-style message built
# from commits since the last go/v* tag, pushes master, then signs
# and pushes the go/v{{version}} tag. The "go/v" prefix is added for
# you, so pass the semver without it. Usage: just release 0.0.2
#
# Use `just tag <version> <message>` directly if you want to
# control the commit message yourself without bumping.
[group('release')]
release version:
  #!/usr/bin/env bash
  set -euo pipefail
  current_branch=$(git rev-parse --abbrev-ref HEAD)
  if [[ "$current_branch" != "master" ]]; then
    echo "ERROR: just release must be run on master (currently on $current_branch)" >&2
    exit 1
  fi
  prev=$(git tag --sort=-v:refname -l "go/v*" | head -1)
  header="release v{{version}}"
  if [[ -n "$prev" ]]; then
    summary=$(git log --format='- %s' "$prev"..HEAD -- go/)
    if [[ -n "$summary" ]]; then
      msg="$header"$'\n\n'"$summary"
    else
      msg="$header"
    fi
  else
    msg="$header"
  fi
  just bump-version "{{version}}"
  if ! git diff --quiet flake.nix; then
    git add flake.nix
    git commit -m "chore: release go/v{{version}}"
    git push origin master
    echo "==> pushed flake.nix bump to master"
  fi
  just tag "{{version}}" "$msg"

[group: 'explore']
explore-setup browser="firefox":
  just build
  go/build/release/chrest init --browser {{browser}} --name primary

explore-run browser="firefox":
  #!/usr/bin/env bash
  set -euo pipefail
  if [ "{{browser}}" = "firefox" ]; then
    web-ext run --target firefox-desktop --source-dir extension/dist-firefox
  else
    web-ext run --target chromium --source-dir extension/dist-chrome --start-url "chrome://extensions"
  fi

explore-capture format="text" browser="firefox" url="https://example.com" output="":
  #!/usr/bin/env bash
  set -euo pipefail
  out=$(nix build --no-link --print-out-paths)
  if [ -n "{{output}}" ]; then
    timeout 30 "$out/bin/chrest" capture --format {{format}} --browser {{browser}} --url "{{url}}" > "{{output}}"
    echo "wrote {{output}}"
  else
    timeout 30 "$out/bin/chrest" capture --format {{format}} --browser {{browser}} --url "{{url}}"
  fi

# Capture a small diverse page set across the three markdown variants so
# the output can be visually compared. Writes results to /tmp/md-samples/
# and echoes the list at the end. Uses the debug-tagged binary
# (go/build/release/chrest) because it's already built in the dev loop
# and firefox is on the dev shell PATH.
explore-markdown-samples:
  #!/usr/bin/env bash
  set -uo pipefail
  out_dir=/tmp/md-samples
  mkdir -p "$out_dir"
  CHREST=go/build/release/chrest
  capture() {
    local src=$1 url=$2 fmt=$3 sel=${4:-}
    local out="$out_dir/${src}-${fmt}.md"
    local -a selflag=()
    if [ -n "$sel" ]; then selflag=(--selector "$sel"); fi
    echo "== $src $fmt ==" >&2
    if timeout 45 "$CHREST" capture --format "$fmt" --browser firefox --url "$url" \
         "${selflag[@]}" --output "$out" >"$out_dir/${src}-${fmt}.stderr" 2>&1; then
      echo "  ok $(wc -c <"$out") bytes" >&2
    else
      echo "  FAIL (see $out_dir/${src}-${fmt}.stderr)" >&2
    fi
  }
  for fmt in markdown-full markdown-reader; do
    capture swblog https://simonwillison.net/2026/Feb/15/gwtar/ "$fmt"
    capture wiki   https://en.wikipedia.org/wiki/Markdown                                                                  "$fmt"
    capture mdn    https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Array/map              "$fmt"
    capture hn     https://news.ycombinator.com/item?id=46762667                                                            "$fmt"
  done
  capture swblog https://simonwillison.net/2026/Feb/15/gwtar/ markdown-selector article
  capture wiki   https://en.wikipedia.org/wiki/Markdown       markdown-selector '#bodyContent'
  capture mdn    https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Array/map markdown-selector main
  capture hn     https://news.ycombinator.com/item?id=46762667 markdown-selector '#hnmain'
  echo >&2
  echo "=== outputs ===" >&2
  ls -la "$out_dir"/*.md 2>/dev/null >&2 || true

[group: 'explore']
explore-vendor-dewey:
  #!/usr/bin/env bash
  set -euo pipefail
  src="/Users/sfriedenberg/.cache/go/pkg/mod/github.com/amarbel-llc/purse-first/libs/dewey@v0.0.4"
  dst="go/libs/dewey"
  pkgs=(
    "0/interfaces"
    "0/stack_frame"
    "0/primordial"
    "0/box_chars"
    "0/http_statuses"
    "alfa/pool"
    "alfa/cmp"
    "bravo/errors"
    "bravo/collections_slice"
    "charlie/ui"
    "charlie/ohio"
    "charlie/values"
    "charlie/flags"
    "charlie/quiter"
    "0/flag_policy"
    "delta/cli"
    "delta/collections_value"
    "golf/jsonrpc"
    "golf/transport"
    "golf/server"
    "golf/protocol"
    "golf/command"
  )
  for pkg in "${pkgs[@]}"; do
    mkdir -p "$dst/$pkg"
    for f in "$src/$pkg"/*.go; do
      [ -f "$f" ] || continue
      base=$(basename "$f")
      # Skip test files
      [[ "$base" == *_test.go ]] && continue
      cp "$f" "$dst/$pkg/$base"
    done
    echo "  copied $pkg ($(ls "$dst/$pkg"/*.go 2>/dev/null | wc -l) files)"
  done
  # Exclude golf/command/huh/ subpackage (charmbracelet dep, not used by chrest)
  echo "done — $dst populated"

[group: 'explore']
explore-mcp-v1-debug:
  #!/usr/bin/env bash
  set -euo pipefail
  v1_init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}'
  notif='{"jsonrpc":"2.0","method":"notifications/initialized"}'
  list='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  result=$(printf '%s\n' "$v1_init" "$notif" "$list" | go/build/release/chrest mcp)
  echo "=== init response ==="
  echo "$result" | grep '"id":1' | jq .
  echo "=== tools/list response (web-fetch) ==="
  echo "$result" | grep '"id":2' | jq '[.result.tools[] | select(.name == "web-fetch")] | first'

[group: 'explore']
explore-mcp-web-fetch-blocks url="https://example.com" selector="":
  #!/usr/bin/env bash
  set -euo pipefail
  init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}'
  notif='{"jsonrpc":"2.0","method":"notifications/initialized"}'
  call=$(jq -nc --arg url "{{url}}" --arg sel "{{selector}}" '
    {jsonrpc:"2.0",id:2,method:"tools/call",params:{name:"web-fetch",
      arguments: ($sel | if . == "" then {url:$url,format:"markdown"}
                        else {url:$url,format:"markdown",selector:.} end)}}')
  result=$(printf '%s\n' "$init" "$notif" "$call" | go/build/release/chrest mcp)
  echo "=== content block shapes (types + keys, content elided) ==="
  echo "$result" | grep '"id":2' | jq '.result.content | map({type, uri, name, mimeType, text_bytes: (.text // "" | length), resource_bytes: (.resource.text // "" | length), resource_uri: .resource.uri})'
  echo
  echo "=== TOC (content[0].text) first 20 lines ==="
  echo "$result" | grep '"id":2' | jq -r '.result.content[0].text' | head -20

# Verify Firefox/BiDi response interception support: addIntercept at the
# responseStarted phase + continueResponse + failRequest. Pre-requisite
# spike for the web-fetch content-type-dispatch design
# (docs/plans/2026-04-29-web-fetch-content-type-dispatch-design.md).
# Launches a real headless Firefox via the standard NewSession.
[group: 'explore']
explore-bidi-intercept:
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  CHREST_SPIKE_BIDI_INTERCEPT=1 go test -tags spike -count=1 -v \
    -run TestSpikeBiDiResponseIntercept \
    ./src/charlie/firefox/...

# Same as explore-bidi-intercept but exercises the typed BiDi intercept
# wrappers (Session.AddResponseIntercept / ContinueResponse /
# RemoveIntercept) instead of the raw conn.Send calls.
[group: 'explore']
explore-bidi-intercept-typed:
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  CHREST_SPIKE_BIDI_INTERCEPT=1 go test -tags spike -count=1 -v \
    -run TestSession_AddResponseIntercept \
    ./src/charlie/firefox/...

[group: 'explore']
explore-rewrite-dewey-imports:
  #!/usr/bin/env bash
  set -euo pipefail
  old="github.com/amarbel-llc/purse-first/libs/dewey"
  new="code.linenisgreat.com/chrest/go/libs/dewey"
  # Rewrite vendored dewey files
  count=0
  while IFS= read -r f; do
    if grep -q "$old" "$f"; then
      sed -i'' "s|$old|$new|g" "$f"
      count=$((count + 1))
    fi
  done < <(find go/libs/dewey -name '*.go' -type f)
  echo "rewrote $count vendored files"
  # Rewrite chrest source files
  count2=0
  while IFS= read -r f; do
    if grep -q "$old" "$f"; then
      sed -i'' "s|$old|$new|g" "$f"
      count2=$((count2 + 1))
    fi
  done < <(find go/src go/cmd -name '*.go' -type f)
  echo "rewrote $count2 chrest source files"

explore-client +httpie_args:
  go/build/release/chrest client {{httpie_args}}

# End-to-end sanity for `chrest capture-batch` using the RFC 0001
# fixture + writer stub landed in ~/eng/aim/ by the nebulous session.
# Pipes the example batch input through chrest, pretty-prints the
# output JSON, and echoes any stderr chrest emitted. Intended to be
# re-run after chrest changes to verify the cross-session contract
# still matches.
explore-capture-batch input="/home/sasha/eng/aim/fixtures/batch-input.example.json":
  #!/usr/bin/env bash
  set -euo pipefail
  err=$(mktemp)
  trap 'rm -f "$err"' EXIT
  go/build/release/chrest capture-batch < "{{input}}" 2>"$err" | jq '.'
  if [ -s "$err" ]; then
    echo "--- chrest stderr ---" >&2
    cat "$err" >&2
  fi

# Drive chrest capture-batch against a real HTTP fixture and save
# every writer-stdin artifact to disk so envelope / spec / payload
# bytes can be visually reviewed. Use to sanity-check artifact shape
# against RFC 0001 after non-trivial capturebatch changes.
#
# Output goes under /tmp/chrest-envelope-review.<timestamp>/. Prints
# the batch output JSON + a categorized dump of every artifact.
explore-envelope-review format="text" browser="firefox" split="true":
  #!/usr/bin/env bash
  set -euo pipefail
  just build-go
  out_dir=$(mktemp -d "/tmp/chrest-envelope-review.XXXXXX")
  echo "review dir: $out_dir" >&2
  rec_dir="$out_dir/artifacts"
  mkdir -p "$rec_dir"

  # Recording writer: tee stdin to a file, emit a JSON ref.
  cat >"$out_dir/writer.sh" <<EOF
  #!/usr/bin/env bash
  out=\$(mktemp "$rec_dir/artifact.XXXXXX")
  cat > "\$out"
  size=\$(wc -c < "\$out")
  echo "{\"id\":\"blake2b256-rec-\$(basename \$out)\",\"size\":\$size}"
  EOF
  chmod +x "$out_dir/writer.sh"

  # Minimal HTML fixture in the same dir the server will serve.
  cat >"$out_dir/test.html" <<'HTML'
  <!doctype html>
  <html><head><title>envelope review</title></head>
  <body><h1>Hello from chrest</h1><p>Fixture for envelope-review recipe.</p></body>
  </html>
  HTML

  # Python http.server on an ephemeral port.
  port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')
  (cd "$out_dir" && python3 -m http.server "$port" >/dev/null 2>&1) &
  srv_pid=$!
  trap 'kill $srv_pid 2>/dev/null || true' EXIT
  for _ in $(seq 1 50); do
    curl -sf "http://127.0.0.1:$port/test.html" >/dev/null && break
    sleep 0.1
  done

  cat <<JSON > "$out_dir/input.json"
  {
    "schema": "web-capture-archive/v1",
    "writer": {"cmd": ["$out_dir/writer.sh"]},
    "url": "http://127.0.0.1:$port/test.html",
    "defaults": {"browser": "{{browser}}", "split": {{split}}},
    "captures": [{"name": "c", "format": "{{format}}"}]
  }
  JSON

  echo "=== batch output ===" >&2
  go/build/release/chrest capture-batch < "$out_dir/input.json" | tee "$out_dir/output.json" | jq '.'

  echo >&2
  echo "=== artifact classification ===" >&2
  for f in "$rec_dir"/artifact.*; do
    name=$(basename "$f")
    size=$(wc -c < "$f")
    magic=$(xxd -l 8 -p "$f" 2>/dev/null || true)
    # Try to decide artifact type from content.
    kind="unknown"
    if jq -e '.schema | startswith("web-capture-archive.envelope")' < "$f" >/dev/null 2>&1; then
      kind="envelope"
    elif jq -e '.schema | startswith("web-capture-archive.spec")' < "$f" >/dev/null 2>&1; then
      kind="spec"
    else
      case "$magic" in
        89504e47*) kind="payload-png" ;;
        25504446*) kind="payload-pdf" ;;
        *) kind="payload-other" ;;
      esac
    fi
    printf '%-18s %-8s %10s bytes  %s\n' "$name" "$kind" "$size" "$magic" >&2
  done

  echo >&2
  echo "=== pretty JSON artifacts ===" >&2
  for f in "$rec_dir"/artifact.*; do
    if jq -e 'type == "object"' < "$f" >/dev/null 2>&1; then
      echo "--- $(basename "$f") ---" >&2
      jq '.' < "$f" >&2
    fi
  done
  echo >&2
  echo "artifact files kept in: $rec_dir" >&2

# Decompress every FlateDecode stream in a PDF looking for one that
# contains the /Info dict fields (Producer / CreationDate / ModDate).
# Used while investigating chrest#27 — lets us see whether pdfcpu put
# the re-stamped /Info entries in plain text or inside a compressed
# object stream (answer: compressed). Keep as a debug tool.
explore-pdf-inspect-info pdf:
  #!/usr/bin/env python3
  import zlib, re
  with open("{{pdf}}", "rb") as f:
      b = f.read()
  found = False
  for m in re.finditer(rb"stream\r?\n(.*?)\r?\nendstream", b, re.DOTALL):
      try:
          dec = zlib.decompress(m.group(1))
      except Exception:
          continue
      if b"pdfcpu" in dec or b"CreationDate" in dec or b"ModDate" in dec:
          print("--- decompressed stream (len={} bytes) ---".format(len(dec)))
          print(dec[:2000].decode("latin-1", errors="replace"))
          found = True
  if not found:
      print("(no decompressed stream contained pdfcpu/CreationDate/ModDate)")

# Print chrest's help text (both top-level and per-command) so we can
# verify command discoverability after any registration changes.
explore-help subcommand="":
  #!/usr/bin/env bash
  set -euo pipefail
  if [ -n "{{subcommand}}" ]; then
    go/build/release/chrest {{subcommand}} --help
  else
    go/build/release/chrest --help
  fi

# Run chrest-jcs on a shared byte-stability fixture and compare the
# sha256 against the remote implementation's hash. Output file lives
# next to the input in the aim/ directory so other sessions can diff
# it. Hash printed to stdout and written beside the output file.
explore-jcs-fixture vector="jcs-spec-vector-1" expected="":
  #!/usr/bin/env bash
  set -euo pipefail
  fixtures=/home/sasha/eng/aim/fixtures
  input="$fixtures/{{vector}}.input.json"
  output="$fixtures/{{vector}}.chrest.canonical.json"
  if [ ! -f "$input" ]; then
    echo "missing input: $input" >&2
    exit 1
  fi
  go/build/release/chrest-jcs < "$input" > "$output"
  got=$(sha256sum "$output" | awk '{print $1}')
  echo "output=$output"
  echo "sha256=$got"
  if [ -n "{{expected}}" ]; then
    if [ "$got" = "{{expected}}" ]; then
      echo "MATCH (expected $got)"
    else
      echo "MISMATCH (expected {{expected}}, got $got)" >&2
      exit 2
    fi
  fi

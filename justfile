
# Aggregator layout per eng#37 + eng#91: `default` chains the four
# lifecycle phases (`validate lint build test`); each is an
# aggregate-only recipe that lists every leaf in its phase. `default`
# is the sweatfile pre-merge hook, so anything that should gate a
# merge belongs in one of these aggregates. See
# eng-design_patterns-justfile(7) for the verb / group taxonomy.
default: validate lint build test

# Pre-build static checks: hard failures on parse / schema / drift.
validate: validate-devshell validate-nix validate-dagnabit-export validate-dagnabit-reposition

# Pre-build opinion checks: read-only style / convention.
lint: lint-fmt

# All build artifacts. `build-nix` runs the chrest derivation (which
# builds chrest + chrest-server + chrest-jcs and runs the Go unit
# suite in checkPhase), so a clean `just build` proves both compile
# and unit tests. The dev-loop binaries (build-go) and the extension
# (build-extension) are intentionally NOT in the aggregate — they
# rebuild artifacts the prod derivation already covers.
build: build-nix

# Post-build smoke + integration. Unit tests already ran inside the
# nix sandbox during `build-nix`'s checkPhase, so this layer only
# adds the bats + MCP-inspector integration lanes. Matches madder,
# where `nix build` covers unit tests and `just test` runs the
# integration lanes.
test: test-mcp test-mcp-bats

# `nix build` runs the chrest derivation, which builds chrest +
# chrest-server + chrest-jcs and runs the Go unit suite in checkPhase.
[group("build")]
build-nix:
  nix build --no-link

# Devshell rapid-iteration: builds all three binaries into
# go/build/release/ so the explore-* recipes (which reference
# go/build/release/chrest by path) keep working. Skips checkPhase and
# the firefox/monolith wrap. Mirrors madder's `build-go`.
[group("build")]
build-go:
  cd go && go build -o build/release/ ./cmd/...

[group("build")]
build-extension:
  just extension/build

# Verify the devShell evaluates and builds without errors. Catches
# vendor-env / goFlakeInputs / mkGoEnv breakage that the prod-binary
# build can mask from cache. No store-output usage --- just a build-
# check. See eng-design_patterns-justfile(7) VALIDATE-DEVSHELL.
[group("pre-build")]
validate-devshell:
  nix build --no-link .#devShells.{{ arch() }}-linux.default

# Evaluate flake outputs for every supported system. Catches malformed
# fixed-output hashes on non-host platforms before they surface in
# flakehub-push's inspect wrapper (see chrest#50).
#
# Previously pre-pinned cross-system devShell + package .drvs as GC
# roots via `nix-instantiate --add-root`. That loop was removed
# because it began failing on cross-system IFDs whose outputs aren't
# fixed-output (so they can't be substituted from cache and can't be
# built on a host without binfmt/QEMU for the foreign system). See
# amarbel-llc/eng#98 for whether the gcroots strategy is still
# needed at all.
[group("pre-build")]
validate-nix:
  nix flake check --no-build

# Read-only formatting gate. Builds `checks.treefmt`, which runs
# treefmt over a /nix/store snapshot of the source tree and fails
# if anything would change. Does NOT modify files in the worktree
# --- the modifying counterpart is `codemod-fmt-treefmt`. See
# eng-design_patterns-justfile(7) LINT-FMT.
[group("pre-build")]
lint-fmt:
  #!/usr/bin/env bash
  set -euo pipefail
  system=$(nix eval --raw --impure --expr 'builtins.currentSystem')
  nix build --print-build-logs --no-link ".#checks.${system}.treefmt"

# Reinstalls the native-messaging-host manifest pointing at the
# nix-built (firefox-wrapped) chrest, then reloads the running
# extension. Uses the nix output rather than go/build/release/ so the
# manifest points at a binary with firefox + monolith on PATH.
[group("operational")]
load-extension:
  #!/usr/bin/env bash
  set -euo pipefail
  out=$(nix build --no-link --print-out-paths)
  "$out/bin/chrest" install jbcogiaaaaikinoljmplilmcnicpfoek
  chrest reload-extension

# Devshell rapid-iteration. Mirrors madder's `test-go *flags`.
[group("post-build")]
test-go *flags:
  cd go && go test {{flags}} ./...

# Regenerate gomod2nix.toml from go.mod / go.sum. Run after
# `just go/add-dep <pkg>` (or any manual go.mod edit); stage the
# updated gomod2nix.toml alongside go.mod and go.sum. `nix build`
# will fail loudly if the manifest is out of sync — that's the
# drift signal now, not a justfile drift-guard.
[group("build")]
build-gomod2nix:
  cd go && gomod2nix

dagnabit := "nix run github:amarbel-llc/purse-first#dagnabit --"

# Regenerate pkgs/<leaf>/main.go facades from `//go:generate dagnabit
# export` directives in go/internal/. Stage the regenerated pkgs/
# tree alongside any source changes; external consumers (e.g.
# dodder) import via pkgs/<leaf>, so the facade is the API contract.
[group("build")]
build-dagnabit-export:
  cd go && {{dagnabit}} export

# CI drift gate: pkgs/ must match what `dagnabit export` would emit
# right now. Re-runs the exporter into a sibling dir (dagnabit's
# -output-dir is always module-root-relative, so we can't use a
# /tmp path) and diffs against the committed pkgs/. Joined into the
# `validate` aggregator so merges catch facades that fell behind
# their internal package's exported surface.
[group("pre-build")]
validate-dagnabit-export:
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  tmp_rel="pkgs.validate-dagnabit-export.tmp"
  trap 'rm -rf "$tmp_rel"' EXIT
  {{dagnabit}} export -output-dir "$tmp_rel"
  diff -ru pkgs "$tmp_rel"

# CI drift gate: NATO-level tiering of go/internal/ must match what
# `dagnabit reposition` would compute by current dependency height.
# Runs the dry-run; any would-move event is a drift failure. Run
# `just codemod-dagnabit-reposition apply` to fix (the move
# subcommand does type-aware import rewrites in callers automatically).
[group("pre-build")]
validate-dagnabit-reposition:
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  out=$({{dagnabit}} -n internal)
  if [ -n "$out" ]; then
    echo "$out"
    echo "FAIL: dagnabit-reposition would-move events present (above)." >&2
    echo "      Run 'just codemod-dagnabit-reposition apply' to fix the layout drift." >&2
    exit 1
  fi

# Re-tier packages under go/internal/<level>/<leaf> by current
# dependency height. Dry-runs by default; pass `apply` to commit
# moves. Re-run when a dependency change bumps a leaf into a
# different NATO tier (rare).
[group("codemod")]
codemod-dagnabit-reposition apply="":
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  if [ "{{apply}}" = "apply" ]; then
    {{dagnabit}} internal
  else
    {{dagnabit}} -n -v internal
  fi

# All `nix fmt`-driven rewrites.
codemod-fmt: codemod-fmt-treefmt

# Apply treefmt over the worktree. Modifying counterpart to
# `lint-fmt`; both consume the same treefmt.nix config.
[group("codemod")]
codemod-fmt-treefmt:
  nix fmt

mcp-inspect := "npx @modelcontextprotocol/inspector --cli"

[group("post-build")]
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

[group("post-build")]
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
  # bwrap --unshare-pid; root cause open). The new bats wrapper
  # (amarbel-llc/bats `bats` package) splits passing records into a
  # sidecar file and prints failure records + a trailing summary
  # record to stdout as NDJSON. We validate by locating that
  # `{"type":"summary",...}` record and asserting valid && !bailed
  # && failed==0, independent of bats's own exit code.
  #
  # Tests find the chrest binary via CHREST_BIN env var (see
  # zz-tests_bats/lib/common.bash).
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
    local summary
    summary=$(grep -E '^\{"type":"summary"' "$logfile" | tail -n1)
    rm -f "$logfile"
    if [ -z "$summary" ]; then
      echo "FAIL [$label]: no NDJSON summary record (bats exit $rc)"; return 1
    fi
    local passed failed total valid bailed
    passed=$(echo "$summary" | jq -r '.passed')
    failed=$(echo "$summary" | jq -r '.failed')
    total=$(echo "$summary" | jq -r '.total')
    valid=$(echo "$summary" | jq -r '.valid')
    bailed=$(echo "$summary" | jq -r '.bailed')
    if [ "$valid" != "true" ] || [ "$bailed" = "true" ]; then
      echo "FAIL [$label]: summary valid=$valid bailed=$bailed (bats exit $rc)"; return 1
    fi
    if [ "$failed" -gt 0 ]; then
      echo "FAIL [$label]: $failed/$total tests failed (bats exit $rc)"; return 1
    fi
    echo "PASS [$label]: $passed/$total tests ok (bats exit $rc)"
    return 0
  }

  rc=0
  run_lane fence bats --filter-tags '!firefox' zz-tests_bats/ || rc=1
  run_lane firefox bats --no-sandbox --filter-tags 'firefox' zz-tests_bats/ || rc=1
  exit $rc

# Write a project-local .mcp.json with a `chrest-dev` server key
# pointing at the nix store path. Gives us a separate MCP entry
# alongside the production `chrest`, so we can A/B against the dev
# binary without overwriting the global ~/.claude.json. The
# .mcp.json must be generated by the same binary it points at because
# `chrest dev-mcp` resolves its own executable path.
[group("operational")]
install-mcp-dev:
  #!/usr/bin/env bash
  set -euo pipefail
  out=$(nix build --no-link --print-out-paths)
  "$out/bin/chrest" dev-mcp

# Generate the VHS demo GIF.
[group("build")]
build-demo:
  vhs demo/demo.tape

# Sign + push two parallel tags at HEAD: `vX.Y.Z` (project-level
# canonical name used by the flake, extension, MCP serverInfo) and
# `go/vX.Y.Z` (path-prefix tag preserved so downstream Go module
# consumers like dodder can `require code.linenisgreat.com/chrest/go
# vX.Y.Z`). Both point at the same commit. The "v" prefix is added
# for you. Usage: just deploy-tag 0.0.2 "feat: release tooling"
[group("operational")]
deploy-tag version message:
  #!/usr/bin/env bash
  set -euo pipefail
  prev=$(git tag --sort=-v:refname -l "go/v*" | head -1)
  if [[ -n "$prev" ]]; then
    echo "==> Previous: $prev"
    git log --oneline "$prev"..HEAD -- go/
  fi
  project_tag="v{{version}}"
  go_tag="go/v{{version}}"
  git tag -s -m "{{message}}" "$project_tag"
  git tag -s -m "{{message}}" "$go_tag"
  echo "==> Created tags: $project_tag, $go_tag"
  git push origin "$project_tag" "$go_tag"
  echo "==> Pushed $project_tag, $go_tag"
  git tag -v "$project_tag"
  git tag -v "$go_tag"

# Sed-rewrite chrestVersion to the given semver across the two
# source-controlled callers: flake.nix's `chrestVersion = "..."`
# (the single source of truth — flake outputs derive from this and
# pass it down to the Go binary, MCP serverInfo, and extension
# manifest via callPackage) and extension/manifest-common.json's
# static `"version"` (which the extension build always overwrites
# with the flake value, but the source-controlled value is kept in
# sync so editor / direct manifest reads see the right number).
# No-op if already at the target version. Usage: just bump-version 0.0.2
[group("maintenance")]
bump-version new_version:
  #!/usr/bin/env bash
  set -euo pipefail
  current=$(grep 'chrestVersion = ' flake.nix | sed 's/.*"\(.*\)".*/\1/')
  if [[ "$current" == "{{new_version}}" ]]; then
    echo "==> already at {{new_version}}"
    exit 0
  fi
  sed -i.bak 's/chrestVersion = "'"$current"'"/chrestVersion = "{{new_version}}"/' flake.nix && rm flake.nix.bak
  sed -i.bak 's/"version": "'"$current"'"/"version": "{{new_version}}"/' extension/manifest-common.json && rm extension/manifest-common.json.bak
  echo "==> bumped chrestVersion: $current -> {{new_version}}"

# Cut a release: must be run on master. Bumps chrestVersion across
# flake.nix + extension/manifest-common.json, commits the bump with
# a changelog-style message built from commits since the last
# go/v* tag, pushes master, then signs and pushes BOTH
# `vX.Y.Z` (project-level) and `go/vX.Y.Z` (Go module path-prefix)
# tags. The "v" prefix is added for you. Usage: just deploy-release 0.0.2
#
# Use `just deploy-tag <version> <message>` directly if you want to
# control the commit message yourself without bumping.
[group("operational")]
deploy-release version:
  #!/usr/bin/env bash
  set -euo pipefail
  current_branch=$(git rev-parse --abbrev-ref HEAD)
  if [[ "$current_branch" != "master" ]]; then
    echo "ERROR: just deploy-release must be run on master (currently on $current_branch)" >&2
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
  if ! git diff --quiet flake.nix extension/manifest-common.json; then
    git add flake.nix extension/manifest-common.json
    git commit -m "chore: release v{{version}}"
    git push origin master
    echo "==> pushed version bump to master"
  fi
  just deploy-tag "{{version}}" "$msg"

[group("explore")]
explore-setup browser="firefox":
  just build
  go/build/release/chrest init --browser {{browser}} --name primary

[group("explore")]
explore-run browser="firefox":
  #!/usr/bin/env bash
  set -euo pipefail
  if [ "{{browser}}" = "firefox" ]; then
    web-ext run --target firefox-desktop --source-dir extension/dist-firefox
  else
    web-ext run --target chromium --source-dir extension/dist-chrome --start-url "chrome://extensions"
  fi

[group("explore")]
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
[group("explore")]
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

[group("explore")]
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

[group("explore")]
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

[group("explore")]
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
[group("explore")]
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
[group("explore")]
explore-bidi-intercept-typed:
  #!/usr/bin/env bash
  set -euo pipefail
  cd go
  CHREST_SPIKE_BIDI_INTERCEPT=1 go test -tags spike -count=1 -v \
    -run TestSession_AddResponseIntercept \
    ./src/charlie/firefox/...

[group("explore")]
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

[group("explore")]
explore-client +httpie_args:
  go/build/release/chrest client {{httpie_args}}

# End-to-end sanity for `chrest capture-batch` using the RFC 0001
# fixture + writer stub landed in ~/eng/aim/ by the nebulous session.
# Pipes the example batch input through chrest, pretty-prints the
# output JSON, and echoes any stderr chrest emitted. Intended to be
# re-run after chrest changes to verify the cross-session contract
# still matches.
[group("explore")]
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
[group("explore")]
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
[group("explore")]
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
[group("explore")]
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
[group("explore")]
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

# Curl a URL and report every element whose `id` attribute matches the
# given value, in document order. Used to confirm whether a page has
# duplicate ids that confuse cascadia.Query first-match semantics.
[group: 'explore']
explore-inspect-page-ids url id:
  #!/usr/bin/env bash
  set -euo pipefail
  tmp=$(mktemp)
  trap 'rm -f "$tmp"' EXIT
  curl -fsSL "{{url}}" -o "$tmp"
  size=$(wc -c < "$tmp")
  echo "fetched $size bytes from {{url}}"
  echo
  count=$(grep -oE "id=\"{{id}}\"" "$tmp" | wc -l)
  echo "id=\"{{id}}\" appears $count time(s)"
  echo
  echo "=== context (200 chars before, 1500 chars after each match) ==="
  grep -boE ".{0,200}id=\"{{id}}\".{0,1500}" "$tmp" || true

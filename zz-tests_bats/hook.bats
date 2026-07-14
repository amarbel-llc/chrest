#!/usr/bin/env bats
# Tests for the pre-commit hook's dagnabit wrapper (chrest#106).
#
# The wrapper must rebuild dagnabit from the project's staged flake.lock
# when a purse-first bump is being committed — the pre-built binary
# (baked in at derivation eval time) would produce byte-identical facades
# and the merge gate's fresh nix build would see version-stamp drift.
#
# CONFORMIST_DAGNABIT_BIN — path to the conformist-dagnabit wrapper binary.
# Set by test-mcp-bats in the justfile; falls back to PATH lookup.

bats_load_library bats-support
bats_load_library bats-assert

: "${CONFORMIST_DAGNABIT_BIN:=$(command -v conformist-dagnabit 2>/dev/null || true)}"

require_dagnabit_bin() {
  if [ -z "$CONFORMIST_DAGNABIT_BIN" ] || [ ! -x "$CONFORMIST_DAGNABIT_BIN" ]; then
    skip "CONFORMIST_DAGNABIT_BIN not set or not executable"
  fi
}

# Create a fake "new" dagnabit in $BATS_TEST_TMPDIR/fake-bin that records
# invocations. Callers add their own fake git (and optionally fake nix) to
# fake-bin to exercise the full dispatch path.
setup_fake_dagnabit() {
  local dag_dir="$BATS_TEST_TMPDIR/new-dag"
  mkdir -p "$dag_dir/bin" "$BATS_TEST_TMPDIR/fake-bin"

  printf '#!/bin/sh\necho "new-dagnabit: $*"\n' >"$dag_dir/bin/dagnabit"
  chmod +x "$dag_dir/bin/dagnabit"
}

# Create a fake git in $BATS_TEST_TMPDIR/fake-bin that reports flake.lock
# as staged and returns $BATS_TEST_TMPDIR as the project root.
make_fake_git_staged() {
  printf '#!/bin/sh\ncase "$1" in\n  diff) echo "flake.lock" ;;\n  rev-parse) echo "%s" ;;\nesac\n' \
    "$BATS_TEST_TMPDIR" >"$BATS_TEST_TMPDIR/fake-bin/git"
  chmod +x "$BATS_TEST_TMPDIR/fake-bin/git"
}

@test "dagnabit wrapper dispatches to nix-built binary when flake.lock is staged" {
  require_dagnabit_bin
  setup_fake_dagnabit
  make_fake_git_staged

  # fake nix: returns the new-dag dir so the wrapper execs the new dagnabit
  printf '#!/bin/sh\necho "%s"\n' "$BATS_TEST_TMPDIR/new-dag" >"$BATS_TEST_TMPDIR/fake-bin/nix"
  chmod +x "$BATS_TEST_TMPDIR/fake-bin/nix"

  run env PATH="$BATS_TEST_TMPDIR/fake-bin:$PATH" "$CONFORMIST_DAGNABIT_BIN" export
  assert_success
  assert_output --partial "new-dagnabit: export"
}

@test "dagnabit wrapper falls through to built-in when flake.lock is not staged" {
  require_dagnabit_bin
  setup_fake_dagnabit

  # fake git: diff --cached returns nothing (no staged lock change)
  printf '#!/bin/sh\ntrue\n' >"$BATS_TEST_TMPDIR/fake-bin/git"
  chmod +x "$BATS_TEST_TMPDIR/fake-bin/git"

  run env PATH="$BATS_TEST_TMPDIR/fake-bin:$PATH" "$CONFORMIST_DAGNABIT_BIN" --version
  refute_output --partial "new-dagnabit:"
}

@test "dagnabit wrapper falls through to built-in when nix build fails" {
  require_dagnabit_bin
  setup_fake_dagnabit
  make_fake_git_staged

  # fake nix that fails (simulates network/build failure)
  printf '#!/bin/sh\nexit 1\n' >"$BATS_TEST_TMPDIR/fake-bin/nix"
  chmod +x "$BATS_TEST_TMPDIR/fake-bin/nix"

  run env PATH="$BATS_TEST_TMPDIR/fake-bin:$PATH" "$CONFORMIST_DAGNABIT_BIN" --version
  refute_output --partial "new-dagnabit:"
}

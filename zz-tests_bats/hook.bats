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

# Set up a scratch dir with a fake git and fake nix so the wrapper's
# dynamic dispatch path can be exercised without a real nix evaluation.
setup_mock_env() {
  local workdir="$1"
  local new_dag_dir="$workdir/new-dag"
  mkdir -p "$new_dag_dir/bin" "$workdir/fake-bin"

  # fake dagnabit that records it was invoked ("new" version)
  printf '#!/bin/sh\necho "new-dagnabit: $*"\n' > "$new_dag_dir/bin/dagnabit"
  chmod +x "$new_dag_dir/bin/dagnabit"

  # fake nix: always returns $new_dag_dir regardless of installable/flags
  printf '#!/bin/sh\necho "%s"\n' "$new_dag_dir" > "$workdir/fake-bin/nix"
  chmod +x "$workdir/fake-bin/nix"
}

@test "dagnabit wrapper dispatches to nix-built binary when flake.lock is staged" {
  require_dagnabit_bin

  local workdir
  workdir=$(mktemp -d)
  trap 'rm -rf "$workdir"' EXIT

  setup_mock_env "$workdir"

  # fake git: diff --cached reports flake.lock staged; rev-parse gives a
  # non-empty project root so the nix build path proceeds.
  printf '#!/bin/sh\ncase "$1" in\n  diff) echo "flake.lock" ;;\n  rev-parse) echo "%s" ;;\nesac\n' \
    "$workdir" > "$workdir/fake-bin/git"
  chmod +x "$workdir/fake-bin/git"

  run env PATH="$workdir/fake-bin:$PATH" "$CONFORMIST_DAGNABIT_BIN" export
  assert_success
  assert_output --partial "new-dagnabit: export"
}

@test "dagnabit wrapper falls through to built-in when flake.lock is not staged" {
  require_dagnabit_bin

  local workdir
  workdir=$(mktemp -d)
  trap 'rm -rf "$workdir"' EXIT

  setup_mock_env "$workdir"

  # fake git: diff --cached returns nothing (no staged lock change)
  printf '#!/bin/sh\ntrue\n' > "$workdir/fake-bin/git"
  chmod +x "$workdir/fake-bin/git"

  # The wrapper falls through to the built-in dagnabit (which runs the
  # real binary — we just check the new-dagnabit path was NOT taken).
  run env PATH="$workdir/fake-bin:$PATH" "$CONFORMIST_DAGNABIT_BIN" --version 2>&1 || true
  refute_output --partial "new-dagnabit:"
}

@test "dagnabit wrapper falls through to built-in when nix build fails" {
  require_dagnabit_bin

  local workdir
  workdir=$(mktemp -d)
  trap 'rm -rf "$workdir"' EXIT

  setup_mock_env "$workdir"

  # fake git: flake.lock IS staged
  printf '#!/bin/sh\ncase "$1" in\n  diff) echo "flake.lock" ;;\n  rev-parse) echo "%s" ;;\nesac\n' \
    "$workdir" > "$workdir/fake-bin/git"
  chmod +x "$workdir/fake-bin/git"

  # fake nix that fails (simulates network/build failure)
  printf '#!/bin/sh\nexit 1\n' > "$workdir/fake-bin/nix"
  chmod +x "$workdir/fake-bin/nix"

  # Should not error — wrapper falls back gracefully to the built-in.
  run env PATH="$workdir/fake-bin:$PATH" "$CONFORMIST_DAGNABIT_BIN" --version 2>&1 || true
  refute_output --partial "new-dagnabit:"
}

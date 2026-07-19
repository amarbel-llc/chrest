# chrest's conformist config as a nix module (eng#246 item 2). Replaces BOTH
# the former ./treefmt.nix (treefmt-nix: `nix fmt` / checks.treefmt) and the
# former hand-written ./conformist.toml shadow config: flake.nix evals this via
# `conformist.lib.evalModule` together with `conformist.lib.presets.eng`, and
# the GENERATED conformist.toml (build.configFile) drives `nix fmt`
# (build.wrapper), the sandboxed `checks.formatting` (build.check), the
# store-pinned `conformist-pre-commit` / `conformist-repair` hooks, and
# dagnabit's facade-format pass (DAGNABIT_CONFORMIST_CONFIG →
# `.#conformist-config`). `package` is injected by flake.nix.
#
# See eng-design_patterns-conformist(7), conformist-nix(7), and the
# cutting-garden / piggy flakes for the reference consumer shape.
{ lib, ... }:
{
  # justfile-task-hierarchy is force-disabled pending its own migration
  # (out of scope for the formatter flip, eng#246 item 2): it requires
  # every pipeline-verb leaf to sit in exactly one aggregate; chrest's
  # devshell dev-loop leaves (build-go, build-extension, test-go,
  # build-gomod2nix, build-dagnabit-export, build-demo,
  # codemod-dagnabit-reposition) are deliberately outside the merge-gate
  # aggregates and would need renames (cutting-garden's debug-build-go
  # shape) that ripple through documented workflows.
  #
  # eng-versioning-deprecated-file is enforced (no override): the
  # `chrestVersion = "X.Y.Z"` let-binding it used to flag was migrated to
  # version.env (eng-versioning(7)) — see flake.nix.
  #
  # The rest of the preset (flake hygiene + the other justfile conventions)
  # is enforced. mkForce because the preset sets enable = true directly.
  linters.justfile-task-hierarchy.enable = lib.mkForce false;

  # eng-versioning's own conformance check (whole-tree, checks version.env
  # itself) can't auto-derive the version key: it only looks for go.mod /
  # Cargo.toml AT THE TREE ROOT, but chrest's Go module lives under go/
  # (a polyglot layout — see flake.nix's chrestVersion comment). Pin the
  # key explicitly rather than rely on derivation.
  linters.eng-versioning.key = "CHREST_VERSION";

  # Go: goimports (priority 1) runs before gofumpt (priority 2) so the
  # import-grouped output is re-canonicalized by gofumpt. Both registry
  # programs pass -w (write in place), required for conformist's sandbox check
  # to observe drift.
  programs.goimports.enable = true;
  programs.goimports.priority = 1;
  programs.gofumpt.enable = true;
  programs.gofumpt.priority = 2;

  programs.nixfmt.enable = true;

  # JavaScript/TypeScript, JSON, CSS, HTML, YAML, markdown — the prettier
  # registry module's default file-type mapping covers the extension/ tree;
  # generated / vendored trees are kept off-limits via the global excludes
  # below.
  programs.prettier.enable = true;

  # shfmt: the registry defaults (indent_size = 2, simplify, caseIndent) emit
  # `-i 2 -s -ci` — the same flags the retired treefmt.nix forced by hand.
  # Narrow the includes to the shell we actually format; the registry default
  # also matches *.envrc, which chrest's direnv stub is not house-formatted as.
  programs.shfmt.enable = true;
  programs.shfmt.includes = [
    "*.sh"
    "*.bash"
    "*.bats"
  ];

  settings.excludes = [
    "flake.lock"
    "go/go.sum"
    "go/gomod2nix.toml"
    # Generated dagnabit facades are committed RAW (validate-dagnabit-export
    # diffs against the exporter's byte-exact output); never reformat them.
    # THREE globs are required (cutting-garden's hard-won shape): dagnabit's
    # facade-format pass runs conformist with the module root (go/) as the
    # tree root — a /nix/store config can't anchor an upward walk — so the
    # repo-root-relative first glob never matches there; "pkgs/**" covers the
    # go/-rooted invocation, and "**/pkgs/**" covers `dagnabit export -check`,
    # which regenerates into a temp dir and formats `<tmp>/pkgs/**` before
    # byte-comparing (an unexcluded temp copy gets gofumpt-grouped while the
    # committed side stays raw and the check phantom-fails).
    "go/pkgs/**"
    "pkgs/**"
    "**/pkgs/**"
    "extension/bun.lock"
    "extension/bun.nix"
    "extension/dist-*/**"
    "extension/assets/**"
    "result"
    "result-*/**"
    "dist-*/**"
    ".tmp/**"
    ".direnv/**"
    ".worktrees/**"
    "sweatfile"
    "LICENSE"
    "go/**/*.json"
    # Test fixtures are captured byte-exact (e.g. live DDG SERP pages in
    # */websearch/testdata); formatters must never rewrite them.
    "go/**/testdata/**"
  ];
}

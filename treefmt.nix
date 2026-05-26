# treefmt-nix configuration. Run via `nix fmt` or `just codemod-fmt-treefmt`.
# Sandboxed check derivation lives at `checks.<system>.treefmt` and is
# what `just lint-fmt` builds (see top-level justfile).
{ lib, ... }:
{
  projectRootFile = "flake.nix";

  # Go: goimports → gofumpt chain. Lower priority runs first; goimports
  # must run before gofumpt so the import-grouped output is then
  # re-canonicalized by gofumpt.
  programs.goimports.enable = true;
  settings.formatter.goimports.priority = 1;
  programs.gofumpt.enable = true;
  settings.formatter.gofumpt.priority = 2;

  programs.nixfmt.enable = true;

  programs.shfmt.enable = true;
  settings.formatter.shfmt.includes = [
    "*.sh"
    "*.bash"
    "*.bats"
  ];
  # treefmt-nix's shfmt module exposes `indent_size` and `simplify` but
  # not `--case-indent` (-ci). Override the full options list to keep
  # those flags AND add -ci so `case` branches stay indented one level
  # past the `case` keyword.
  settings.formatter.shfmt.options = lib.mkForce [
    "-i"
    "2"
    "-s"
    "-ci"
  ];

  settings.global.excludes = [
    "flake.lock"
    "go/go.sum"
    "go/gomod2nix.toml"
    "go/pkgs/**"
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
    "*.md"
    "*.json"
    "*.js"
    "*.mjs"
  ];
}

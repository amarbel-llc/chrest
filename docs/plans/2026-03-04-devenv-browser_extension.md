# devenv-browser_extension Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Create `devenvs/browser_extension/flake.nix` inside chrest that bundles all browser extension build tools, then wire it into the top-level devShell replacing `devenv-node` and scattered direct package entries.

**Architecture:** Standalone flake at `devenvs/browser_extension/flake.nix` using the stable-first nixpkgs convention. Top-level `flake.nix` references it via a `path:` input and pulls it in through `inputsFrom`, removing `jq`, `web-ext`, and `httpie` from the direct package list. `devenv-node` input is removed entirely.

**Tech Stack:** Nix flakes, nixpkgs stable + master, nodePackages.rollup

**Rollback:** `git revert` the two commits introduced here; `rm -rf devenvs/browser_extension/`.

---

### Task 1: Create devenvs/browser_extension/flake.nix

**Files:**

- Create: `devenvs/browser_extension/flake.nix`

**Step 1: Create the directory**

```bash
mkdir -p devenvs/browser_extension
```

**Step 2: Write the flake**

Create `devenvs/browser_extension/flake.nix` with this exact content (use same nixpkgs SHAs as the top-level flake to avoid duplicate fetches):

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/6d41bc27aaf7b6a3ba6b169db3bd5d6159cfaa47";
    nixpkgs-master.url = "github:NixOS/nixpkgs/5b7e21f22978c4b740b3907f3251b470f466a9a2";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
    }:
    (utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        pkgs-master = import nixpkgs-master { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.nodejs_latest
            pkgs.web-ext
            pkgs.jq
            pkgs.zip
            pkgs.httpie
            pkgs-master.nodePackages.rollup
          ];
        };
      }
    ));
}
```

> **Note on rollup:** `nodePackages.rollup` exists in nixpkgs. Using nixpkgs-master for the latest version. If the build fails citing a missing attribute, fall back to `pkgs.nodePackages.rollup` (stable) and note this in the commit message.

**Step 3: Git-add the file** (required — path: inputs only see git-tracked files)

```bash
git add devenvs/browser_extension/flake.nix
```

**Step 4: Verify the sub-flake evaluates**

```bash
nix flake show ./devenvs/browser_extension
```

Expected: prints `devShells.{system}.default` for your platform. Any eval error here means a typo in the flake — fix before continuing.

**Step 5: Commit**

```bash
git commit -m "feat: add devenvs/browser_extension flake"
```

---

### Task 2: Wire devenv-browser_extension into top-level flake.nix

**Promotion criteria:** N/A — this replaces devenv-node entirely; no old approach to remove separately.

**Files:**

- Modify: `flake.nix`
- Modify: `flake.lock` (generated)

**Step 1: Rewrite flake.nix**

Replace `flake.nix` with:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/6d41bc27aaf7b6a3ba6b169db3bd5d6159cfaa47";
    nixpkgs-master.url = "github:NixOS/nixpkgs/5b7e21f22978c4b740b3907f3251b470f466a9a2";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";

    devenv-go.url = "github:amarbel-llc/purse-first?dir=devenvs/go";
    devenv-browser_extension.url = "path:./devenvs/browser_extension";
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
      devenv-go,
      devenv-browser_extension,
    }:
    (utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            devenv-go.overlays.default
          ];
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = (
            with pkgs;
            [
              fish
              gnumake
              just
            ]
          );

          inputsFrom = [
            devenv-go.devShells.${system}.default
            devenv-browser_extension.devShells.${system}.default
          ];
        };
      }
    ));
}
```

**Step 2: Lock flake inputs**

```bash
nix flake lock
```

Expected: `flake.lock` updated with a `path` entry for `devenv-browser_extension`. No network errors.

**Step 3: Verify all tools are present in the shell**

Run each of these and confirm each prints a `/nix/store/...` path:

```bash
nix develop --command which node
nix develop --command which npm
nix develop --command which rollup
nix develop --command which web-ext
nix develop --command which jq
nix develop --command which zip
nix develop --command which http
nix develop --command which just
```

**Step 4: Commit**

```bash
git add flake.nix flake.lock
git commit -m "feat: replace devenv-node with devenv-browser_extension

Moves nodejs, web-ext, jq, zip, httpie, rollup into the new
devenv-browser_extension local flake. Removes devenv-node input.
Retains fish, gnumake, just as direct project-level packages."
```

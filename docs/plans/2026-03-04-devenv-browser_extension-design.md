# devenv-browser_extension Design

**Date:** 2026-03-04

## Problem

`chrest/flake.nix` directly lists browser-extension-specific tools (`web-ext`,
`jq`, `httpie`) alongside generic project tools. The existing `devenv-node` input
only provides `nodejs_latest`, `pnpm`, `yarn`, and `node2nix` — it does not cover
browser extension build tooling.

## Goal

Extract all browser-extension-specific tools into a dedicated
`devenv-browser_extension` flake, prototyped locally within chrest before
potential promotion to `purse-first/devenvs/`.

## Design

### New file: `devenvs/browser_extension/flake.nix`

A standard stable-first flake (`nixpkgs` → stable, `nixpkgs-master` → unstable)
exposing a single `devShells.default` with:

| Package               | Source         | Purpose                                                            |
| --------------------- | -------------- | ------------------------------------------------------------------ |
| `nodejs_latest`       | nixpkgs stable | node + npm runtime                                                 |
| `web-ext`             | nixpkgs stable | Firefox extension CLI (signing, AMO deploy)                        |
| `jq`                  | nixpkgs stable | Merge manifest-common.json with browser-specific manifests         |
| `zip`                 | nixpkgs stable | Package dist-chrome/ and dist-firefox/ into .zip files             |
| `httpie`              | nixpkgs stable | HTTP client for manual testing                                     |
| `nodePackages.rollup` | nixpkgs-master | Explicit rollup binary (falls back to `npx rollup` if unavailable) |

### Updated: top-level `flake.nix`

- Add input: `devenv-browser_extension.url = "path:./devenvs/browser_extension"`
- Replace `devenv-node` → `devenv-browser_extension` in `inputsFrom`
- Remove `jq`, `web-ext`, `httpie` from direct `packages` list
- Retain `fish`, `gnumake`, `just` as direct packages (project-level, not devenv-specific)

## Files Changed

1. `devenvs/browser_extension/flake.nix` — new
2. `flake.nix` — updated inputs and packages

## Rollback

Single `git revert` covering both files, plus `rm -rf devenvs/browser_extension/`.

## Promotion Path

Once the devenv stabilizes, move `devenvs/browser_extension/flake.nix` to
`purse-first/devenvs/browser_extension/` and update chrest's flake input from
`path:./devenvs/browser_extension` to
`github:amarbel-llc/purse-first?dir=devenvs/browser_extension`.

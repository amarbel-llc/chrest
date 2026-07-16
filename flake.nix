{
  inputs = {
    igloo = {
      url = "git+https://code.linenisgreat.com/igloo.git";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.treefmt-nix.follows = "treefmt-nix";
      inputs.bun2nix.follows = "bun2nix";
      inputs.systems.follows = "bun2nix/systems";
    };
    nixpkgs-master.url = "github:NixOS/nixpkgs/567a49d1913ce81ac6e9582e3553dd90a955875f";
    utils = {
      url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
      inputs.systems.follows = "bun2nix/systems";
    };

    bun2nix = {
      url = "github:nix-community/bun2nix";
      inputs.nixpkgs.follows = "igloo";
      inputs.treefmt-nix.follows = "treefmt-nix";
      # Force bun2nix's flake-parts onto nixpkgs's rev so the lock
      # carries only one flake-parts revision (chrest#87).
      inputs.flake-parts.follows = "igloo/flake-parts";
    };

    # `nix fmt` driver. Config lives in ./treefmt.nix. The sandboxed
    # check derivation surfaces as `checks.<system>.treefmt` and is
    # what `just lint-fmt` builds.
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "igloo";
    };

    tommy = {
      url = "git+https://code.linenisgreat.com/tommy.git";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
      inputs.tap.follows = "tap";
      inputs.bats.follows = "bats";
    };

    # amarbel-llc/bats provides the `batman` bundle (wrapped bats + the
    # bats-* helper libs `common.bash` calls via `bats_load_library`).
    # The fork's bats does NOT accept `--bin-dir`; tests find binaries
    # by env var (`CHREST_BIN`, etc.) instead.
    bats = {
      url = "git+https://code.linenisgreat.com/bats.git";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
      inputs.treefmt-nix.follows = "treefmt-nix";
    };

    # Consumed via goFlakeInputs (./go/gomod.nix). A tap bump only
    # touches flake.lock — no go.mod / gomod2nix.toml lockstep edits.
    # See amarbel-llc/chrest#84 and amarbel-llc/nixpkgs RFC 0001.
    tap = {
      url = "git+https://code.linenisgreat.com/tap.git";
      inputs.bats.follows = "bats";
      inputs.gomod2nix.follows = "purse-first/gomod2nix";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.purse-first.follows = "purse-first";
      inputs.treefmt-nix.follows = "treefmt-nix";
      inputs.utils.follows = "utils";
    };

    # Consumed via goFlakeInputs for libs/dewey and libs/go-mcp.
    purse-first = {
      url = "git+https://code.linenisgreat.com/purse-first.git";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # Provides `doppelgang lint`; flake.lock dedup gate (chrest#87).
    doppelgang = {
      url = "git+https://code.linenisgreat.com/doppelgang.git";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # Consumed via goFlakeInputs (./go/gomod.nix) for pkgs/capture_plugin
    # and pkgs/capture_serve (chrest#83, chrest#98). Sourced from the
    # forge, not GitHub — cutting-garden's canonical remote moved off
    # GitHub (the amarbel-llc/cutting-garden mirror is archived, frozen
    # at v0.1.24) to a self-hosted Forgejo instance; the bridge fetches
    # over SSH at eval time, bypassing GOPROXY entirely, which is why
    # this can see commits (pkgs/capture_serve) the frozen mirror can't.
    # `follows` names verified against cutting-garden's own flake.nix
    # (it calls the flake-utils input `flake-utils`, not `utils`).
    cutting-garden = {
      url = "git+https://code.linenisgreat.com/cutting-garden.git";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.flake-utils.follows = "utils";
      inputs.tap.follows = "tap";
      inputs.purse-first.follows = "purse-first";
      inputs.bats.follows = "bats";
      inputs.tommy.follows = "tommy";
    };
    cutting-garden.inputs.madder.inputs.piggy.follows = "cutting-garden/piggy";
    # Remaining multi-version dedups doppelgang lint --fix reported but
    # didn't auto-collapse (chrest#87): cutting-garden's own transitive
    # chain pins conformist/doppelgang/tommy separately from chrest's
    # existing (purse-first-, and chrest's own top-level) pins of the
    # same flakes.
    cutting-garden.inputs.conformist.follows = "purse-first/conformist";
    cutting-garden.inputs.hyphence.inputs.doppelgang.follows = "doppelgang";
    cutting-garden.inputs.madder.inputs.tommy.follows = "tommy";
    purse-first.inputs.conformist.follows = "doppelgang/conformist";
    tommy.inputs.conformist.follows = "doppelgang/conformist";
    # Top-level alias so conformist.lib.evalModule is accessible in outputs
    # without a new lock node — follows the same doppelgang/conformist node
    # that purse-first and tommy already pin.
    conformist.follows = "doppelgang/conformist";
  };

  outputs =
    {
      self,
      igloo,
      nixpkgs-master,
      utils,
      bun2nix,
      treefmt-nix,
      tommy,
      bats,
      tap,
      purse-first,
      doppelgang,
      cutting-garden,
      conformist,
    }:
    let
      # Single source of truth for the release version. Burnt into:
      #   * Go binary  — via -X main.version (auto-injected by the
      #     amarbel-llc/nixpkgs fork's buildGoApplication when `version`
      #     is passed).
      #   * MCP serverInfo.version — Go binary surfaces `app.Version`
      #     as the MCP server version.
      #   * Extension manifest.version — templated into manifest.json
      #     at extension/default.nix build-time.
      # `just bump-version` sed-rewrites this line; `just deploy-tag`
      # pushes both `vX.Y.Z` (project-level canonical) and
      # `go/vX.Y.Z` (path-prefix tag preserved for downstream Go
      # module consumers, e.g. dodder).
      chrestVersion = "0.3.1";
      # shortRev for clean builds, dirtyShortRev for dirty working
      # trees, "unknown" as a last-resort fallback.
      chrestCommit = self.shortRev or self.dirtyShortRev or "unknown";
      # Dev marker per chrest#61 acceptance: clean release builds
      # report the bare chrestVersion ("0.2.6"); dirty / non-tag
      # builds report "<version>-dev+<shortSha>". The Go binary and
      # MCP serverInfo carry the marker; the extension manifest does
      # NOT — browser stores require numeric-only semver, so the
      # extension always reports the bare chrestVersion.
      chrestVersionFull =
        if self ? shortRev then chrestVersion else "${chrestVersion}-dev+${chrestCommit}";
    in
    (utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import igloo {
          inherit system;
          overlays = [
            igloo.overlays.default
          ];
        };
        firefox = pkgs.callPackage ./nix/firefox.nix { };
        pkgs-master = import nixpkgs-master {
          inherit system;
          overlays = [
            (final: prev: {
              web-ext = prev.buildNpmPackage rec {
                pname = "web-ext";
                version = "10.1.0";
                src = prev.fetchFromGitHub {
                  owner = "mozilla";
                  repo = "web-ext";
                  rev = version;
                  hash = "sha256-iyhiMX8Qey2VdjIxQnU/YVN3XGwK3uE0JXOV//6dbAc=";
                };
                npmDepsHash = "sha256-z6bE1j8EuEIYKi6bRkAX6KULVShUoXMOQStBX+1QNqk=";
                npmBuildFlags = [ "--production" ];
                passthru.tests.help = prev.runCommand "${pname}-tests" { } ''
                  ${final.web-ext}/bin/web-ext --help
                  touch $out
                '';
                meta = {
                  description = "Command line tool to help build, run, and test web extensions";
                  homepage = "https://github.com/mozilla/web-ext";
                  license = prev.lib.licenses.mpl20;
                  mainProgram = "web-ext";
                };
              };
            })
          ];
        };
        # flake-input-go_mod consumer-half bridge. See go/gomod.nix and
        # amarbel-llc/nixpkgs RFC 0001. Threaded into buildGoApplication
        # below; any future buildGoApplication / mkGoEnv call site must
        # also receive it or it silently falls back to organic
        # gomod2nix.toml resolution and resurrects the lockstep
        # regression (chrest#84).
        goFlakeInputs = import ./go/gomod.nix {
          inherit
            tap
            tommy
            purse-first
            cutting-garden
            system
            ;
        };

        chrest = pkgs.buildGoApplication {
          pname = "chrest";
          version = chrestVersionFull;
          commit = chrestCommit;
          src = ./go;
          subPackages = [
            "cmd/chrest"
            "cmd/chrest-server"
            "cmd/chrest-jcs"
          ];
          modules = ./go/gomod2nix.toml;
          inherit goFlakeInputs;
          go = pkgs.go_1_26;
          GOTOOLCHAIN = "local";
          nativeBuildInputs = [ pkgs.makeWrapper ];
          checkPhase = ''
            runHook preCheck
            # pdfcpu writes config to $HOME on first call; the nix
            # sandbox's default $HOME (/homeless-shelter) is read-only,
            # so capturebatch's PDF normalization tests fail without
            # this. $TMPDIR is the per-build writable scratch dir.
            export HOME=$TMPDIR
            # No -tags test: the only //go:build test file
            # (charlie/browser_items/item_test.go) references a
            # ui.T type that was never vendored across from dewey
            # upstream, so it does not compile under -tags test. The
            # file is marked "// TODO fix this test" and is silently
            # skipped by `just test-go` today (no tag passed). Matches
            # current behavior.
            go test -p $NIX_BUILD_CORES ./...
            runHook postCheck
          '';
          postInstall = ''
            $out/bin/chrest generate-plugin $out
            cat > $out/share/purse-first/chrest/clown.json <<'JSON'
            {
              "version": 1,
              "stdioServers": {
                "chrest": {
                  "command": "chrest",
                  "args": ["mcp"]
                }
              }
            }
            JSON
          '';
          postFixup =
            let
              monolithBinPath = "${pkgs.monolith}/bin";
            in
            ''
              wrapProgram $out/bin/chrest \
                --prefix PATH : ${firefox}/bin:${monolithBinPath}
              ln -s ${firefox}/bin/firefox $out/bin/firefox
            '';
        };
        extension =
          browserType:
          pkgs.callPackage ./extension/default.nix {
            inherit browserType;
            version = chrestVersion;
          };

        # The stable dagnabit from the current purse-first rev.  Used directly
        # in the devShell (see packages list below) and as the fast-path
        # fallback in conformistDagnabit for commits that don't touch
        # flake.lock.
        dagnabitForHook = purse-first.packages.${system}.dagnabit;

        # Hook-time dagnabit wrapper (chrest#106).
        #
        # Root cause: the repair/check scripts bake in the dagnabit store
        # path at derivation eval time (the old purse-first rev).  When
        # flake.lock is staged for a purse-first bump, those scripts call
        # OLD dagnabit → it produces byte-identical facades → the lane
        # stages nothing → the merge gate's fresh nix build sees version-
        # stamp drift and fails.
        #
        # Fix: intercept `dagnabit` calls at hook time.  When flake.lock is
        # staged, call `nix build <project>#dagnabit` to realise the dagnabit
        # derivation from the *on-disk* (staged) lock and exec that binary
        # instead.  The nix store already has the new derivation after
        # `nix flake update`, so the build is typically a store-path lookup
        # (instant).  Falls back to the pre-built binary on any failure so
        # ordinary commits stay fast and unaffected.
        #
        # Remove conformistDagnabit, packages.conformist-dagnabit, and
        # CONFORMIST_DAGNABIT_BIN in the justfile once purse-first's conformist
        # module exposes a dagnabitCommand string option (evaluated at hook
        # time rather than baked in as a store path at evalModule time).
        conformistDagnabit = pkgs.writeShellScriptBin "dagnabit" ''
          set -eu
          if git diff --cached --name-only 2>/dev/null | grep -qF 'flake.lock'; then
            project_root=$(git rev-parse --show-toplevel 2>/dev/null || true)
            new_bin=''${project_root:+$(nix build "$project_root#dagnabit" --no-link --print-out-paths 2>/dev/null || true)}
            [ -n "$new_bin" ] && exec "$new_bin/bin/dagnabit" "$@"
          fi
          exec ${dagnabitForHook}/bin/dagnabit "$@"
        '';

        # Per-commit facade-repair eval (chrest#105). Carries only the
        # dewey-facade-export repair lane (no formatter programs — treefmt-nix
        # owns chrest's formatting; the facade-format pass uses the on-disk
        # ./conformist.toml via DAGNABIT_CONFORMIST_CONFIG). build.preCommit
        # from this eval is named conformist-pre-commit in packages and on
        # the devShell PATH; the sweatfile [hooks].pre-commit command
        # references it by that name so a commit that touches flake.lock or
        # any go/**/*.go automatically regenerates and stages the pkgs/
        # facades (stage-mutation tiers 2–4).
        conformistCodegenEval = conformist.lib.evalModule pkgs {
          imports = [
            purse-first.lib.conformistLinters.dewey-facade-export
          ];
          package = conformist.packages.${system}.default;
          linters.dewey-facade-export.enable = true;
          linters.dewey-facade-export.deweyDir = "go";
          linters.dewey-facade-export.library = false;
          # chrest#106: use the hook-time wrapper so purse-first bump commits
          # regenerate from the staged lock, not the pre-built binary.
          linters.dewey-facade-export.dagnabitPackage = conformistDagnabit;
          # ./conformist.toml is the repo's on-disk formatter config; passing
          # it as a Nix path copies it to the store so dagnabit's
          # facade-format pass can run `conformist --config-file <store-path>`
          # without an upward walk that might escape the repo root.
          linters.dewey-facade-export.conformistConfig = ./conformist.toml;
          settings.linter.dewey-facade-export = {
            # flake.lock added to the module's default go/**/*.go trigger so a
            # purse-first bump commit (flake.lock only, no *.go staged) still
            # fires the lane — facades embed dagnabit's version stamp.
            includes = [ "flake.lock" ];
            "restage-repair-outputs" = true; # tier 2: restage modified facades
            "stage-new-outputs" = true; # tier 3: stage a brand-new pkgs/ facade
            "stage-deleted-outputs" = true; # tier 4: stage a removed/relocated facade
          };
        };

        # `nix fmt` entry point. Config lives in ./treefmt.nix.
        treefmtEval = treefmt-nix.lib.evalModule pkgs ./treefmt.nix;
      in
      {
        packages.chrest = chrest;
        packages.default = chrest;
        packages.extension-chrome = extension "chrome";
        packages.extension-firefox = extension "firefox";
        # Toolchain-hermetic per-commit facade-repair hook (chrest#105).
        # Named by the sweatfile [hooks].pre-commit command and put on the
        # devShell PATH as `conformist-pre-commit`. `nix build
        # .#conformist-pre-commit` dogfoods the codegen eval + facade lane.
        packages.conformist-pre-commit = conformistCodegenEval.config.build.preCommit;
        # Re-export purse-first's dagnabit so `nix build .#dagnabit` resolves
        # it from the project's lock — used by the conformistDagnabit wrapper
        # at hook time to realise the post-bump dagnabit (chrest#106).
        packages.dagnabit = dagnabitForHook;
        # The hook-time dagnabit wrapper itself, exposed for BATS testing
        # (CONFORMIST_DAGNABIT_BIN in test-mcp-bats — chrest#106).
        packages.conformist-dagnabit = conformistDagnabit;

        apps.default = {
          type = "app";
          program = "${chrest}/bin/chrest";
        };

        formatter = treefmtEval.config.build.wrapper;
        # Sandboxed treefmt check for `just lint-fmt` and `nix flake
        # check`. Runs formatters over a /nix/store snapshot of the
        # source tree and exits non-zero on drift — no working-tree
        # side effects, unlike `nix fmt`.
        checks.treefmt = treefmtEval.config.build.check self;

        # `checks.all-systems-eval` previously forced evaluation of every
        # supported system's devShell + package .drvPath from the host's
        # checks, as a pre-flakehub-push guard against malformed fixed-
        # output hashes (chrest#50). Removed because evaluating
        # `packages.aarch64-linux.default.drvPath` triggered a build of
        # `source-go-pkgs-test.drv` for the foreign system — an IFD that
        # can't be realised on an x86_64-linux host without binfmt/QEMU,
        # so it broke `nix flake check --no-build` whenever the working
        # tree was dirty (e.g. mid-`update-nix-repos` cascade) and the
        # IFD output wasn't already substituted locally. See the
        # tracking task in the worktree for the follow-up investigation
        # into the IFD root cause; the cross-system hash safety net
        # should be re-added once it can be expressed without IFDs into
        # foreign-system builds.

        devShells.default = pkgs-master.mkShell {
          packages = [
            tommy.packages.${system}.default
            bun2nix.packages.${system}.default
          ]
          ++ (with pkgs; [
            bun
            fish
            gnumake
            jq
            just
            nodejs_latest
            poppler-utils
            unixtools.xxd
            zip
          ])
          ++ [
            firefox
            pkgs.monolith
            # amarbel-llc/bats wrapped-bats binary (fence-sandboxed,
            # tap-dancer NDJSON pipeline). Sibling `batman` orchestrator
            # is in the same flake at `bats.packages.${system}.batman`
            # but unused here. BATS_LIB_PATH is set in shellHook below.
            bats.packages.${system}.bats
          ]
          ++ [
            # Use the fork-pinned go_1_26 (currently 1.26.3 per
            # amarbel-llc/nixpkgs#27, addressing GO-2026-4971 +
            # GO-2026-4918). pkgs-master ships bare 1.26.2; the
            # pin lives in the fork's overlay, which `pkgs` here
            # applies. Mismatch between devshell-go and prod-build-go
            # is the kind of vendor-env drift that validate-devshell
            # also guards.
            pkgs.go_1_26
          ]
          ++ (with pkgs-master; [
            delve
            gofumpt
            golangci-lint
            golines
            gopls
            gotools
            govulncheck
            httpie
            bash-language-server
            parallel
            shellcheck
            shfmt
            web-ext
          ])
          ++ [
            pkgs.gomod2nix
            # `doppelgang lint --flake .` runs in the `lint` aggregate
            # as a flake.lock dedup gate (chrest#87).
            doppelgang.packages.${system}.default
            # Pinned `dagnabit` for build-dagnabit-export +
            # validate-dagnabit-export + codemod-dagnabit-reposition.
            # Previously the justfile did `nix run github:.../#dagnabit`
            # which followed purse-first HEAD and surfaced upstream
            # emitter-format drift on unrelated PRs (chrest#90).
            dagnabitForHook
            # Per-commit facade-repair hook (chrest#105). Placed on PATH as
            # `conformist-pre-commit`; spinclass installs it as a git pre-commit
            # hook at session start/resume so a session restart is needed after
            # this lands.
            conformistCodegenEval.config.build.preCommit
          ];

          # Passthru: use the outer-shell git (user's nix profile, NixOS
          # system path, or distro). Respects the user's gitconfig,
          # signing keys, and hooks, and keeps `git` behavior identical
          # inside and outside the devshell. Without this, any recipe
          # that shells out to `git` under `nix develop --command` fails
          # with `git: command not found`.
          #
          # Only prepends the single directory the located git lives in
          # — avoids polluting PATH with /usr/bin wholesale.
          shellHook = ''
            if ! command -v git >/dev/null 2>&1; then
              for candidate in \
                "$HOME/.nix-profile/bin/git" \
                /run/current-system/sw/bin/git \
                /etc/profiles/per-user/"$USER"/bin/git \
                /usr/bin/git \
                /bin/git; do
                if [ -x "$candidate" ]; then
                  export PATH="$(dirname "$candidate"):$PATH"
                  break
                fi
              done
            fi
            # bats_load_library bats-assert (etc.) in common.bash
            # needs BATS_LIB_PATH to point at the amarbel-llc/bats
            # bats-libs path.
            export BATS_LIB_PATH="${
              bats.packages.${system}.bats-libs.batsLibPath
            }''${BATS_LIB_PATH:+:}''${BATS_LIB_PATH:-}"
            # code.linenisgreat.com now serves go-import meta
            # (linenisgreat#64) for repos migrated off GitHub to the
            # self-hosted Forgejo forge — currently just cutting-garden's
            # fork-replace (go/go.mod: `replace
            # github.com/amarbel-llc/cutting-garden =>
            # code.linenisgreat.com/cutting-garden <pseudo-version>`).
            # GOPRIVATE skips GOPROXY + GOSUMDB for that host so plain `go`
            # tooling (go build, go test, go vet, dagnabit) resolves it —
            # the nix build resolves it independently via the
            # flake-input-go_mod bridge (go/gomod.nix), which needs no env
            # var. Drop this once cutting-garden's own module path
            # migrates to code.linenisgreat.com/cutting-garden and the
            # fork-replace becomes a plain require bump.
            export GOPRIVATE="code.linenisgreat.com''${GOPRIVATE:+,$GOPRIVATE}"
          '';
        };
      }
    ));
}

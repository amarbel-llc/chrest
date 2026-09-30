{
  inputs = {
    igloo = {
      url = "https://code.linenisgreat.com/igloo/archive/master.tar.gz";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.bun2nix.follows = "bun2nix";
      inputs.systems.follows = "bun2nix/systems";
    };
    nixpkgs-master.url = "github:NixOS/nixpkgs/b4fd65b198c599cbe814fcb9f42d25d021595ec9";
    utils = {
      url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";
      inputs.systems.follows = "bun2nix/systems";
    };

    bun2nix = {
      url = "github:nix-community/bun2nix";
      inputs.nixpkgs.follows = "igloo";
      # chrest no longer takes its own treefmt-nix input (conformist owns
      # formatting, eng#246); collapse bun2nix's and tap's treefmt-nix onto
      # igloo's node so the lock keeps ONE treefmt-nix revision (chrest#87).
      inputs.treefmt-nix.follows = "igloo/treefmt-nix";
      # Force bun2nix's flake-parts onto nixpkgs's rev so the lock
      # carries only one flake-parts revision (chrest#87).
      inputs.flake-parts.follows = "igloo/flake-parts";
    };

    tommy = {
      url = "https://code.linenisgreat.com/tommy/archive/master.tar.gz";
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
      url = "https://code.linenisgreat.com/bats/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # A go/go.nix flakeInputs bridge (igloo FDR 0008, RFC 0001). A tap
    # bump only touches flake.lock (chrest#84).
    tap = {
      url = "https://code.linenisgreat.com/tap/archive/master.tar.gz";
      inputs.bats.follows = "bats";
      inputs.gomod2nix.follows = "purse-first/gomod2nix";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.purse-first.follows = "purse-first";
      inputs.treefmt-nix.follows = "igloo/treefmt-nix";
      inputs.utils.follows = "utils";
    };

    # go/go.nix flakeInputs bridges for libs/dewey and libs/go-mcp; also
    # provides dagnabit.
    purse-first = {
      url = "https://code.linenisgreat.com/purse-first/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # Provides `doppelgang lint`; flake.lock dedup gate (chrest#87).
    doppelgang = {
      url = "https://code.linenisgreat.com/doppelgang/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # Declared directly rather than aliased into a dependency's node. chrest
    # consumes conformist as a nix module (conformist.lib.evalModule, see
    # conformist.nix), so the option set this flake evaluates against is part
    # of chrest's own contract and must not be decided by a dependency.
    #
    # This wiring used to terminate at `bats/conformist`. A `follows` alias
    # into a dependency's input resolves to that dependency's LOCKED rev, not
    # to the URL it declares -- so even though bats declares conformist as
    # master.tar.gz, chrest actually inherited whatever rev bats' flake.lock
    # last pinned. bats' lock sat ~38 days behind chrest's, so a plain
    # `nix flake update` walked chrest's conformist BACKWARDS onto a rev
    # predating `linters.git-merge-drivers` and broke eval with "The option
    # 'linters.git-merge-drivers' does not exist".
    #
    # Owning the node means chrest resolves master itself at lock time, so the
    # rev only moves forward. Every consumer below collapses onto this single
    # node, keeping the chrest#87 dedup gate (`just lint-doppelgang`) green.
    conformist = {
      url = "https://code.linenisgreat.com/conformist/archive/master.tar.gz";
      inputs.igloo.follows = "igloo";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # A go/go.nix flakeInputs bridge for pkgs/capture_plugin and
    # pkgs/capture_serve (chrest#83, chrest#98). Sourced from the
    # forge, not GitHub — cutting-garden's canonical remote moved off
    # GitHub (the amarbel-llc/cutting-garden mirror is archived, frozen
    # at v0.1.24) to a self-hosted Forgejo instance; the bridge fetches
    # over SSH at eval time, bypassing GOPROXY entirely, which is why
    # this can see commits (pkgs/capture_serve) the frozen mirror can't.
    # `follows` names verified against cutting-garden's own flake.nix
    # (it calls the flake-utils input `flake-utils`, not `utils`).
    cutting-garden = {
      url = "https://code.linenisgreat.com/cutting-garden/archive/master.tar.gz";
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
    cutting-garden.inputs.conformist.follows = "conformist";
    cutting-garden.inputs.hyphence.inputs.doppelgang.follows = "doppelgang";
    # Duplicate-langlang lock node (go-module-rename playbook wave-2
    # gotcha): hyphence's rename bump (2026-07-20 leg) introduced its own
    # langlang subtree alongside cutting-garden's pre-existing direct
    # langlang input. doppelgang lint's recommended collapse.
    cutting-garden.inputs.hyphence.inputs.langlang.follows = "cutting-garden/langlang";
    cutting-garden.inputs.madder.inputs.tommy.follows = "tommy";
    # Collapse every dependency's conformist onto chrest's own node (above),
    # rather than chaining them through each other. The old chain ran
    # conformist -> doppelgang/conformist -> bats/conformist, which handed
    # the revision to bats; these all now terminate at the node this flake
    # declares and controls.
    purse-first.inputs.conformist.follows = "conformist";
    tommy.inputs.conformist.follows = "conformist";
    doppelgang.inputs.conformist.follows = "conformist";
    bats.inputs.conformist.follows = "conformist";
  };

  # `inputs@`: buildGoAuto / mkGoPkgs resolve go/go.nix's flakeInputs entries
  # by name against the whole inputs set (igloo FDR 0008).
  outputs =
    inputs@{
      self,
      igloo,
      nixpkgs-master,
      utils,
      bun2nix,
      tommy,
      bats,
      tap,
      purse-first,
      doppelgang,
      cutting-garden,
      conformist,
    }:
    let
      # version.env at repo root is the single source of truth for the
      # release version (eng-versioning(7)). Match expression captures
      # everything after `CHREST_VERSION=` up to the line break; the
      # `export` prefix is tolerated. Burnt into:
      #   * Go binary  — via -X main.version (injected by buildGoAuto,
      #     on both the godyn and bga backends, from `version`).
      #   * MCP serverInfo.version — Go binary surfaces `app.Version`
      #     as the MCP server version.
      #   * Extension manifest.version — templated into manifest.json
      #     at extension/default.nix build-time.
      # `just bump-version` sed-rewrites version.env; `just deploy-tag`
      # pushes both `vX.Y.Z` (project-level canonical) and
      # `go/vX.Y.Z` (path-prefix tag preserved for downstream Go
      # module consumers, e.g. dodder).
      #
      # `version` is passed explicitly to buildGoAuto (chrestVersionFull
      # below; igloo#70), fed from this parse rather than a literal.
      chrestVersion = builtins.head (
        builtins.match ".*CHREST_VERSION=([^\n]+).*" (builtins.readFile ./version.env)
      );
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
        # Go dependencies live in go/go.nix (igloo FDR 0008): go.mod, go.sum
        # and gomod2nix.toml are rendered inside nix, never committed, and
        # the fleet modules (cutting-garden, dewey, go-mcp, tap, tommy) are
        # its flakeInputs bridges (RFC 0001). There is no ambient `go` in the
        # devshell, so go never fetches modules outside nix — in particular
        # never from a git hook, where an inherited GIT_DIR turned cmd/go's
        # `git init --bare` into a re-init of chrest itself (spinclass#311).
        #
        # Producer half (RFC 0001): go-pkgs carry the rendered go.mod +
        # gomod2nix.toml under go/, so a downstream Go consumer (dodder
        # requires code.linenisgreat.com/chrest/go) bridges chrest with
        # subPath "go" instead of a versioned require, which stops resolving
        # once the published tree has no committed go.mod.
        goPkgs = pkgs.mkGoPkgs {
          src = self;
          manifest = ./go/go.nix;
          subPath = "go";
          name = "chrest";
          inherit inputs;
        };

        # Generators on PATH in every godyn-go run and codegen check: dagnabit
        # with its post-generation conformist pass pinned to this repo's
        # generated PURE config (no conformist.toml on disk; the raw
        # conformist binary because dagnabit passes --tree-root, which
        # collides with the module wrapper's --tree-root-file,
        # purse-first#159), and tommy for config_toml's `//go:generate`.
        dagnabitPinned =
          pkgs.runCommand "dagnabit-pinned"
            {
              nativeBuildInputs = [ pkgs.makeWrapper ];
              meta.mainProgram = "dagnabit";
            }
            ''
              makeWrapper ${pkgs.lib.getExe' purse-first.packages.${system}.dagnabit "dagnabit"} \
                $out/bin/dagnabit \
                --set DAGNABIT_CONFORMIST_CONFIG ${conformistEval.config.build.configFile} \
                --prefix PATH : ${pkgs.lib.makeBinPath [ conformist.packages.${system}.default ]}
            '';
        codegenTools = [
          dagnabitPinned
          tommy.packages.${system}.default
        ];

        # godyn (per-package) on igloo's godynSystems, buildGoApplication
        # elsewhere; both reachable as passthru.native / passthru.bga, and
        # the checks below key off passthru.backend. Built from the published
        # go-pkgs-test (self-consumption: what consumers bridge is what the
        # binaries and tests are built from).
        chrest = pkgs.buildGoAuto {
          pname = "chrest";
          version = chrestVersionFull;
          src = goPkgs.go-pkgs-test + "/go";
          manifest = ./go/go.nix;
          inherit inputs;
          subPackages = [
            "cmd/chrest"
            "cmd/chrest-server"
            "cmd/chrest-jcs"
          ];
          # On PATH in every godyn-go run (`just build-dagnabit-export`, and
          # firefox for the `-tags spike` BiDi tests the explore-bidi-*
          # recipes run).
          goRunInputs = codegenTools ++ [ firefox ];
          # godyn's per-package test lane (passthru.checkAll). No -tags test:
          # the only //go:build test file (charlie/browser_items/item_test.go)
          # references a ui.T type that was never vendored across from dewey
          # upstream and does not compile under it ("// TODO fix this test").
          tests = true;
          # pdfcpu writes config to $HOME on first call; the sandbox's $HOME
          # (/homeless-shelter) is read-only, so capturebatch's PDF
          # normalization tests fail without a writable one.
          testPreRun = ''
            export HOME="$TMPDIR"
          '';
          nativeArgs = {
            commit = chrestCommit;
            # github.com/DataDog/zstd (madder, via cutting-garden) is cgo-only.
            inherit (pkgs.stdenv) cc;
          };
          bgaArgs = {
            commit = chrestCommit;
            go = pkgs.go_1_26;
            GOTOOLCHAIN = "local";
            preCheck = ''
              export HOME=$TMPDIR
            '';
          };
          nativeBuildInputs = [ pkgs.makeWrapper ];
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
            # Wrapped after generate-plugin ran the bare binary. In
            # postInstall (not postFixup): buildGoAuto declares the install
            # step once for both backends and has no fixup hook.
            wrapProgram $out/bin/chrest \
              --prefix PATH : ${firefox}/bin:${pkgs.monolith}/bin
            ln -s ${firefox}/bin/firefox $out/bin/firefox
          '';
        };
        extension =
          browserType:
          pkgs.callPackage ./extension/default.nix {
            inherit browserType;
            version = chrestVersion;
          };

        # Pure lane (eng#246 item 2): the eng preset (sandboxed eng-convention
        # linters) + this repo's formatters/excludes from ./conformist.nix.
        # Drives `nix fmt` (build.wrapper), the sandboxed `checks.formatting`
        # (build.check), and the generated config (build.configFile) that the
        # facade lane and the justfile's dagnabit recipes bake in via
        # DAGNABIT_CONFORMIST_CONFIG. Replaces the retired treefmt-nix
        # (./treefmt.nix) and the hand-written ./conformist.toml shadow config.
        # See conformist-nix(7) and the cutting-garden / piggy flakes.
        conformistEval = conformist.lib.evalModule pkgs {
          imports = [
            conformist.lib.presets.eng
            ./conformist.nix
          ];
          package = conformist.packages.${system}.default;
        };

        # Dedicated PRE-COMMIT/REPAIR eval: the repo's formatters/excludes
        # from ./conformist.nix, deliberately NOT presets.eng (its convention
        # linters stay at the merge gate, not commit/repair time).
        # build.preCommit is conformist-pre-commit (the sweatfile
        # [hooks].pre-commit), build.repair its merge-repair sibling.
        #
        # Formatting only since go.nix. The dewey-facade-export repair lane
        # (chrest#105) and its hook-time dagnabit wrapper (chrest#106, which
        # re-built dagnabit from a staged flake.lock) are gone: dagnabit
        # type-loads packages through a checkout go.mod, which go.nix
        # removed, and it was the one path by which a commit ran `go` inside
        # the hook (spinclass#311). Facade drift — including the version
        # stamp a purse-first bump moves — is the pure checks.dagnabit-codegen
        # merge gate instead, regenerated with `just build-dagnabit-export`.
        # Same shape as cutting-garden and nebulous.
        conformistCodegenEval = conformist.lib.evalModule pkgs {
          imports = [ ./conformist.nix ];
          package = conformist.packages.${system}.default;
        };

        # godyn-backed checks exist only where buildGoAuto chose godyn.
        onGodyn = chrest.passthru.backend == "native";
      in
      {
        packages.chrest = chrest;
        packages.default = chrest;
        packages.extension-chrome = extension "chrome";
        packages.extension-firefox = extension "firefox";
        # RFC 0001 producer outputs (go/go.nix rendered at go-pkgs/go/) for
        # downstream Go consumers that bridge chrest (dodder).
        packages.go-pkgs = goPkgs.go-pkgs;
        packages.go-pkgs-test = goPkgs.go-pkgs-test;
        # Toolchain-hermetic per-commit format hook, named by the sweatfile
        # [hooks].pre-commit command and on the devShell PATH as
        # `conformist-pre-commit`.
        packages.conformist-pre-commit = conformistCodegenEval.config.build.preCommit;
        # The merge-repair hook (build.repair, `--commit --amend`) from the
        # codegen eval, on the devShell PATH below as `conformist-repair` so
        # the eng sweatfile's [hooks].repair resolves the hermetic,
        # this-config hook instead of eng's cwd-aware fallback wrapper —
        # which, with no root conformist.toml left on disk, would format
        # chrest with ENG's catch-all config and re-group the dagnabit pkgs/
        # facades (the exact failure the old shadow conformist.toml existed
        # to prevent; see cutting-garden's conformist-repair comment).
        packages.conformist-repair = conformistCodegenEval.config.build.repair;

        apps.default = {
          type = "app";
          program = "${chrest}/bin/chrest";
        };

        # `nix fmt` runs the generated conformist wrapper (config + every
        # formatter baked as /nix/store paths). See conformistEval.
        formatter = conformistEval.config.build.wrapper;
        # Sandboxed conformist check for `just lint-fmt` and `nix flake
        # check`. Runs formatters (verify mode) + the eng preset's
        # file-based linters over a /nix/store snapshot of the source tree
        # and exits non-zero on drift — no working-tree side effects,
        # unlike `nix fmt`.
        checks = {
          formatting = conformistEval.config.build.check self;
        }
        # godyn lanes from go/go.nix, only where buildGoAuto chose godyn
        # (elsewhere the bga backend's checkPhase runs `go test`). Since
        # go.nix there is no checkout go.mod for dagnabit to type-load
        # through, so its two drift gates run as passthru.codegenCheck: the
        # command runs in the vendored module tree (dagnabitPinned on PATH)
        # and the check fails on any diff from the committed source.
        // pkgs.lib.optionalAttrs onGodyn {
          # Per-package `go test` (`just test-go`).
          chrest-tests = chrest.passthru.checkAll;
          # The pkgs/ facades must be what `dagnabit export` emits now —
          # including the dagnabit version stamp a purse-first bump moves
          # (`just validate-dagnabit-export`; regenerate with
          # `just build-dagnabit-export`).
          dagnabit-codegen = chrest.passthru.codegenCheck {
            command = "go generate -run dagnabit ./... && dagnabit export -check";
            nativeBuildInputs = codegenTools;
          };
          # go/internal/<level>/<leaf> must match dagnabit's computed
          # dependency height (`just validate-dagnabit-reposition`). The
          # dry run, failing on any would-move line: applying a move needs
          # `git mv`, and the vendored tree is not a git checkout.
          dagnabit-reposition = chrest.passthru.codegenCheck {
            command = ''
              # Not `out`: that is the derivation's output path.
              moves=$(dagnabit -n internal)
              if [ -n "$moves" ]; then
                echo "$moves"
                echo "FAIL: dagnabit reposition would move packages (above); move them by hand to the tier shown." >&2
                exit 1
              fi
            '';
            nativeBuildInputs = codegenTools;
          };
        };

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
            # Go: no ambient `go` (igloo FDR 0007/0008). Dependencies live in
            # go/go.nix; go commands (`go get`, `go mod tidy`, `go generate`)
            # run inside nix through godyn-go, single-package tests through
            # godyn-test. With no go on PATH, nothing in the devshell or a
            # git hook can fetch modules (spinclass#311).
            pkgs.godyn-go
            pkgs.godyn-test
          ]
          ++ (with pkgs-master; [
            httpie
            bash-language-server
            parallel
            shellcheck
            shfmt
            web-ext
          ])
          ++ [
            # `doppelgang lint --flake .` runs in the `lint` aggregate
            # as a flake.lock dedup gate (chrest#87).
            doppelgang.packages.${system}.default
            # Per-commit format hook, on PATH as `conformist-pre-commit`;
            # spinclass installs it as a git pre-commit hook at session
            # start/resume. (dagnabit lives in godyn-go's goRunInputs, not
            # here: it needs a go.mod, which only the rendered module has.)
            conformistCodegenEval.config.build.preCommit
            # Its merge-repair sibling, on PATH as `conformist-repair` for
            # spinclass's [hooks].repair (see packages.conformist-repair).
            conformistCodegenEval.config.build.repair
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
          '';
        };
      }
    ));
}

{
  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";

    bun2nix = {
      url = "github:nix-community/bun2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # `nix fmt` driver. Config lives in ./treefmt.nix. The sandboxed
    # check derivation surfaces as `checks.<system>.treefmt` and is
    # what `just lint-fmt` builds.
    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    tommy = {
      url = "github:amarbel-llc/tommy";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };

    # amarbel-llc/bats provides the `batman` bundle (wrapped bats + the
    # bats-* helper libs `common.bash` calls via `bats_load_library`).
    # The fork's bats does NOT accept `--bin-dir`; tests find binaries
    # by env var (`CHREST_BIN`, etc.) instead.
    bats = {
      url = "github:amarbel-llc/bats";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-master,
      utils,
      bun2nix,
      treefmt-nix,
      tommy,
      bats,
    }:
    let
      # Burnt into the binary via the fork's auto-injected -ldflags
      # (-X main.version / -X main.commit). Single source of truth for
      # the release version; `just bump-version` sed-rewrites this line.
      chrestVersion = "0.2.6";
      # shortRev for clean builds, dirtyShortRev for dirty working trees
      # (so devshell builds show `dirty-abcdef` rather than masquerading
      # as a clean release), "unknown" as a last-resort fallback.
      chrestCommit = self.shortRev or self.dirtyShortRev or "unknown";
    in
    (utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            nixpkgs.overlays.default
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
        chrest = pkgs.buildGoApplication {
          pname = "chrest";
          version = chrestVersion;
          commit = chrestCommit;
          src = ./go;
          subPackages = [
            "cmd/chrest"
            "cmd/chrest-server"
            "cmd/chrest-jcs"
          ];
          modules = ./go/gomod2nix.toml;
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
          };

        # `nix fmt` entry point. Config lives in ./treefmt.nix.
        treefmtEval = treefmt-nix.lib.evalModule pkgs ./treefmt.nix;
      in
      {
        packages.chrest = chrest;
        packages.default = chrest;
        packages.extension-chrome = extension "chrome";
        packages.extension-firefox = extension "firefox";

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
          ++ (with pkgs-master; [
            delve
            go_1_26
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

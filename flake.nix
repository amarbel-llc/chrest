{
  inputs = {
    nixpkgs.url = "github:amarbel-llc/nixpkgs";
    nixpkgs-master.url = "github:NixOS/nixpkgs/d233902339c02a9c334e7e593de68855ad26c4cb";
    utils.url = "https://flakehub.com/f/numtide/flake-utils/0.1.102";

    bun2nix = {
      url = "github:nix-community/bun2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    bob = {
      url = "github:amarbel-llc/bob";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-master.follows = "nixpkgs-master";
      inputs.utils.follows = "utils";
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
      bob,
      tommy,
      bats,
    }:
    let
      # Burnt into the binary via the fork's auto-injected -ldflags
      # (-X main.version / -X main.commit). Single source of truth for
      # the release version; `just bump-version` sed-rewrites this line.
      chrestVersion = "0.2.1";
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
        extension = browserType: pkgs.callPackage ./extension/default.nix {
          inherit browserType;
        };
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

        # Force evaluation of devShells and packages across every supported
        # system, from the host system's checks. Catches malformed fixed-
        # output hashes on non-host platforms before they surface in
        # flakehub-push's inspect wrapper (see chrest#50). Eval-only: uses
        # builtins.seq on each system's drvPath string to trigger the
        # fixed-output-hash validation without referencing the foreign
        # drv as a build input — otherwise `nix flake check --no-build`
        # refuses to realize cross-system drvs.
        checks.all-systems-eval =
          let
            systems = [
              "x86_64-linux"
              "aarch64-linux"
              "x86_64-darwin"
              "aarch64-darwin"
            ];
            forced = builtins.deepSeq
              (map
                (sys: {
                  dev = self.devShells.${sys}.default.drvPath;
                  pkg = self.packages.${sys}.default.drvPath;
                })
                systems)
              "ok";
          in
          pkgs.runCommand "all-systems-eval-${forced}" { } ''
            touch $out
          '';

        devShells.default = pkgs-master.mkShell {
          packages = [
            bob.packages.${system}.batman
            tommy.packages.${system}.default
            bun2nix.packages.${system}.default
          ] ++ (
            with pkgs;
            [
              bun
              fish
              gnumake
              jq
              just
              nodejs_latest
              poppler-utils
              unixtools.xxd
              zip
            ]
          ) ++ [
            firefox
            pkgs.monolith
            # amarbel-llc/bats: wrapped bats + bats-* libs +
            # batman orchestrator. Replaces pkgs-master.bats (upstream),
            # which the test-mcp-bats recipe required (the fork's
            # bats dropped --bin-dir; chrest now sets CHREST_BIN env
            # var instead). BATS_LIB_PATH is set in shellHook below.
            bats.packages.${system}.default
          ] ++ (
            with pkgs-master;
            [
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
            ]
          ) ++ [
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
            export BATS_LIB_PATH="${bats.packages.${system}.bats-libs.batsLibPath}''${BATS_LIB_PATH:+:}''${BATS_LIB_PATH:-}"
          '';
        };
      }
    ));
}

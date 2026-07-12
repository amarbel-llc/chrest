# Nix interface to go.mod for chrest. Pure-consumer half of the
# flake-input-go_mod protocol (amarbel-llc/nixpkgs RFC 0001).
#
# Routes the cross-amarbel-llc go.mod `require` lines onto their
# producer flakes' go-pkgs outputs, collapsing the three-place
# lockstep (go.mod pseudo-version + gomod2nix.toml NAR hash +
# flake.lock rev) into a single source: each producer's flake.lock
# entry.
#
# Threaded through every buildGoApplication / mkGoEnv call site in
# flake.nix — a missing call site silently resurrects the lockstep
# regression class (amarbel-llc/madder#208 / #211 / #213).
#
# Tracks amarbel-llc/chrest#84 (adoption) and amarbel-llc/nixpkgs#42
# (the cross-repo adoption tracker).
{
  tap,
  tommy,
  purse-first,
  cutting-garden,
  system,
}:
{
  "github.com/amarbel-llc/tap/go" = {
    src = tap.packages.${system}.go-pkgs;
    subPath = "go";
  };
  "github.com/amarbel-llc/tommy" = {
    src = tommy.packages.${system}.go-pkgs;
  };
  "github.com/amarbel-llc/purse-first/libs/go-mcp" = {
    src = purse-first.packages.${system}.go-pkgs;
    subPath = "libs/go-mcp";
  };
  "github.com/amarbel-llc/purse-first/libs/dewey" = {
    src = purse-first.packages.${system}.go-pkgs;
    subPath = "libs/dewey";
  };
  # github.com/amarbel-llc/cutting-garden (chrest#83 pkgs/capture_plugin,
  # chrest#98 pkgs/capture_serve). Module is at the repo root, no subPath.
  # MUST be go-pkgs, not raw source: passthru.goFlakeInputs only rides the
  # mkGoPkgs output, which is what lets cutting-garden's own bridges
  # (madder, hyphence, piggy, tap, crap, tommy) inherit at depth-1 instead
  # of chrest re-declaring each one itself. Previously un-bridged because
  # igloo's mkMergedGoMod synthesized an invalid v0.0.0 sentinel for /v2+
  # transitive deps (crap/go-crap/v2) — fixed by igloo#38 (major-aware
  # sentinelFor); re-bridged once chrest's igloo pin carried that fix.
  "github.com/amarbel-llc/cutting-garden" = {
    src = cutting-garden.packages.${system}.go-pkgs;
  };
}

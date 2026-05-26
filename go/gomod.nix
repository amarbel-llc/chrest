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
}

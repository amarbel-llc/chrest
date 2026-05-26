# Fixed-output derivation for Firefox, bypassing nixpkgs (unavailable on Darwin).
#
# Darwin: universal .dmg (Apple Silicon + Intel), fetched from Mozilla CDN,
#         extracted with undmg, makeWrapper wraps the MacOS binary into $out/bin
#         so argv[0] resolves to the real binary path (symlinks break XPCOM loading).
# Linux:  platform-specific .tar.xz (x86_64 + aarch64), wrapped with
#         makeWrapper so shared libs and profile data dirs resolve correctly
#         for headless BiDi capture.
#
# To bump to a new Firefox release:
#   1. Update `version` below.
#   2. Fetch the SHA256SUMS for the new release:
#        https://releases.mozilla.org/pub/firefox/releases/<version>/SHA256SUMS
#   3. Grep for these three lines:
#        mac/en-US/Firefox <version>.dmg
#        linux-x86_64/en-US/firefox-<version>.tar.xz
#        linux-aarch64/en-US/firefox-<version>.tar.xz
#   4. Convert each hex digest to SRI:
#        nix hash convert --hash-algo sha256 --to sri <hex>
#   5. Update the three `hash` fields below.
{
  lib,
  stdenv,
  fetchurl,
  undmg,
  makeWrapper,
  version ? "150.0",
}:

let
  base = "https://releases.mozilla.org/pub/firefox/releases/${version}";
in

if stdenv.isDarwin then
  stdenv.mkDerivation {
    pname = "firefox-darwin";
    inherit version;

    src = fetchurl {
      url = "${base}/mac/en-US/Firefox%20${version}.dmg";
      hash = "sha256-IDZn/2sJIPiZc9R3sTlNmbS3iAemE5FMl7sbMgDm2hs=";
    };

    nativeBuildInputs = [
      undmg
      makeWrapper
    ];

    sourceRoot = ".";

    installPhase = ''
      mkdir -p $out/bin $out/Applications
      cp -r Firefox.app $out/Applications/
      makeWrapper $out/Applications/Firefox.app/Contents/MacOS/firefox $out/bin/firefox
    '';

    meta = {
      description = "Mozilla Firefox browser (Darwin fixed-output derivation)";
      homepage = "https://www.mozilla.org/firefox/";
      license = lib.licenses.mpl20;
      mainProgram = "firefox";
      platforms = lib.platforms.darwin;
    };
  }

else
  let
    srcs = {
      x86_64-linux = fetchurl {
        url = "${base}/linux-x86_64/en-US/firefox-${version}.tar.xz";
        hash = "sha256-L/mH6Uv6btUfU9a0uqfw+Ow/wmxMR72fhscNEaoPvWA=";
      };
      aarch64-linux = fetchurl {
        url = "${base}/linux-aarch64/en-US/firefox-${version}.tar.xz";
        hash = "sha256-nm4pdN36hAVEyvJu/adlxJiJMb8q2oXGsQdQDNkWzuc=";
      };
    };
  in
  stdenv.mkDerivation {
    pname = "firefox-linux";
    inherit version;

    src =
      srcs.${stdenv.hostPlatform.system}
        or (throw "firefox.nix: unsupported Linux arch: ${stdenv.hostPlatform.system}");

    nativeBuildInputs = [ makeWrapper ];

    installPhase = ''
      mkdir -p $out/lib/firefox $out/bin
      cp -r . $out/lib/firefox/
      makeWrapper $out/lib/firefox/firefox $out/bin/firefox \
        --set MOZ_LEGACY_PROFILES 1 \
        --set MOZ_ALLOW_DOWNGRADE 1
    '';

    meta = {
      description = "Mozilla Firefox browser (Linux fixed-output derivation)";
      homepage = "https://www.mozilla.org/firefox/";
      license = lib.licenses.mpl20;
      mainProgram = "firefox";
      platforms = [
        "x86_64-linux"
        "aarch64-linux"
      ];
    };
  }

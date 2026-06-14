# Fixed-output derivation for Firefox, bypassing nixpkgs (unavailable on Darwin).
#
# Darwin: universal .dmg (Apple Silicon + Intel), fetched from Mozilla CDN,
#         extracted with undmg, makeWrapper wraps the MacOS binary into $out/bin
#         so argv[0] resolves to the real binary path (symlinks break XPCOM loading).
# Linux:  platform-specific .tar.xz (x86_64 + aarch64). The upstream Mozilla
#         binary is FHS-linked (ELF interpreter /lib64/ld-linux-x86-64.so.2,
#         libraries on standard FHS paths) — neither exists on NixOS, so it
#         must be patchelf'd onto the nix-store glibc loader and given a
#         RUNPATH over its runtime deps. Mirrors nixpkgs firefox-bin-unwrapped
#         so the result runs on NixOS and FHS hosts alike. Without this the
#         binary fails execve with exit 127 ("required file not found") on any
#         host lacking the FHS loader.
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
  # Linux: patchelf the prebuilt Mozilla binary onto the nix-store loader.
  autoPatchelfHook,
  patchelfUnstable,
  wrapGAppsHook3,
  gtk3,
  adwaita-icon-theme,
  alsa-lib,
  dbus-glib,
  libxtst,
  curl,
  pciutils,
  libva,
  pipewire,
  # Self-contained fontconfig so headless rendering (screenshot/PDF) has glyphs
  # without depending on the host's /etc/fonts.
  makeFontsConf,
  dejavu_fonts,
  noto-fonts,
  noto-fonts-cjk-sans,
  noto-fonts-color-emoji,
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
    # Font set so headless captures render text rather than tofu, pointed at
    # via FONTCONFIG_FILE below. DejaVu covers Latin/Greek/Cyrillic; the Noto
    # families add broad Unicode, CJK, and emoji coverage (chrest#95). noto-cjk
    # is large (hundreds of MB), which is the bulk of the firefox closure
    # growth — drop it if closure size ever outweighs CJK capture fidelity.
    fontsConf = makeFontsConf {
      fontDirectories = [
        dejavu_fonts
        noto-fonts
        noto-fonts-cjk-sans
        noto-fonts-color-emoji
      ];
    };
  in
  stdenv.mkDerivation {
    pname = "firefox-linux";
    inherit version;

    src =
      srcs.${stdenv.hostPlatform.system}
        or (throw "firefox.nix: unsupported Linux arch: ${stdenv.hostPlatform.system}");

    nativeBuildInputs = [
      autoPatchelfHook
      # patchelfUnstable is required for --no-clobber-old-sections (below).
      patchelfUnstable
      wrapGAppsHook3
    ];

    # DT_NEEDED libraries autoPatchelf resolves into RUNPATH.
    buildInputs = [
      gtk3
      adwaita-icon-theme
      alsa-lib
      dbus-glib
      libxtst
    ];

    # dlopen'd at runtime (not in DT_NEEDED), so autoPatchelf can't discover
    # them — list explicitly so they still land in RUNPATH.
    runtimeDependencies = [
      curl
      pciutils
      libva.out
    ];
    appendRunpaths = [ "${pipewire}/lib" ];

    # Firefox post-processes its own relocations ("relrhack"); stock patchelf
    # clobbers the sections it relies on, so use the unstable flag.
    patchelfFlags = [ "--no-clobber-old-sections" ];

    installPhase = ''
      runHook preInstall
      mkdir -p $out/lib/firefox $out/bin
      cp -r . $out/lib/firefox/
      # wrapGAppsHook3 wraps whatever lands in $out/bin; symlink the real
      # binary there so it gets the GTK/GApps + profile-env wrapper.
      ln -s $out/lib/firefox/firefox $out/bin/firefox
      runHook postInstall
    '';

    # Inject Firefox's profile env through wrapGAppsHook3's wrapper rather
    # than stacking a second makeWrapper layer on top of it.
    preFixup = ''
      gappsWrapperArgs+=(
        --set MOZ_LEGACY_PROFILES 1
        --set MOZ_ALLOW_DOWNGRADE 1
        --set FONTCONFIG_FILE ${fontsConf}
      )
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

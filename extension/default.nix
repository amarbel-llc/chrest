{
  lib,
  mkBunDerivation,
  fetchBunDeps,
  jq,
  zip,
  browserType,
  # Project-level version, injected from the top-level flake
  # (chrest#61). The single source of truth is `chrestVersion` in
  # ../flake.nix; manifest-common.json's `version` field is overwritten
  # in buildPhase below.
  version,
}:

let
  src = lib.fileset.toSource {
    root = ./.;
    fileset = lib.fileset.unions [
      ./src
      ./assets
      ./manifest-common.json
      ./manifest-chrome.json
      ./manifest-firefox.json
      ./rolldown.config.mjs
      ./package.json
      ./bun.lock
      ./bun.nix
      ./zz-firefox-amo-metadata.json
    ];
  };
in
mkBunDerivation {
  pname = "chrest-extension-${browserType}";
  inherit src version;
  packageJson = ./package.json;
  bunDeps = fetchBunDeps {
    bunNix = ./bun.nix;
  };

  nativeBuildInputs = [
    jq
    zip
  ];

  buildPhase = ''
    runHook preBuild

    mkdir -p dist-${browserType}

    # Merge browser-specific manifest over the common base, then
    # overwrite the `version` field with the flake-supplied value.
    # The static "version" in manifest-common.json is a fallback for
    # editor previews; the canonical version is this one.
    jq -s '(reduce .[] as $i ({}; . + $i)) * { version: "${version}" }' \
      manifest-common.json manifest-${browserType}.json \
      > dist-${browserType}/manifest.json

    cp src/* assets/* dist-${browserType}/

    BROWSER_TYPE=${browserType} bun run build

    runHook postBuild
  '';

  installPhase = ''
    runHook preInstall

    mkdir -p "$out"
    cp -r dist-${browserType} "$out/"
    ${lib.optionalString (browserType == "firefox") ''
      cp zz-firefox-amo-metadata.json "$out/dist-${browserType}/"
    ''}

    # Info-ZIP's zip reads mtime from stat() and does not honor
    # SOURCE_DATE_EPOCH. Normalize mtimes to the earliest representable
    # DOS date (1980-01-01 UTC) so two builds produce identical zips.
    find "$out/dist-${browserType}" -exec touch -h -t 198001010000 {} +

    ( cd "$out" && \
      find dist-${browserType} -type f -print0 | LC_ALL=C sort -z | \
        TZ=UTC xargs -0 zip -X -q dist-${browserType}.zip )

    runHook postInstall
  '';
}

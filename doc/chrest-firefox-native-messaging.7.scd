chrest-firefox-native-messaging(7) "2026-10-21" "chrest"

# NAME

chrest-firefox-native-messaging - debugging chrest's native host on Firefox

# STATUS

Investigation notes dated 2026-10-21, from one host (Ubuntu 24.04, unconfined
nix-built Firefox 150, chrest 0.4.0 dev build). Each claim is labeled
VERIFIED, SOURCE-READ, UNVERIFIED THEORY or DEAD END. Do not promote a
lower-confidence claim without testing it. Tracked as chrest#129 and
chrest#130 (forge: linenisgreat/chrest).

# HOW IT CONNECTS

- The extension's options page stores _browser_id_ (for example _dev1_) in
  _storage.sync_.
- _main.js_ prefixes the browser type, so the id becomes _firefox-dev1_.
- The extension calls _runtime.connectNative_ for
  _com.linenisgreat.code.chrest_ and posts a framed who-am-i message.
- The host (_chrest-server_) creates
  _$XDG_STATE_HOME/chrest/firefox-dev1.sock_ only after it receives that
  framed who-am-i message.
- The extension console logs "try connect", the full id, "native host
  connected" and later "native host disconnected". "native host connected"
  only means connectNative was called and a message was posted. It does *not*
  mean the host started.

# MANIFEST LOCATION AND VALIDATION

VERIFIED by strace, and SOURCE-READ from Firefox's _NativeManifests.sys.mjs_:

	https://raw.githubusercontent.com/mozilla-firefox/firefox/main/toolkit/components/extensions/NativeManifests.sys.mjs

- Lookup order is _~/.mozilla/native-messaging-hosts_, then
  _/usr/lib/mozilla/native-messaging-hosts_.
- Each manifest is validated with
  _Schemas.normalize(manifest, "manifest.NativeManifest")_, then checked for
  type, name, an absolute path, and that _allowed_extensions_ contains the
  extension id.
- ANY failure returns null, and every path tried ends in the same generic
  error "No such native application <name>". The message does not say which
  check failed.
- In the failing case Firefox did not stat or exec the manifest's _path_.
- MDN's Native manifests page documents only _~/.mozilla_ for per-user
  installs:

	https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/Native_manifests

# THE CASE WE HIT

- The manifest's _allowed_extensions_ held a stale Chrome-style entry,
  "bddipienkaenkjmgdaodjkeccineifch", alongside
  "chrest@code.linenisgreat.com".
- _chrest init_ merges existing entries (_Install_ in
  _go/internal/charlie/install_ decodes the existing manifest and appends),
  so it carried the stale entry forward.
- Rewriting the manifest with only "chrest@code.linenisgreat.com" made the
  host start and the socket appear.
- Caveat: the _path_ was fixed in the same edit. So the stale entry is the
  LIKELY cause, not an isolated one.
- UNVERIFIED THEORY: that the Chrome-style string fails the add-on ID pattern
  in Firefox's _native_manifest.json_ schema. That schema file was not read.

# GETTING THE REAL ERROR

Firefox reports the disconnect reason on the port (_port.error.message_), not
in _browser.runtime.lastError_. So _main.js_ printing "unknown reason" hides
it (chrest#129). Run this in the extension's background console:

	const p = browser.runtime.connectNative("com.linenisgreat.code.chrest");
	p.onDisconnect.addListener(port => console.log(port.error && port.error.message))

# TOOLS WE BUILT

All are justfile recipes in the root justfile.

- _explore-install-host <extension-id> [browser] [name]_: runs _chrest init_
  to write the manifest. It also rewrites _default-browser_ in
  _~/.config/chrest/config.json_, so re-run it for the browser you want as
  default.
- _explore-run firefox [moz_log]_: web-ext with _--verbose_. Console and
  _dump()_ output go to stderr via the prefs _browser.dom.window.dump.enabled_,
  _devtools.console.stdout.chrome_ and _devtools.console.stdout.content_. A
  persistent profile under _.tmp/ff-profile_ keeps sign-in and _browser_id_
  across re-runs.
- _explore-run-strace_: the same under _strace -f -e trace=file_, logging to
  _.tmp/firefox-strace.log_. Search the log for _native-messaging-hosts_.
- _explore-host-who-am-i_: runs _chrest-server_ with a correctly framed
  who-am-i message, stderr shown. Proves the host binary itself starts.
- Logging-wrapper technique: point the manifest's _path_ at a shell script
  that logs its environment and stderr, then execs the host. If the log file
  never appears, the browser never launched the host.

# DEBUGGING ORDER

The order that would have saved time:

. Run the console probe above for the real error.
. strace to see which manifest paths Firefox opens.
. If the manifest is opened but rejected, validate its fields against the
  source above (stale _allowed_extensions_ entries first).
. Run _explore-host-who-am-i_ to rule out the binary.
. Only then consider sandboxing; see *chrest-firefox-flatpak*(7).

# DEAD ENDS

- DEAD END, REFUTED: the XDG manifest location. strace showed no access under
  _~/.config/mozilla_ (the profile lives there, the manifests do not).
- DEAD END: reading "native host connected" as proof the host ran.
- DEAD END: treating the options-page failure seen in the Flatpak as the
  same problem as the unconfined "No such native application"; the options
  page loaded fine unconfined, and the two failures have different causes.

# SEE ALSO

*chrest*(1), *chrest-init*(1), *chrest-firefox-flatpak*(7),
*eng-manpages*(7), web-ext (Mozilla's documentation)

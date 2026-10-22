chrest-firefox-flatpak(7) "2026-10-21" "chrest"

# NAME

chrest-firefox-flatpak - notes on running chrest's native host under Flatpak Firefox

# STATUS

Investigation notes dated 2026-10-21. The workaround described below is
*untested*. Nothing here has been implemented in chrest. Each claim is
labeled VERIFIED, FROM RESEARCH, THEORY or NOT TESTED; do not promote a
lower-confidence claim to a higher one without testing it.

# DESCRIPTION

chrest's browser extension talks to the browser-side host (_chrest-server_)
over Native Messaging. A Firefox installed as the Flatpak
_org.mozilla.firefox_ runs in a sandbox, and the standard chrest install
does not reach into it. This page records what was observed on one host, what
upstream documents, and what a future fix would need.

# VERIFIED ON THIS HOST

Host: Ubuntu 24.04.5, chrest 0.4.0 dev build, 2026-10-21.

- The live Firefox is the system-wide Flatpak _org.mozilla.firefox_. Its
  metadata is at _/var/lib/flatpak/app/org.mozilla.firefox/x86_64/stable/active/metadata_.
- Its profile lives under _~/.var/app/org.mozilla.firefox/.mozilla/firefox/<profile>/_.
  The profile was identified because the add-on's moz-extension UUID appears
  in that profile's _prefs.js_ (_extensions.webextensions.uuids_) and in no
  other profile on the host.
- The Flatpak's [Context] grants:
	- shared: network, ipc
	- sockets: x11, wayland, pulseaudio, fallback-x11, pcsc, cups
	- devices: all
	- filesystems: xdg-config/gtk-3.0:ro, xdg-download, /run/.heim_org.h5l.kcm-socket,
	  /run/udev:ro, xdg-run/speech-dispatcher:ro
	- persistent: .mozilla
- The session bus policy has *no* org.freedesktop.Flatpak talk permission.
  Consequently the sandbox cannot see _/nix/store_, the chrest worktree, or
  _~/.local/state/chrest_.
- _chrest init --browser firefox_ (_go/internal/charlie/install_,
  _path_linux.go_) writes the native-messaging manifest only to
  _~/.mozilla/native-messaging-hosts/_ on the host. The Flatpak Firefox's own
  home is _~/.var/app/org.mozilla.firefox/.mozilla/_, which has no
  _native-messaging-hosts_ directory, so it never sees that manifest.
- Neither the WebExtensions portal (_org.freedesktop.portal.WebExtensions_)
  nor xdg-native-messaging-proxy files exist under _/usr/share_ or _/usr_ on
  this host. The Flatpak profile's _prefs.js_ has no
  _widget.use-xdg-desktop-portal.native-messaging_ pref (nor its _-proxy_
  variant).
- Symptoms after loading chrest's _dist-firefox_ as a temporary add-on in
  that Flatpak Firefox: the add-on is listed, but its options page source is
  empty, its icon is missing, and no _firefox-dev.sock_ ever appears in
  _~/.local/state/chrest/_ (the host never started).
- A wrong theory, REFUTED (see below): that Firefox 150 reads manifests from
  the XDG location _~/.config/mozilla/native-messaging-hosts/_. Refuted by
  strace of the unconfined Firefox, which never touches that path.

# VERIFIED UNCONFINED

Same host, same day. The Firefox here is the nix-built Firefox 150 launched
by _just explore-run firefox_ (web-ext), not the Flatpak.

- It loads the same _dist-firefox_ add-on with a working options page and a
  working icon. The Flatpak symptoms (empty options.html source, missing
  icon) did *not* occur unconfined. This supports, but does not prove, that
  the Flatpak sandbox is the cause.
- Once the native-messaging manifest is valid, the native host connects and
  its socket appears.
- The earlier XDG theory is REFUTED for this Firefox: strace showed it opens
  _~/.mozilla/native-messaging-hosts/<name>.json_ first, then
  _/usr/lib/mozilla/native-messaging-hosts/<name>.json_, and nothing under
  _~/.config/mozilla_. The Firefox *profile* does live under
  _~/.config/mozilla/firefox_ on this host, which is what made the XDG theory
  tempting.
- The "No such native application" failure seen unconfined had a DIFFERENT
  cause (a manifest rejected by validation) than the Flatpak limitation
  (the sandbox cannot reach the host binary). Do not conflate them; see
  *chrest-firefox-native-messaging*(7).

# UNVERIFIED THEORY

- The Flatpak's empty options page and missing icon are probably because the
  sandbox cannot read the add-on's files from the worktree path (perhaps
  only the manifest was readable). This was *not confirmed*; the unconfined
  result above is consistent with it but does not isolate it.

# WORKAROUND STATUS

The Flatpak workaround described under FROM RESEARCH is still *untested*. No
part of it has been run on this host.

# FROM RESEARCH

These are upstream sources. None of this was tested on this host.

Mozilla's design document describes how confined Firefox (snap, Flatpak)
reaches native hosts: via the WebExtensions XDG portal (work in progress;
pref _widget.use-xdg-desktop-portal.native-messaging_, where 0 is off, 1 is
on, 2 is autodetect) or via xdg-native-messaging-proxy (pref
_widget.use-xdg-desktop-portal.native-messaging-proxy_). The portal shipped as
a distro patch in Ubuntu 22.04+; the proxy is stated to be available in
Ubuntu 26.04. A user consent prompt appears per extension and host.
Permissions can be inspected with _flatpak permissions webextensions_.
Debug with MOZ_LOG or about:logging, modules NativeMessagingPortal or
NativeMessagingProxy.

	https://firefox-source-docs.mozilla.org/toolkit/components/extensions/webextensions/native-messaging-portal-design.html

Manual workaround, from a KDE developer's write-up for KeePassXC:

	https://kde.haraldsitter.eu/posts/firefox-and-keepassxc-flatpaks/

- Put the manifest in the Flatpak's home,
  _~/.var/app/org.mozilla.firefox/.mozilla/native-messaging-hosts/_.
- Point the manifest's _path_ at a wrapper script visible inside the sandbox
  that runs _exec /usr/bin/flatpak-spawn --host --watch-bus <host binary> "$@"_.
- Grant the escape hatch:
  _flatpak override --user --talk-name=org.freedesktop.Flatpak org.mozilla.firefox_.
- This undermines the sandbox: the Flatpak can then execute arbitrary host
  commands.

Applied to chrest, the wrapper would run the *host* _chrest-server_, so its
unix socket stays at _~/.local/state/chrest/_ where cutting-garden and the
CLI look for it.

Why copying a static _chrest-server_ into the sandbox is the wrong fix: it
would run inside the sandbox, so its socket would land in the Flatpak's own
state directory, unreachable from the host's cutting-garden and CLI.

Alternative: a third-party, work-in-progress proxy (systemd socket plus a
client binary):

	https://github.com/leenr/native-messaging-proxy

Upstream context: Bugzilla 1621763 ("[flatpak] Support native messaging")
and Bugzilla 1955322 (portal does not work on flathub).

# NOT TESTED

- That stdio (the Native Messaging length-prefixed framing) passes cleanly
  through _flatpak-spawn --host_.
- That the extension files must additionally live in a sandbox-visible path
  such as _~/Downloads_ (_xdg-download_ is granted) for the add-on's options
  page and icon to load.
- That the portal or proxy route works at all on this host; the required
  files were absent.

# UNCONFINED ALTERNATIVES

- A nixpkgs Firefox installed through home-manager is not sandboxed. It
  reads _~/.mozilla/native-messaging-hosts_ and executes _/nix/store_ paths
  directly, so the standard _chrest init --browser firefox_ is expected to
  work as-is.
- For the immediate test, _just explore-run firefox_ was chosen: it launches
  the dev-built extension via web-ext in a throwaway profile, bypassing the
  Flatpak entirely.

# DECISION / TODO

DECISION (2026-10-21): the installer is left unchanged for now. _chrest init_
writes only the host-side per-user location, _~/.mozilla/native-messaging-hosts/_,
which VERIFIED works for an unconfined Firefox. It was deliberately not
extended to write the Flatpak's own directory
(_~/.var/app/org.mozilla.firefox/.mozilla/native-messaging-hosts/_) because a
manifest there does not work by itself: the sandbox cannot see or execute the
_/nix/store_ host binary the manifest names, so it needs the wrapper and the
permission override below, and that workaround is still NOT TESTED. Writing
the manifest alone would give an install that looks complete and still fails.
The _~/.config/mozilla/native-messaging-hosts/_ location was also considered
and rejected: it is REFUTED (see above).

Everything below is PROPOSAL ONLY. None of it is implemented.

chrest's installer (_go/internal/charlie/install_) has no Flatpak awareness.
A future fix would need:

- The installer detects _org.mozilla.firefox_ (for example by the presence of
  _~/.var/app/org.mozilla.firefox_) and also writes the manifest to
  _~/.var/app/org.mozilla.firefox/.mozilla/native-messaging-hosts/_.
- A wrapper script at a sandbox-visible path (inside the Flatpak's own data
  directory) that execs _flatpak-spawn --host --watch-bus_ on the host
  _chrest-server_, with the manifest's _path_ pointing at the wrapper.
- A _flatpak override --user --talk-name=org.freedesktop.Flatpak_ for the app,
  with the sandbox-weakening consequence documented to the user, or
  preferably detection of a working portal/proxy first and use of that
  instead.
- Placing extension files where the sandbox can read them, if the THEORY
  above holds.
- A test of the stdio-through-flatpak-spawn behavior before committing to the
  design.
- Whatever the installer writes for a Firefox manifest must pass Firefox's own
  validation (see *chrest-firefox-native-messaging*(7), chrest#130): merging
  into an existing manifest currently carries stale entries forward, and one
  invalid entry makes Firefox reject the whole file.
- Until this is done, a Flatpak Firefox user should either use an unconfined
  Firefox (for example a nixpkgs build) or apply the workaround by hand.

# SEE ALSO

*chrest*(1), *chrest-init*(1), *chrest-firefox-native-messaging*(7),
*eng-manpages*(7), *flatpak-override*(1),
*flatpak-spawn*(1)

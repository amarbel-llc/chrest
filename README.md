Chrest: healthier window and tab hygiene

---

Chrest is a CLI tool and browser extension that lets you manage Chrome and
Firefox via REST. Chrest was inspired by (and somewhat forked from)
[`TabFS`](https://omar.website/tabfs/).

Chrest consists of two parts:

1.  Browser Extension: ([/extension](extension))
2.  Native Messaging Host & CLI Client: `chrest` ([/](/))

# Examples

Note: to keep Chrest slim, Chrest does not include pretty-print JSON or HTTP
Request construction, but there are other wonderful tools you can use for that
like [`http`](https://httpie.io/) and [`jq`](https://jqlang.github.io/jq/).

- Getting all windows and tabs:

      $ http --ignore-stdin --offline localhost/windows |
      $   chrest client |
      $   jq
      > [
      >   {
      >     "alwaysOnTop": false,
      >     "focused": false,
      >     "height": 1400,
      >     "id": 1531273373,
      >     "incognito": false,
      >     "left": 1411,
      >     "state": "normal",
      >     "tabs": [
      >       {
      >         "active": true,
      >         "audible": false,
      >         "autoDiscardable": true,
      >         "discarded": false,
      >         "favIconUrl": "https://github.githubassets.com/favicons/favicon.svg",
      >         "groupId": -1,
      >         "height": 1279,
      >         "highlighted": true,
      >         "id": 1531273367,
      >         "incognito": false,
      >         "index": 0,
      >         "mutedInfo": {
      >           "muted": false
      >         },
      >         "pinned": false,
      >         "selected": true,
      >         "status": "complete",
      >         "title": "friedenberg/chrest",
      >         "url": "https://github.com/friedenberg/chrest",
      >         "width": 1128,
      >         "windowId": 1531273373
      >       }
      >     ],
      >     "top": 20,
      >     "type": "normal",
      >     "width": 1128
      >   }
      > ]

- Creating a new window with some URL's:

      $ http --ignore-stdin --offline localhost/windows \
      $   urls[]=https://github.com/friedenberg/chrest |
      $   chrest client |
      $   jq
      > {
      >   "alwaysOnTop": false,
      >   "focused": false,
      >   "height": 1400,
      >   "id": 1531273390,
      >   "incognito": false,
      >   "left": 0,
      >   "state": "normal",
      >   "tabs": [
      >     {
      >       "active": true,
      >       "audible": false,
      >       "autoDiscardable": true,
      >       "discarded": false,
      >       "groupId": -1,
      >       "height": 1279,
      >       "highlighted": true,
      >       "id": 1531273391,
      >       "incognito": false,
      >       "index": 0,
      >       "lastAccessed": 1708099951989.452,
      >       "mutedInfo": {
      >         "muted": false
      >       },
      >       "pendingUrl": "https://github.com/friedenberg/chrest",
      >       "pinned": false,
      >       "selected": true,
      >       "status": "loading",
      >       "title": "",
      >       "url": "",
      >       "width": 1128,
      >       "windowId": 1531273390
      >     }
      >   ],
      >   "top": 0,
      >   "type": "normal",
      >   "width": 1128
      > }

- Closing a window:

      $ http --ignore-stdin --offline DELETE localhost/windows/1531273390 |
      $   chrest client |
      $   jq

# Capturing pages

`chrest capture` drives a headless browser to render a URL and returns the
result in the format you ask for. Output streams to stdout by default; use
`--output <path>` to write atomically to a file.

Eleven formats are supported:

| Format              | Media type                     | Notes                                                                                                               |
| ------------------- | ------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| `pdf`               | `application/pdf`              | flags: `--landscape`, `--no-headers`, `--background`                                                                |
| `screenshot-png`    | `image/png`                    | flag: `--full-page`                                                                                                 |
| `screenshot-jpeg`   | `image/jpeg`                   | flags: `--quality`, `--full-page`                                                                                   |
| `mhtml`             | `multipart/related`            | not yet functional                                                                                                  |
| `a11y`              | `application/json`             | not yet functional; accessibility tree                                                                              |
| `text`              | `text/plain; charset=utf-8`    | `document.body.innerText`                                                                                           |
| `html-monolith`     | `text/html; charset=utf-8`     | self-contained HTML with assets inlined as `data:` URIs (via the [`monolith`](https://github.com/Y2Z/monolith) CLI) |
| `html-outer`        | `text/html; charset=utf-8`     | raw `document.documentElement.outerHTML` — no asset inlining                                                        |
| `markdown-full`     | `text/markdown; charset=utf-8` | whole rendered DOM → markdown                                                                                       |
| `markdown-reader`   | `text/markdown; charset=utf-8` | Readability-extracted main content → markdown                                                                       |
| `markdown-selector` | `text/markdown; charset=utf-8` | CSS-selector scoped; requires `--selector <css>`                                                                    |

Backend is headless Firefox via WebDriver BiDi.

Examples:

- PDF to stdout:

      $ chrest capture --format pdf --url https://example.com > page.pdf

- Full-page PNG to a file, atomically:

      $ chrest capture --format screenshot-png --full-page \
      $   --url https://en.wikipedia.org/wiki/Markdown \
      $   --output wiki.png

- Self-contained archive of a blog post:

      $ chrest capture --format html-monolith \
      $   --url https://simonwillison.net/ \
      $   --output blog.html

- Reader-mode markdown (boilerplate stripped):

      $ chrest capture --format markdown-reader \
      $   --url https://simonwillison.net/2026/Feb/15/gwtar/

- Markdown scoped to one element:

      $ chrest capture --format markdown-selector --selector "#bodyContent" \
      $   --url https://en.wikipedia.org/wiki/Markdown

## Batch captures

`chrest capture-batch` implements the **Web Capture Archive Protocol (RFC 0001)**
for pipelines that need multiple captures of the same page in a single pass,
with content-addressed artifacts and a JSON result envelope.

    $ echo '{
        "schema":   "web-capture-archive/v1",
        "writer":   {"cmd": ["your-content-addressed-writer"]},
        "url":      "https://en.wikipedia.org/wiki/Ferris_wheel",
        "defaults": {"browser": "firefox", "split": false},
        "captures": [
          {"name": "pdf",     "format": "pdf"},
          {"name": "md",      "format": "markdown-reader"},
          {"name": "archive", "format": "html-monolith"}
        ]
      }' | chrest capture-batch | jq

The writer command is invoked once per artifact; chrest streams the bytes to
its stdin and expects a one-line JSON response of the form
`{"id": "<content-addressed-id>", "size": N}`. The final stdout JSON is a
result envelope with one entry per capture, each pointing at the writer's
returned IDs.

See [docs/features/0001-web-page-capture.md](docs/features/0001-web-page-capture.md)
for design intent, backend trade-offs, and a full list of known limitations.

# Installation

## 1. Install the CLI

Choose one:

- Go: `go install code.linenisgreat.com/chrest/go/cmd/chrest@latest`
- Nix: `nix profile install github:friedenberg/chrest`

## 2. Install the browser extension

**Chrome:**

1.  Go to [chrome://extensions/](chrome://extensions/)
2.  Enable Developer mode (top-right corner)
3.  Click "Load unpacked" and select the `extension/dist-chrome/` folder

**Firefox:**

1.  Go to [about:debugging#/runtime/this-firefox](about:debugging#/runtime/this-firefox)
2.  Click "Load Temporary Add-on" and select any file in `extension/dist-firefox/`

## 3. Initialize chrest

    $ chrest init --browser chrome

Or for Firefox:

    $ chrest init --browser firefox

If you omit `--browser`, chrest will prompt you interactively.

## 4. Verify

Reload the extension in your browser, then:

    $ chrest list-windows

# Known Issues

- Chrome will turn off service workers when there are no windows left, so at
  least 1 window needs to exist for `chrest` to be running. I may add a
  fallback in `chrest` to use `AppleScript` to create a new window
  if the server is not running.

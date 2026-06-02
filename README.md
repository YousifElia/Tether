# myterm

A self-hosted **web terminal**: run it on your machine, sign in from any browser,
and drive a real shell — including from an iPad over a public HTTPS link. Built
in Go as a six-phase project; this tree is the **Phase 6 (file transfer)**
delivery.

```
go run ./cmd/myterm --share-dir ~/term-share
# open the printed http://127.0.0.1:7314, paste the owner token, click "Files"
```

## The six phases

| Phase | Theme | What it added | Status |
|------:|-------|---------------|--------|
| 1 | **Terminal over the web** | Browser ↔ shell across a WebSocket: a cross-platform PTY (ConPTY on Windows, Unix PTY elsewhere) rendered with xterm.js, with live resize. | ✅ in this build |
| 2 | **Auth & roles** | A token sign-in gate on every page and the socket; two roles — **owner** (read/write) and **spectator** (read-only) — carried on a `HttpOnly`, `SameSite=Strict` cookie. | ✅ in this build |
| 3 | **Shared session** | One shared shell fanned out to many viewers, with **scrollback replay** so late joiners/reconnects see recent history, and **size sync** so spectators mirror the owner's exact dimensions. | ✅ in this build |
| 4 | **Public access** | One flag brings up a **Cloudflare quick tunnel** (`https://….trycloudflare.com`, no account, no port-forwarding) and prints a **QR code**; the tunnel auto-reconnects and shuts down cleanly. | ✅ in this build |
| 5 | **Mobile polish & offline** | A mobile-optimized UI and bundling xterm.js into the binary for fully-offline first loads. | ⏳ **not in this tree** — xterm.js still loads from a CDN (see *Notes*) |
| 6 | **File transfer** | Upload / download / delete files in a shared directory behind `--share-dir`, with owner/spectator gating, atomic overwrite, and a dependency-free file panel in the UI. | ✅ **this delivery** |

> **A note on Phase 5.** The base this was built on still serves xterm.js from a
> CDN, so the Phase 5 goal (offline bundling + a dedicated mobile key bar) is the
> one remaining piece. Phase 6 was implemented independently of it and adds **no
> new dependencies**, so it slots in cleanly whenever Phase 5 lands. The current
> UI is responsive and works on iPad today; it simply needs network on first load
> to fetch xterm.js.

## What works now

- **File transfer** — point myterm at a directory with `--share-dir` and a
  collapsible **Files** panel appears. The owner can upload (drag-and-drop on
  desktop, tap-to-browse on iPad), download, and delete; spectators can list and
  download only. Overwrites and deletes are confirmed in the UI; uploads are
  written atomically and capped by `--max-upload-mb`.
- **Public URL** via a Cloudflare quick tunnel + scannable QR code.
- **Sign-in gate** on every page, the websocket, and the file API.
- **Owner / spectator roles**; spectators are off unless `--allow-spectators`.
- **Shared session** with scrollback replay and owner→spectator size sync.
- **Cross-platform PTY** (ConPTY / Unix) with live resize.

## Prerequisites

- **Go 1.22+** — https://go.dev/dl/
- For the public tunnel: **cloudflared** (auto-detected, or let myterm fetch it)
- Internet on first page load (xterm.js comes from a CDN; the file panel itself
  is dependency-free vanilla JS)

## Quick start (local)

```
go mod tidy
go run ./cmd/myterm
```

Open the printed `http://127.0.0.1:7314`, paste the **owner** token from the
startup banner, and you're in.

## Share files

```
go run ./cmd/myterm --share-dir ~/term-share
```

A **Files** button appears top-right. From the panel you can:

- **Download** any file (owner or spectator).
- **Upload** (owner only) — drag files onto the drop zone, or tap it to pick
  files. Uploading a name that already exists asks for confirmation, then
  replaces it.
- **Delete** (owner only) — confirmed, and **permanent** (no trash/undo).

The shared directory is **flat**: only files in that directory are listed,
subdirectories are ignored, and uploaded names are reduced to a single filename,
so nothing is ever written outside the shared directory. Uploads stream to a
hidden temp file that is atomically renamed into place, so a partial or oversized
upload never appears in the list.

### File API

The panel is a thin client over four endpoints. All require a signed-in token;
upload and delete additionally require the **owner** role.

| Method & path              | Role      | Purpose                                            |
|----------------------------|-----------|----------------------------------------------------|
| `GET /api/files`           | any       | List files (`name`, `size`, `modified`) + upload cap |
| `GET /api/download/<name>` | any       | Download one file (served as an attachment)        |
| `POST /api/upload`         | **owner** | Multipart upload, field `files` (one or many)      |
| `POST /api/delete/<name>`  | **owner** | Permanently delete one file                        |

## Go public (reach it from your iPad anywhere)

```
go run ./cmd/myterm --tunnel
```

After a few seconds you'll see a public `https://….trycloudflare.com` URL and a
QR code. Scan it on your iPad, paste the owner token, and run `claude` in the
terminal — you're coding on the iPad against your desktop.

### Getting cloudflared

myterm looks for `cloudflared` on your PATH and common install locations:

```
# Windows
winget install --id Cloudflare.cloudflared
# macOS
brew install cloudflared
```

Or let myterm fetch the official binary (cached for next time):

```
go run ./cmd/myterm --tunnel --install-cloudflared
```

Point at a specific binary with `--cloudflared /path/to/cloudflared`.

## Flags

| Flag                   | Default               | Description                                       |
|------------------------|-----------------------|---------------------------------------------------|
| `-addr`                | `127.0.0.1:7314`      | Address to listen on (`host:port`)                |
| `-shell`               | `powershell.exe` / `$SHELL` / `bash` | Shell to launch                    |
| `-owner-token`         | env or auto-generated | Owner (read/write) token                          |
| `-allow-spectators`    | `false`               | Enable read-only spectator access                 |
| `-spectator-token`     | env or auto-generated | Spectator token (only if spectators enabled)      |
| `-scrollback-kb`       | `256`                 | KB of recent output replayed to (re)joiners       |
| `-share-dir`           | `""` (off)            | Directory to share for upload/download/delete     |
| `-max-upload-mb`       | `100`                 | Maximum size of a single uploaded file (MB)       |
| `-tunnel`              | `false`               | Expose a public HTTPS URL via a cloudflared tunnel|
| `-cloudflared`         | auto-detected         | Path to the cloudflared binary                    |
| `-install-cloudflared` | `false`               | Download cloudflared automatically if not found   |
| `-qr`                  | `true`                | Print a QR code for the public URL                |

Environment equivalents: `MYTERM_OWNER_TOKEN`, `MYTERM_SPECTATOR_TOKEN`.
Sign out anytime via `/logout`.

## Build from source

```
make build            # native binary -> ./myterm
make run              # go run ./cmd/myterm
```

Cross-compile (output in `dist/`); pure-Go, so no C toolchain is needed:

```
make windows          # dist/myterm.exe        (windows/amd64)
make linux            # dist/myterm-linux      (linux/amd64)
make macos            # dist/myterm-macos      (darwin/arm64, Apple Silicon)

# Intel Mac:
GOOS=darwin GOARCH=amd64 go build -o dist/myterm-macos-intel ./cmd/myterm
```

All four targets (windows/amd64, linux/amd64, darwin/arm64, darwin/amd64) build
from this source as-is.

## Security when public

When you use `--tunnel`, your terminal is reachable from the public internet, so:

- **The token is the only thing protecting it.** Set a long, stable owner token
  (`MYTERM_OWNER_TOKEN`) before going public — the random URL is not a credential
  (it appears in logs).
- **Don't enable `--allow-spectators` on a public tunnel** unless you mean to let
  others watch; share only the spectator token, never the owner token.
- **Mind what you share.** With `--share-dir` on a public tunnel, anyone with a
  token can download from that directory (and the owner can delete in it). Point
  it at a dedicated transfer folder, not your home directory.
- Auth rides a `SameSite=Strict`, `HttpOnly` cookie; `Secure` is set
  automatically over the HTTPS tunnel (via `X-Forwarded-Proto`), which blocks
  cross-site WebSocket hijacking.
- Quit with Ctrl+C — myterm kills the shell and the tunnel before exiting.

## Notes & limits

- **xterm.js loads from a CDN** (the outstanding Phase 5 item), so the first page
  load needs internet; once cached by the browser it works offline until evicted.
- **Quick-tunnel URLs are temporary.** Each run (and each auto-reconnect) gets a
  new random hostname; because the auth cookie is bound to the hostname, a changed
  URL means signing in again. A permanent URL needs a named Cloudflare tunnel
  (account required; not built in).
- The shared directory is flat by design; per-file size is capped, but a single
  multipart request's total is not separately bounded (the uploader is the
  authenticated owner).
- Scrollback is replayed as raw bytes; a full-screen TUI may need one keypress to
  repaint after a reconnect. The QR is clearest on a dark terminal; the URL is
  always printed too (`-qr=false` to hide it).

## Project layout

```
my-term/
├── cmd/myterm/main.go        flags, tunnel wiring, share-dir setup, shutdown
├── pkg/
│   ├── auth/auth.go          tokens, roles, cookie/role resolution
│   ├── files/files.go        sandboxed share dir: list/open/save/delete + guards
│   ├── session/session.go    shared shell, scrollback, output broadcast
│   ├── pty/                  ConPTY (Windows) / Unix PTY behind one interface
│   ├── tunnel/               cloudflared supervisor: spawn, parse URL, restart, install
│   └── server/
│       ├── server.go         routes, auth middleware, /ws bridge
│       ├── files_handlers.go file-transfer endpoints (list/download/upload/delete)
│       ├── login.go          sign-in page
│       ├── web.go            embeds web/ into the binary
│       └── web/              index.html · app.js · style.css
├── go.mod
└── Makefile
```

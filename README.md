# myterm — phase 3

A web terminal: run it on your machine, sign in, and drive a shell from any
browser. **Phase 3 of 6** makes the shared session robust for reconnects and
multiple viewers.

## What works now

- **Sign-in gate** — every page and the websocket require a valid token
- **Two roles** — **owner** (read/write) and **spectator** (read-only; off by default)
- **One shared session** — the owner drives a single shell; spectators watch the
  *same* terminal
- **Scrollback replay** — anyone who attaches is replayed recent output, so:
  - a spectator who joins mid-session sees what's already on screen
  - the owner who reconnects (e.g. iPad wifi blip) lands back on the live screen
    instead of a blank one
- **Size sync** — spectators mirror the owner's exact terminal dimensions, so the
  shared view lines up
- Cross-platform PTY (ConPTY on Windows, Unix PTY elsewhere) and live resize

## Prerequisites

- Go 1.22 or newer — https://go.dev/dl/
- Internet on first page load (xterm.js is pulled from a CDN; phase 5 bundles it)

## Setup & run

```
go mod tidy
go run ./cmd/myterm
```

On startup it prints a sign-in box:

```
----------------------------------------------------------------
  open   http://127.0.0.1:7314   and paste a token to sign in:

    owner  (read / write):  k3Jq...   (auto-generated; set MYTERM_OWNER_TOKEN for a stable token)
    spectator (read-only):  disabled  (use --allow-spectators)
----------------------------------------------------------------
```

Open the URL, paste the **owner** token on the sign-in page, and you're in.

## Stable tokens (recommended for daily use)

Auto-generated tokens change on every restart. Set your own with environment
variables (they don't show up in the process list the way flags do):

```powershell
$env:MYTERM_OWNER_TOKEN = "pick-a-long-random-string"
.\myterm.exe
```

```
export MYTERM_OWNER_TOKEN=pick-a-long-random-string
./myterm
```

## Sharing read-only access (spectators)

Spectators are **off by default**. Enable with `--allow-spectators`:

```
myterm.exe --allow-spectators
```

This prints a separate spectator token; anyone who signs in with it sees your
live terminal but cannot type. Keep the owner token private; share only the
spectator token. Set a stable one via `MYTERM_SPECTATOR_TOKEN`/`--spectator-token`.

## Flags

| Flag                 | Default               | Description                                  |
|----------------------|-----------------------|----------------------------------------------|
| `-addr`              | `127.0.0.1:7314`      | Address to listen on (`host:port`)           |
| `-shell`             | `powershell.exe` / `$SHELL` / `bash` | Shell to launch               |
| `-owner-token`       | env or auto-generated | Owner (read/write) token                     |
| `-allow-spectators`  | `false`               | Enable read-only spectator access            |
| `-spectator-token`   | env or auto-generated | Spectator token (only if spectators enabled) |
| `-scrollback-kb`     | `256`                 | KB of recent output replayed to (re)joiners  |

Environment equivalents: `MYTERM_OWNER_TOKEN`, `MYTERM_SPECTATOR_TOKEN`.
Sign out anytime by visiting `/logout`.

## How it works

- **Auth** — the token lives in a `SameSite=Strict`, `HttpOnly` cookie, which is
  what blocks cross-site WebSocket hijacking; `Secure` is set automatically over
  HTTPS (honoring `X-Forwarded-Proto`); tokens are compared in constant time.
- **Frames** — the server multiplexes one channel per viewer: binary frames carry
  shell bytes; text frames carry JSON control messages (`role`, `size`). A single
  writer goroutine per connection preserves ordering.
- **Scrollback** — the session keeps a bounded ring of recent raw output
  (`-scrollback-kb`) and replays a snapshot to each viewer on attach.

## Project layout

```
my-term/
├── cmd/myterm/main.go        flags, token setup, graceful shutdown
├── pkg/
│   ├── auth/auth.go          tokens, roles, cookie/role resolution
│   ├── session/session.go    shared shell, scrollback, output broadcast
│   ├── pty/                  ConPTY (Windows) / Unix PTY behind one interface
│   └── server/
│       ├── server.go         routes, auth middleware, /ws bridge
│       ├── login.go          sign-in page
│       ├── web.go            embeds web/ into the binary
│       └── web/              index.html · app.js · style.css
├── go.mod
└── Makefile
```

## Notes & limits

- Scrollback is replayed as raw bytes. For a full-screen TUI (vim, the Claude
  Code UI) the very first repaint after a reconnect may look slightly off until
  the app redraws on the next keypress/resize; a plain shell replays cleanly.
- A spectator mirrors the owner's exact size, so on a small screen a wide session
  may overflow — zoom out in the browser to see it all.

Next: the Cloudflare tunnel for access from anywhere (phase 4), the real
mobile-optimized UI with xterm.js bundled offline (phase 5), and file upload /
download (phase 6).

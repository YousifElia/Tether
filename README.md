# tether — phase 4

A web terminal: run it on your machine, sign in, and drive a shell from any
browser. **Phase 4 of 6** adds a public HTTPS URL so you can reach it from
anywhere — not just your local network.

## What works now

- **Public URL via Cloudflare tunnel** — one flag spins up a `cloudflared` quick
  tunnel and prints an `https://….trycloudflare.com` link (no account, no port
  forwarding), plus a **QR code** you can scan with your phone or iPad camera.
  The tunnel auto-reconnects if it drops, and is shut down cleanly on exit.
- **Sign-in gate** — every page and the websocket require a valid token
- **Two roles** — **owner** (read/write) and **spectator** (read-only; off by default)
- **One shared session** with **scrollback replay** (joiners/reconnects see recent
  history) and **size sync** (spectators mirror the owner's dimensions)
- Cross-platform PTY (ConPTY on Windows, Unix PTY elsewhere) and live resize

## Prerequisites

- Go 1.22 or newer — https://go.dev/dl/
- For the tunnel: `cloudflared` (auto-detected, or let myterm download it)
- Internet on first page load (xterm.js is from a CDN; phase 5 bundles it)

## Secret scanning (recommended)

Local pre-commit hook (blocks accidental secrets):

```
pip install pre-commit
pre-commit install
```

CI secret scan runs on every push and pull request.

## Setup & run (local)

```
go mod tidy
go run ./cmd/myterm
```

Open the printed `http://127.0.0.1:7314`, paste the **owner** token, and you're in.

## Go public (reach it from your iPad anywhere)

```
go run ./cmd/myterm --tunnel
```

After a few seconds you'll see:

```
================================================================
  PUBLIC URL (reachable from anywhere - sign in with your token):

    https://calm-river-1234.trycloudflare.com

    <a scannable QR code of that URL>

  scan the QR with your phone or iPad camera to open it
================================================================
```

Scan the QR on your iPad, paste the owner token, and you have your terminal from
anywhere. Run `claude` in it and you're coding on the iPad against your desktop.

### Getting cloudflared

myterm looks for `cloudflared` on your PATH and in common install locations. If
it isn't installed:

```
# Windows
winget install --id Cloudflare.cloudflared
# macOS
brew install cloudflared
```

Or let myterm fetch the official binary for you (cached for next time):

```
go run ./cmd/myterm --tunnel --install-cloudflared
```

Point at a specific binary with `--cloudflared C:\path\to\cloudflared.exe`.

## Flags

| Flag                   | Default               | Description                                       |
|------------------------|-----------------------|---------------------------------------------------|
| `-addr`                | `127.0.0.1:7314`      | Address to listen on (`host:port`)                |
| `-shell`               | `powershell.exe` / `$SHELL` / `bash` | Shell to launch                    |
| `-owner-token`         | env or auto-generated | Owner (read/write) token                          |
| `-allow-spectators`    | `false`               | Enable read-only spectator access                 |
| `-spectator-token`     | env or auto-generated | Spectator token (only if spectators enabled)      |
| `-scrollback-kb`       | `256`                 | KB of recent output replayed to (re)joiners       |
| `-tunnel`              | `false`               | Expose a public HTTPS URL via a cloudflared tunnel|
| `-cloudflared`         | auto-detected         | Path to the cloudflared binary                    |
| `-install-cloudflared` | `false`               | Download cloudflared automatically if not found   |
| `-qr`                  | `true`                | Print a QR code for the public URL                |

Environment equivalents: `MYTERM_OWNER_TOKEN`, `MYTERM_SPECTATOR_TOKEN`.
Sign out anytime by visiting `/logout`.

## Security when public

When you use `--tunnel`, your terminal is reachable from the public internet, so:

- **The token is the only thing protecting it.** Set a long, stable owner token
  (`MYTERM_OWNER_TOKEN`) before going public — don't rely on the random URL being
  secret (it appears in logs and isn't a credential).
- **Don't enable `--allow-spectators` on a public tunnel** unless you intend for
  others to watch; share only the spectator token, never the owner token.
- Auth rides a `SameSite=Strict`, `HttpOnly` cookie and the `Secure` flag is set
  automatically over the HTTPS tunnel (via `X-Forwarded-Proto`), which is what
  blocks cross-site WebSocket hijacking.
- Quit with Ctrl+C — myterm kills the shell and the tunnel before exiting.

## Notes & limits

- **Quick-tunnel URLs are temporary.** Each run (and each auto-reconnect) gets a
  new random hostname, and because the auth cookie is bound to the hostname, a
  changed URL means signing in again. For a permanent URL, use a named Cloudflare
  tunnel with an account (not built in yet).
- Scrollback is replayed as raw bytes; a full-screen TUI may need one keypress to
  repaint after a reconnect. The QR is most reliable on a dark terminal; the URL
  is always printed too (`-qr=false` to hide it).

## Project layout

```
my-term/
├── cmd/myterm/main.go        flags, tunnel wiring, ordered shutdown
├── pkg/
│   ├── auth/auth.go          tokens, roles, cookie/role resolution
│   ├── session/session.go    shared shell, scrollback, output broadcast
│   ├── pty/                  ConPTY (Windows) / Unix PTY behind one interface
│   ├── tunnel/               cloudflared supervisor: spawn, parse URL, restart, install
│   └── server/
│       ├── server.go         routes, auth middleware, /ws bridge
│       ├── login.go          sign-in page
│       ├── web.go            embeds web/ into the binary
│       └── web/              index.html · app.js · style.css
├── go.mod
└── Makefile
```

Next: a real mobile-optimized UI with xterm.js bundled offline (phase 5), then
file upload / download (phase 6).

# myterm — phase 2

A web terminal: run it on your machine, sign in, and drive a shell from any
browser. **Phase 2 of 6** adds authentication and roles on top of phase 1.

## What works now

- **Sign-in gate** — every page and the websocket require a valid token
- **Two roles**
  - **owner** — full read/write control of the shell
  - **spectator** — read-only; sees the live terminal but cannot type (off by default)
- **One shared session** — the owner drives a single shell; spectators watch the
  *same* terminal. The shell persists across owner reconnects.
- Cross-platform PTY (ConPTY on Windows, Unix PTY elsewhere) and live resize

## Prerequisites

- Go 1.22 or newer — https://go.dev/dl/
- Internet on first page load (xterm.js is pulled from a CDN; phase 5 bundles it)

## Setup

```
go mod tidy
```

## Secret scanning (recommended)

Local pre-commit hook (blocks accidental secrets):

```
pip install pre-commit
pre-commit install
```

CI secret scan runs on every push and pull request.

## Run it

```
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

Auto-generated tokens change on every restart. Set your own so you don't
re-login each time. Use environment variables (they don't show up in the process
list the way command-line flags do):

PowerShell:

```powershell
$env:MYTERM_OWNER_TOKEN = "pick-a-long-random-string"
.\myterm.exe
```

macOS / Linux:

```
export MYTERM_OWNER_TOKEN=pick-a-long-random-string
./myterm
```

## Sharing read-only access (spectators)

Spectators are **off by default**. Enable them with `--allow-spectators`:

```
myterm.exe --allow-spectators
```

This prints a separate spectator token. Anyone who signs in with it sees your
live terminal but cannot type. Set a stable one with `MYTERM_SPECTATOR_TOKEN`
or `--spectator-token`. Keep the owner token private; share only the spectator
token for view-only access.

## Flags

| Flag                 | Default               | Description                                  |
|----------------------|-----------------------|----------------------------------------------|
| `-addr`              | `127.0.0.1:7314`      | Address to listen on (`host:port`)           |
| `-shell`             | `powershell.exe` / `$SHELL` / `bash` | Shell to launch               |
| `-owner-token`       | env or auto-generated | Owner (read/write) token                     |
| `-allow-spectators`  | `false`               | Enable read-only spectator access            |
| `-spectator-token`   | env or auto-generated | Spectator token (only if spectators enabled) |

Environment equivalents: `MYTERM_OWNER_TOKEN`, `MYTERM_SPECTATOR_TOKEN`.

Sign out anytime by visiting `/logout`.

## How auth works (and why)

- The token is stored in a `SameSite=Strict`, `HttpOnly` cookie. `SameSite=Strict`
  is the key protection against **cross-site WebSocket hijacking**: a malicious
  page can't attach your cookie to its own socket, so the server rejects it.
- The `Secure` flag is set automatically when the request arrives over HTTPS
  (e.g. through the phase-4 tunnel), honoring `X-Forwarded-Proto`.
- Tokens are compared in constant time.

## Build a binary

```
# Windows
go build -o myterm.exe ./cmd/myterm
# macOS / Linux
go build -o myterm ./cmd/myterm
# cross-compile a Windows .exe from elsewhere
GOOS=windows GOARCH=amd64 go build -o dist/myterm.exe ./cmd/myterm
```

## Project layout

```
my-term/
├── cmd/myterm/main.go        flags, token setup, graceful shutdown
├── pkg/
│   ├── auth/auth.go          tokens, roles, cookie/role resolution
│   ├── session/session.go    one shared shell, output broadcast to viewers
│   ├── pty/                  ConPTY (Windows) / Unix PTY behind one interface
│   └── server/
│       ├── server.go         routes, auth middleware, /ws bridge
│       ├── login.go          sign-in page
│       ├── web.go            embeds web/ into the binary
│       └── web/              index.html · app.js · style.css
├── go.mod
└── Makefile
```

## Known limitations (addressed in phase 3)

- A spectator who joins mid-session sees only output from that point on — there's
  no scrollback replay yet. Reconnecting the owner also starts with a blank view
  until the program on screen repaints.
- Spectators render at their own window size, so a spectator whose window differs
  from the owner's may see slightly misaligned output.

Phase 3 adds a scrollback buffer (so late joiners and reconnects see recent
history) and size handling. Then: the Cloudflare tunnel (4), the real mobile UI
(5), and file transfer (6).

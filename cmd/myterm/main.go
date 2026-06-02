// Command myterm serves a shell as an authenticated web terminal over WebSocket,
// with an owner (read/write) role and an optional read-only spectator role, and
// can expose itself on a public HTTPS URL via a cloudflared quick tunnel.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"myterm/pkg/auth"
	"myterm/pkg/files"
	"myterm/pkg/server"
	"myterm/pkg/session"
	"myterm/pkg/tunnel"
)

func defaultShell() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "bash"
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7314", "address to listen on (host:port)")
	shell := flag.String("shell", defaultShell(), "shell executable to launch")
	ownerFlag := flag.String("owner-token", "", "owner (read/write) token; or env MYTERM_OWNER_TOKEN; auto-generated if unset")
	allowSpectators := flag.Bool("allow-spectators", false, "enable read-only spectator access")
	spectatorFlag := flag.String("spectator-token", "", "spectator token; or env MYTERM_SPECTATOR_TOKEN; auto-generated if --allow-spectators and unset")
	scrollbackKB := flag.Int("scrollback-kb", 256, "kilobytes of recent output retained for replay to (re)joining viewers")
	tunnelOn := flag.Bool("tunnel", false, "expose a public HTTPS URL via a cloudflared quick tunnel")
	cfPath := flag.String("cloudflared", "", "path to the cloudflared binary (auto-detected if empty)")
	installCf := flag.Bool("install-cloudflared", false, "download cloudflared automatically if it isn't found")
	showQR := flag.Bool("qr", true, "print a QR code for the public tunnel URL")
	shareDir := flag.String("share-dir", "", "directory to share for file upload/download (file transfer is disabled when empty)")
	maxUploadMB := flag.Int("max-upload-mb", 100, "maximum size, in MB, of a single uploaded file")
	flag.Parse()

	ownerToken := firstNonEmpty(*ownerFlag, os.Getenv("MYTERM_OWNER_TOKEN"))
	autoOwner := ownerToken == ""
	if autoOwner {
		ownerToken = auth.GenerateToken()
	}

	spectatorToken := ""
	if *allowSpectators {
		spectatorToken = firstNonEmpty(*spectatorFlag, os.Getenv("MYTERM_SPECTATOR_TOKEN"))
		if spectatorToken == "" {
			spectatorToken = auth.GenerateToken()
		}
	}

	authr := auth.New(ownerToken, spectatorToken)
	sess := session.New(*shell, nil, *scrollbackKB*1024)

	fileMgr := buildFileManager(*shareDir, *maxUploadMB)

	srv := server.New(server.Config{Auth: authr, Session: sess, Files: fileMgr})
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Routes()}

	// ctx is cancelled on Ctrl+C; it also stops the tunnel.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	log.Printf("web terminal listening on http://%s", *addr)
	log.Printf("shell: %s", *shell)
	printBanner(*addr, ownerToken, spectatorToken, autoOwner, *allowSpectators, fileMgr)

	// The tunnel goroutine (if any) closes tunnelDone when it has fully stopped,
	// i.e. after cloudflared has been killed and reaped. Shutdown waits on it so
	// we never exit leaving an orphaned cloudflared process behind.
	tunnelDone := make(chan struct{})
	if *tunnelOn {
		startTunnel(ctx, *addr, *cfPath, *installCf, *showQR, tunnelDone)
	} else {
		close(tunnelDone)
	}

	serverErr := make(chan error, 1)
	go func() {
		err := httpSrv.ListenAndServe()
		if err == http.ErrServerClosed {
			err = nil
		}
		serverErr <- err
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down")
	case err := <-serverErr:
		if err != nil {
			log.Printf("server error: %v", err)
		}
		stop()
	}

	// Ordered teardown: kill the shell, stop accepting HTTP, then make sure the
	// tunnel child is gone before the process exits.
	sess.Close()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = httpSrv.Shutdown(shutCtx)
	shutCancel()

	select {
	case <-tunnelDone:
	case <-time.After(3 * time.Second):
		log.Println("tunnel did not stop in time")
	}
}

func startTunnel(ctx context.Context, addr, cfPath string, install, qr bool, done chan struct{}) {
	bin, err := tunnel.Find(cfPath)
	if err != nil && install {
		log.Printf("cloudflared not found; downloading the official binary...")
		if bin, err = tunnel.Install(tunnel.DefaultInstallDir()); err == nil {
			log.Printf("cloudflared installed at %s", bin)
		}
	}
	if err != nil {
		printCloudflaredHelp(err)
		close(done) // nothing to wait for
		return
	}
	mgr := tunnel.New(bin, tunnel.Target(addr), func(u string) { printPublicURL(u, qr) }, log.Printf)
	go func() {
		mgr.Run(ctx)
		close(done)
	}()
	log.Printf("starting cloudflared tunnel (a public URL will appear in a few seconds)")
}

func printPublicURL(u string, qr bool) {
	line := strings.Repeat("=", 64)
	var b strings.Builder
	b.WriteString("\n" + line + "\n")
	b.WriteString("  PUBLIC URL (reachable from anywhere - sign in with your token):\n\n")
	b.WriteString("    " + u + "\n")
	if qr {
		if code, err := qrcode.New(u, qrcode.Medium); err == nil {
			code.DisableBorder = false
			b.WriteString("\n" + indent(code.ToSmallString(false), "    ") + "\n")
			b.WriteString("  scan the QR with your phone or iPad camera to open it\n")
		}
	}
	b.WriteString(line + "\n")
	fmt.Fprint(os.Stderr, b.String())
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func printCloudflaredHelp(err error) {
	log.Printf("tunnel unavailable: %v", err)
	var b strings.Builder
	b.WriteString("\nTo expose a public URL, install cloudflared and re-run with --tunnel:\n")
	switch runtime.GOOS {
	case "windows":
		b.WriteString("  winget install --id Cloudflare.cloudflared\n")
		b.WriteString("  (or:  choco install cloudflared)\n")
	case "darwin":
		b.WriteString("  brew install cloudflared\n")
	default:
		b.WriteString("  https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/\n")
	}
	b.WriteString("Or let myterm fetch it for you:  --tunnel --install-cloudflared\n")
	b.WriteString("The terminal is still available locally at the address above.\n")
	fmt.Fprint(os.Stderr, b.String())
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func printBanner(addr, owner, spectator string, autoOwner, spectatorsOn bool, fm *files.Manager) {
	line := strings.Repeat("-", 64)
	fmt.Fprintln(os.Stderr, "\n"+line)
	fmt.Fprintf(os.Stderr, "  open   http://%s   and paste a token to sign in:\n\n", addr)
	note := ""
	if autoOwner {
		note = "   (auto-generated; set MYTERM_OWNER_TOKEN for a stable token)"
	}
	fmt.Fprintf(os.Stderr, "    owner  (read / write):  %s%s\n", owner, note)
	if spectatorsOn {
		fmt.Fprintf(os.Stderr, "    spectator (read-only):  %s\n", spectator)
	} else {
		fmt.Fprintf(os.Stderr, "    spectator (read-only):  disabled  (use --allow-spectators)\n")
	}
	if fm != nil {
		fmt.Fprintf(os.Stderr, "    file transfer:          on  -  sharing %s\n", fm.Dir())
	} else {
		fmt.Fprintf(os.Stderr, "    file transfer:          disabled  (use --share-dir DIR)\n")
	}
	fmt.Fprintln(os.Stderr, line+"\n")
}

// buildFileManager validates the --share-dir path and returns a files.Manager,
// or nil when no directory was given. A bad path is fatal: the user explicitly
// asked to share a directory, so silently disabling the feature would be worse
// than failing loudly.
func buildFileManager(shareDir string, maxUploadMB int) *files.Manager {
	if shareDir == "" {
		return nil
	}
	abs, err := filepath.Abs(shareDir)
	if err != nil {
		log.Fatalf("--share-dir %q: %v", shareDir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		log.Fatalf("--share-dir %q: %v", abs, err)
	}
	if !info.IsDir() {
		log.Fatalf("--share-dir %q is not a directory", abs)
	}
	if maxUploadMB < 1 {
		log.Fatalf("--max-upload-mb must be at least 1 (got %d)", maxUploadMB)
	}
	log.Printf("file transfer enabled: sharing %s (max upload %d MB)", abs, maxUploadMB)
	return files.New(abs, int64(maxUploadMB)<<20)
}

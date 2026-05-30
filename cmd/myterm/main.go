// Command myterm serves a shell as an authenticated web terminal over WebSocket,
// with an owner (read/write) role and an optional read-only spectator role.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"myterm/pkg/auth"
	"myterm/pkg/server"
	"myterm/pkg/session"
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
	sess := session.New(*shell, nil)
	srv := server.New(server.Config{Auth: authr, Session: sess})

	httpSrv := &http.Server{Addr: *addr, Handler: srv.Routes()}

	// Kill the shell and shut down cleanly on Ctrl+C.
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, os.Interrupt)
		<-sigs
		log.Println("shutting down")
		sess.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	log.Printf("web terminal listening on http://%s", *addr)
	log.Printf("shell: %s", *shell)
	printBanner(*addr, ownerToken, spectatorToken, autoOwner, *allowSpectators)

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func printBanner(addr, owner, spectator string, autoOwner, spectatorsOn bool) {
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
	fmt.Fprintln(os.Stderr, line+"\n")
}

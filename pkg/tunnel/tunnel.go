// Package tunnel manages a cloudflared quick tunnel: it launches cloudflared as a
// child process, extracts the public https URL it reports, and restarts it with
// backoff if it exits, until the supervising context is cancelled. It can also
// locate an installed cloudflared or download the official binary on request.
package tunnel

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var urlRE = regexp.MustCompile(`https://[a-zA-Z0-9][a-zA-Z0-9-]*\.trycloudflare\.com`)

// ErrNotFound is returned by Find when no cloudflared binary can be located.
var ErrNotFound = errors.New("cloudflared not found")

// Manager supervises a cloudflared quick tunnel.
type Manager struct {
	bin    string
	target string
	onURL  func(string)
	logf   func(string, ...any)
}

// New returns a Manager that tunnels to target (e.g. "http://127.0.0.1:7314")
// using the cloudflared binary at bin. onURL is invoked with each public URL the
// tunnel reports (the URL changes whenever a quick tunnel restarts). logf
// receives human-readable status lines; pass nil to discard them.
func New(bin, target string, onURL func(string), logf func(string, ...any)) *Manager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if onURL == nil {
		onURL = func(string) {}
	}
	return &Manager{bin: bin, target: target, onURL: onURL, logf: logf}
}

// Run supervises the tunnel until ctx is cancelled. It blocks, so run it in a
// goroutine. Each time cloudflared exits unexpectedly it is restarted after an
// exponential backoff, which resets once a tunnel has stayed up for a while.
func (m *Manager) Run(ctx context.Context) {
	const (
		minBackoff = time.Second
		maxBackoff = 15 * time.Second
		healthyFor = 30 * time.Second
	)
	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		err := m.runOnce(ctx)
		if ctx.Err() != nil {
			return // shutting down: a clean stop, not a failure
		}
		if time.Since(start) >= healthyFor {
			backoff = minBackoff
		}
		m.logf("tunnel stopped (%v) - reconnecting in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

// runOnce launches cloudflared once and blocks until it exits.
func (m *Manager) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, m.bin, "tunnel", "--no-autoupdate", "--url", m.target)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting cloudflared: %w", err)
	}

	var found atomic.Bool
	tail := &ring{n: 25}
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			tail.add(line)
			if u := urlRE.FindString(line); u != "" && found.CompareAndSwap(false, true) {
				m.onURL(u)
			}
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scan(stdout) }()
	go func() { defer wg.Done(); scan(stderr) }()

	// Drain both pipes to EOF (process has closed them) before reaping: calling
	// Wait first would close the pipes and could truncate in-flight reads.
	wg.Wait()
	waitErr := cmd.Wait()

	if !found.Load() {
		if out := tail.join(); out != "" {
			return fmt.Errorf("%v; recent cloudflared output:\n%s", waitErr, out)
		}
		if waitErr != nil {
			return waitErr
		}
		return errors.New("cloudflared exited before reporting a URL")
	}
	return waitErr
}

// ring is a small fixed-size, concurrency-safe buffer of the most recent lines.
type ring struct {
	mu  sync.Mutex
	buf []string
	n   int
}

func (r *ring) add(s string) {
	r.mu.Lock()
	r.buf = append(r.buf, s)
	if len(r.buf) > r.n {
		r.buf = r.buf[len(r.buf)-r.n:]
	}
	r.mu.Unlock()
}

func (r *ring) join() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.buf, "\n")
}

// Target converts a listen address into a cloudflared --url origin, mapping
// wildcard/empty hosts to loopback (cloudflared connects to the origin locally).
func Target(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// Find locates a cloudflared binary: an explicit path if given and present,
// otherwise PATH, otherwise common install locations and the myterm cache.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if isFile(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("%w at %s", ErrNotFound, explicit)
	}
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	for _, c := range commonPaths() {
		if isFile(c) {
			return c, nil
		}
	}
	return "", ErrNotFound
}

func commonPaths() []string {
	cached := filepath.Join(cacheDir(), binName())
	if runtime.GOOS == "windows" {
		out := []string{filepath.Join(".", "cloudflared.exe"), cached}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			out = append(out, filepath.Join(pf, "cloudflared", "cloudflared.exe"))
		}
		if up := os.Getenv("USERPROFILE"); up != "" {
			out = append(out, filepath.Join(up, ".cloudflared", "cloudflared.exe"))
		}
		return out
	}
	return []string{
		"./cloudflared",
		cached,
		"/usr/local/bin/cloudflared",
		"/usr/bin/cloudflared",
		"/opt/homebrew/bin/cloudflared",
	}
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// --- optional automatic install of the official binary ---

const releaseBase = "https://github.com/cloudflare/cloudflared/releases/latest/download/"

func cacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil || d == "" {
		d = os.TempDir()
	}
	return filepath.Join(d, "myterm")
}

// DefaultInstallDir is where Install places cloudflared when called from main.
func DefaultInstallDir() string { return cacheDir() }

// Install downloads the official cloudflared for the current platform into
// destDir and returns its path. Requires network access.
func Install(destDir string) (string, error) { return installFrom(releaseBase, destDir) }

func installFrom(base, destDir string) (string, error) {
	asset, isTgz, err := assetName()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(destDir, binName())
	if err := download(base+asset, dst, isTgz); err != nil {
		return "", err
	}
	return dst, nil
}

func binName() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

func assetName() (name string, isTgz bool, err error) {
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", false, fmt.Errorf("automatic install unsupported for arch %s; install cloudflared manually", arch)
	}
	switch runtime.GOOS {
	case "windows":
		return "cloudflared-windows-" + arch + ".exe", false, nil
	case "linux":
		return "cloudflared-linux-" + arch, false, nil
	case "darwin":
		return "cloudflared-darwin-" + arch + ".tgz", true, nil
	}
	return "", false, fmt.Errorf("automatic install unsupported for OS %s; install cloudflared manually", runtime.GOOS)
}

func download(url, dst string, isTgz bool) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	if isTgz {
		return extractTgz(resp.Body, dst)
	}
	return writeExecutable(resp.Body, dst)
}

func writeExecutable(r io.Reader, dst string) (err error) {
	f, ferr := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if ferr != nil {
		return ferr
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(f, r)
	return err
}

// extractTgz writes the first regular file from a gzipped tar to dst.
func extractTgz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return errors.New("archive contained no file")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg {
			return writeExecutable(tr, dst)
		}
	}
}

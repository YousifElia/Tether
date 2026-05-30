package tunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakecf.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTarget(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:7314": "http://127.0.0.1:7314",
		":7314":          "http://127.0.0.1:7314",
		"0.0.0.0:7314":   "http://127.0.0.1:7314",
		"192.168.1.5:80": "http://192.168.1.5:80",
	}
	for in, want := range cases {
		if got := Target(in); got != want {
			t.Errorf("Target(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindExplicitMissing(t *testing.T) {
	if _, err := Find(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing explicit binary")
	}
}

func TestManagerParsesURLAndRestarts(t *testing.T) {
	fake := writeScript(t, "#!/bin/sh\n"+
		"echo 'INF |  https://unit-test-xyz.trycloudflare.com  |' 1>&2\n"+
		"sleep 1\nexit 1\n")

	var calls atomic.Int64
	var last atomic.Value
	m := New(fake, "http://127.0.0.1:9", func(u string) {
		calls.Add(1)
		last.Store(u)
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	time.Sleep(4500 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after cancel")
	}
	if calls.Load() < 2 {
		t.Fatalf("expected >= 2 URL detections across restarts, got %d", calls.Load())
	}
	if got, _ := last.Load().(string); got != "https://unit-test-xyz.trycloudflare.com" {
		t.Fatalf("unexpected URL %q", got)
	}
}

func TestRunOnceReportsOutputWhenNoURL(t *testing.T) {
	fake := writeScript(t, "#!/bin/sh\necho 'ERR boom went the tunnel' 1>&2\nexit 7\n")
	m := New(fake, "http://127.0.0.1:9", nil, nil)
	err := m.runOnce(context.Background())
	if err == nil {
		t.Fatal("expected error when no URL is reported")
	}
	if !strings.Contains(err.Error(), "boom went the tunnel") {
		t.Fatalf("error should include recent output, got: %v", err)
	}
}

func TestDownloadDirect(t *testing.T) {
	want := []byte("#!/bin/sh\necho i-am-cloudflared\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), binName())
	if err := download(srv.URL+"/cloudflared", dst, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("downloaded content mismatch")
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("expected executable bit, got %v", fi.Mode())
	}
}

func makeTgz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestDownloadTgz(t *testing.T) {
	content := []byte("fake cloudflared mach-o bytes")
	tgz := makeTgz(t, "cloudflared", content)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tgz)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "cloudflared")
	if err := download(srv.URL+"/cf.tgz", dst, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("extracted content mismatch")
	}
}

func TestInstallFromLocal(t *testing.T) {
	asset, isTgz, err := assetName()
	if err != nil {
		t.Skip("platform unsupported:", err)
	}
	if isTgz {
		t.Skip("tgz platform; covered by TestDownloadTgz")
	}
	want := []byte("fake-cloudflared-binary")
	mux := http.NewServeMux()
	mux.HandleFunc("/"+asset, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(want) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	path, err := installFrom(srv.URL+"/", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, want) {
		t.Fatal("install content mismatch")
	}
}

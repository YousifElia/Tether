package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"myterm/pkg/auth"
	"myterm/pkg/files"
	"myterm/pkg/session"
)

const (
	testOwnerToken = "owner-token-abc"
	testSpecToken  = "spectator-token-xyz"
)

// newFileServer returns a Server with file transfer enabled over a fresh temp
// dir, plus the temp dir path so tests can inspect it.
func newFileServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	srv := New(Config{
		Auth:    auth.New(testOwnerToken, testSpecToken),
		Session: session.New("/bin/sh", nil, 4096),
		Files:   files.New(dir, 1<<20), // 1 MiB cap
	})
	return srv, dir
}

// do executes a request against the server's mux with the given role's cookie
// (pass "" for no cookie / unauthenticated).
func do(t *testing.T, srv *Server, req *http.Request, token string) *httptest.ResponseRecorder {
	t.Helper()
	if token != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// multipartUpload builds a POST /api/upload request carrying the named files.
func multipartUpload(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := mw.CreateFormFile(uploadField, name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(fw, content)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestListRequiresAuth(t *testing.T) {
	srv, _ := newFileServer(t)
	rec := do(t, srv, httptest.NewRequest(http.MethodGet, "/api/files", nil), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestListEmptyOwner(t *testing.T) {
	srv, _ := newFileServer(t)
	rec := do(t, srv, httptest.NewRequest(http.MethodGet, "/api/files", nil), testOwnerToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Files     []files.Entry `json:"files"`
		MaxUpload int64         `json:"maxUpload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Files) != 0 {
		t.Errorf("want empty list, got %d", len(resp.Files))
	}
	if resp.MaxUpload != 1<<20 {
		t.Errorf("maxUpload = %d, want %d", resp.MaxUpload, 1<<20)
	}
}

func TestUploadOwnerRoundTrip(t *testing.T) {
	srv, _ := newFileServer(t)

	// Upload.
	rec := do(t, srv, multipartUpload(t, map[string]string{"hello.txt": "hi there"}), testOwnerToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var up struct {
		Uploaded []string `json:"uploaded"`
		Failed   []any    `json:"failed"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &up)
	if len(up.Uploaded) != 1 || up.Uploaded[0] != "hello.txt" {
		t.Fatalf("uploaded = %v, want [hello.txt]", up.Uploaded)
	}

	// It now appears in the listing.
	rec = do(t, srv, httptest.NewRequest(http.MethodGet, "/api/files", nil), testOwnerToken)
	if !strings.Contains(rec.Body.String(), "hello.txt") {
		t.Fatalf("listing missing hello.txt: %s", rec.Body)
	}

	// And it downloads with the original content.
	rec = do(t, srv, httptest.NewRequest(http.MethodGet, "/api/download/hello.txt", nil), testOwnerToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hi there" {
		t.Errorf("download body = %q, want %q", rec.Body.String(), "hi there")
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "hello.txt") {
		t.Errorf("Content-Disposition = %q, want it to name hello.txt", cd)
	}
}

func TestUploadSpectatorForbidden(t *testing.T) {
	srv, dir := newFileServer(t)
	rec := do(t, srv, multipartUpload(t, map[string]string{"x.txt": "data"}), testSpecToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	// Nothing should have been written.
	if entries, _ := files.New(dir, 1<<20).List(); len(entries) != 0 {
		t.Errorf("spectator upload wrote %d files", len(entries))
	}
}

func TestSpectatorCanListAndDownload(t *testing.T) {
	srv, _ := newFileServer(t)
	// Owner uploads a file first.
	do(t, srv, multipartUpload(t, map[string]string{"shared.txt": "readable"}), testOwnerToken)

	// Spectator can list.
	rec := do(t, srv, httptest.NewRequest(http.MethodGet, "/api/files", nil), testSpecToken)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "shared.txt") {
		t.Fatalf("spectator list failed: %d %s", rec.Code, rec.Body)
	}
	// Spectator can download.
	rec = do(t, srv, httptest.NewRequest(http.MethodGet, "/api/download/shared.txt", nil), testSpecToken)
	if rec.Code != http.StatusOK || rec.Body.String() != "readable" {
		t.Fatalf("spectator download failed: %d %q", rec.Code, rec.Body.String())
	}
}

func TestDeleteOwnerAndSpectator(t *testing.T) {
	srv, _ := newFileServer(t)
	do(t, srv, multipartUpload(t, map[string]string{"temp.txt": "delete me"}), testOwnerToken)

	// Spectator cannot delete.
	rec := do(t, srv, httptest.NewRequest(http.MethodPost, "/api/delete/temp.txt", nil), testSpecToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spectator delete status = %d, want 403", rec.Code)
	}

	// Owner can delete.
	rec = do(t, srv, httptest.NewRequest(http.MethodPost, "/api/delete/temp.txt", nil), testOwnerToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner delete status = %d, want 200", rec.Code)
	}

	// Deleting again is a 404.
	rec = do(t, srv, httptest.NewRequest(http.MethodPost, "/api/delete/temp.txt", nil), testOwnerToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", rec.Code)
	}
}

func TestDownloadTraversalRejected(t *testing.T) {
	srv, _ := newFileServer(t)
	// Encoded so the literal "../" survives to the handler rather than being
	// collapsed by the client; httptest keeps the raw path.
	req := httptest.NewRequest(http.MethodGet, "/api/download/..%2f..%2fmain.go", nil)
	rec := do(t, srv, req, testOwnerToken)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("traversal download status = %d, want 400 or 404", rec.Code)
	}
}

func TestUploadOversizeRejected(t *testing.T) {
	dir := t.TempDir()
	srv := New(Config{
		Auth:    auth.New(testOwnerToken, testSpecToken),
		Session: session.New("/bin/sh", nil, 4096),
		Files:   files.New(dir, 8), // 8-byte cap
	})
	rec := do(t, srv, multipartUpload(t, map[string]string{"big.txt": "way too many bytes"}), testOwnerToken)
	// All files failed, so the batch reports 400.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "size limit") {
		t.Errorf("body = %s, want a size-limit reason", rec.Body)
	}
}

func TestRoutesAbsentWhenDisabled(t *testing.T) {
	// No Files manager: the API routes must not exist. Unauthenticated so the
	// catch-all redirects to /login (303) rather than serving anything.
	srv := New(Config{
		Auth:    auth.New(testOwnerToken, testSpecToken),
		Session: session.New("/bin/sh", nil, 4096),
	})
	rec := do(t, srv, httptest.NewRequest(http.MethodGet, "/api/files", nil), testOwnerToken)
	// With file transfer off, /api/files falls through to the static file
	// server, which has no such file -> 404.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when file transfer disabled", rec.Code)
	}
}

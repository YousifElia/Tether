package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"myterm/pkg/auth"
	"myterm/pkg/files"
)

// uploadField is the multipart form field the browser uses for file uploads.
const uploadField = "files"

// handleListFiles serves GET /api/files. Any authenticated role may list.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if s.authRole(w, r) == auth.RoleNone {
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	entries, err := s.files.List()
	if err != nil {
		log.Printf("list files: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "could not read shared directory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":     entries,
		"maxUpload": s.files.MaxUpload(),
	})
}

// handleDownload serves GET /api/download/<name>. Any authenticated role may
// download.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if s.authRole(w, r) == auth.RoleNone {
		return
	}
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/download/")
	f, info, err := s.files.Open(name)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer f.Close()

	ctype := mime.TypeByExtension(filepath.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", contentDisposition(name))
	// Defense in depth: never let a browser sniff a download into executable
	// content, and keep shared files out of shared caches.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if _, err := io.Copy(w, f); err != nil {
		log.Printf("download %q: %v", name, err) // client likely disconnected
	}
}

// handleUpload serves POST /api/upload. Owner only. It accepts one or more
// files in the "files" multipart field and reports per-file success/failure so
// a partial batch still tells the client exactly what landed.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	role := s.authRole(w, r)
	if role == auth.RoleNone {
		return
	}
	if role != auth.RoleOwner {
		writeJSONError(w, http.StatusForbidden, "read-only: uploads require the owner role")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 32 MiB of the request is buffered in memory; the rest spills to temp
	// files that ParseMultipartForm cleans up when the request ends.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "could not parse upload")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	headers := r.MultipartForm.File[uploadField]
	if len(headers) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no files in upload")
		return
	}

	maxUpload := s.files.MaxUpload()
	uploaded := make([]string, 0, len(headers))
	type failure struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	failed := make([]failure, 0)

	for _, fh := range headers {
		name := files.CleanName(fh.Filename)
		// Reject before reading the body when the part's declared size already
		// exceeds the cap, so we don't copy a doomed multi-megabyte upload.
		if maxUpload > 0 && fh.Size > maxUpload {
			failed = append(failed, failure{name, "exceeds size limit"})
			continue
		}
		src, err := fh.Open()
		if err != nil {
			failed = append(failed, failure{name, "could not read upload"})
			continue
		}
		_, err = s.files.Save(name, src)
		_ = src.Close()
		switch {
		case err == nil:
			uploaded = append(uploaded, name)
		case errors.Is(err, files.ErrTooLarge):
			failed = append(failed, failure{name, "exceeds size limit"})
		case errors.Is(err, files.ErrInvalidName):
			failed = append(failed, failure{name, "invalid file name"})
		default:
			log.Printf("save upload %q: %v", name, err)
			failed = append(failed, failure{name, "could not save"})
		}
	}

	status := http.StatusOK
	if len(uploaded) == 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"uploaded": uploaded, "failed": failed})
}

// handleDelete serves POST /api/delete/<name>. Owner only. Deletion is
// permanent — there is no trash.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	role := s.authRole(w, r)
	if role == auth.RoleNone {
		return
	}
	if role != auth.RoleOwner {
		writeJSONError(w, http.StatusForbidden, "read-only: deletion requires the owner role")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/delete/")
	if err := s.files.Delete(name); err != nil {
		writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}

// authRole resolves the request's role, writing a 401 JSON response and
// returning RoleNone when the request is unauthenticated. Callers return early
// on RoleNone.
func (s *Server) authRole(w http.ResponseWriter, r *http.Request) auth.Role {
	role := s.auth.RoleFromRequest(r)
	if role == auth.RoleNone {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
	}
	return role
}

// writeFileError maps a files package error to an HTTP JSON response.
func writeFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrInvalidName):
		writeJSONError(w, http.StatusBadRequest, "invalid file name")
	case errors.Is(err, files.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "file not found")
	default:
		log.Printf("file error: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "file error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// contentDisposition builds an attachment header that works for both ASCII and
// non-ASCII names: a quoted ASCII fallback plus an RFC 5987 filename* for
// modern browsers.
func contentDisposition(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		ascii, url.PathEscape(name))
}

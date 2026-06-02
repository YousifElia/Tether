// Package files implements a sandboxed view of a single directory that the web
// terminal optionally shares for upload and download (Phase 6). It is the one
// place where untrusted file names are validated, so the path-traversal guard
// cannot be accidentally bypassed by an HTTP handler: every operation routes
// through validateName before touching the filesystem.
//
// The shared directory is flat — subdirectories are listed for neither
// browsing nor traversal. Uploads are written to a hidden temp file and then
// renamed into place, so a partially-received upload is never visible to List
// and an overwrite is atomic.
package files

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Sentinel errors returned by Manager. Callers map these to HTTP status codes.
var (
	// ErrInvalidName is returned for a name that is not a single, visible path
	// element (contains a separator, is "." or "..", starts with a dot, or
	// contains a NUL byte).
	ErrInvalidName = errors.New("files: invalid file name")
	// ErrTooLarge is returned by Save when the upload exceeds the size cap.
	ErrTooLarge = errors.New("files: file exceeds size limit")
	// ErrNotFound is returned by Open and Delete when the file is absent. It
	// wraps os.ErrNotExist so errors.Is(err, os.ErrNotExist) also holds.
	ErrNotFound = os.ErrNotExist
)

// tempPrefix marks in-progress uploads. The leading dot makes them hidden, so
// List skips them and a finished file (which can never start with a dot) never
// collides with one.
const tempPrefix = ".myterm-upload-"

// Entry describes one file in the shared directory. JSON tags match the shape
// the browser expects from GET /api/files.
type Entry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // modification time, Unix seconds
}

// Manager is a sandboxed accessor for one directory.
type Manager struct {
	dir       string
	maxUpload int64 // per-file upload cap in bytes
}

// New returns a Manager for an already-validated absolute directory. maxUpload
// is the per-file upload cap in bytes; a value <= 0 disables the cap. New also
// sweeps any stale upload temp files left by a previous crash.
func New(dir string, maxUpload int64) *Manager {
	m := &Manager{dir: dir, maxUpload: maxUpload}
	m.sweepTemps()
	return m
}

// Dir returns the shared directory path.
func (m *Manager) Dir() string { return m.dir }

// MaxUpload returns the per-file upload cap in bytes (0 means unlimited).
func (m *Manager) MaxUpload() int64 { return m.maxUpload }

// validateName enforces that name is a single, visible path element. This is
// the security boundary: base==name rejects "a/b" and "..", the dot check
// rejects dotfiles (and our own temp files), and the explicit separator/NUL
// check defends on every OS regardless of filepath.Base's platform rules.
func validateName(name string) error {
	if name == "" || name != filepath.Base(name) {
		return ErrInvalidName
	}
	if strings.HasPrefix(name, ".") {
		return ErrInvalidName
	}
	if strings.ContainsAny(name, `/\`+"\x00") {
		return ErrInvalidName
	}
	return nil
}

// CleanName reduces a browser-supplied filename (which may carry a relative
// path, with either separator) to its final element so it can be validated.
// Handlers call this before Save; it is a usability nicety, not the security
// boundary — Save still validates the result.
func CleanName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	return path.Base(name)
}

// List returns the visible regular files in the shared directory, sorted by
// name. Subdirectories, symlinks to directories, hidden files, and in-progress
// upload temp files are omitted.
func (m *Manager) List() ([]Entry, error) {
	des, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue // hidden files and our temp uploads
		}
		if !de.Type().IsRegular() {
			continue // directories, symlinks, devices, etc.
		}
		info, err := de.Info()
		if err != nil {
			continue // file vanished between ReadDir and Info; skip it
		}
		entries = append(entries, Entry{
			Name:     name,
			Size:     info.Size(),
			Modified: info.ModTime().Unix(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// Open opens a shared file for reading and returns it with its FileInfo. The
// caller is responsible for closing the returned file. A name that resolves to
// a directory is treated as not found.
func (m *Manager) Open(name string) (*os.File, os.FileInfo, error) {
	if err := validateName(name); err != nil {
		return nil, nil, err
	}
	full := filepath.Join(m.dir, name)
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, err // already wraps os.ErrNotExist when absent
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, nil, ErrNotFound
	}
	return f, info, nil
}

// Save streams r into the shared directory under name, replacing any existing
// file atomically. It enforces the per-file size cap and never leaves a
// partial file visible: data is written to a hidden temp file that is renamed
// into place only after a complete, within-limit copy. Returns the number of
// bytes written.
func (m *Manager) Save(name string, r io.Reader) (int64, error) {
	if err := validateName(name); err != nil {
		return 0, err
	}

	tmp, err := os.CreateTemp(m.dir, tempPrefix+"*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	// On any failure path, make sure the temp file does not linger.
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	// Read one byte past the cap so an exactly-at-limit file passes but the
	// first oversize byte is detected without copying the whole stream.
	src := r
	if m.maxUpload > 0 {
		src = io.LimitReader(r, m.maxUpload+1)
	}
	n, err := io.Copy(tmp, src)
	if err != nil {
		cleanup()
		return 0, err
	}
	if m.maxUpload > 0 && n > m.maxUpload {
		cleanup()
		return 0, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}

	final := filepath.Join(m.dir, name)
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return 0, err
	}
	return n, nil
}

// Delete permanently removes a shared file. There is no trash — the file is
// gone. A missing file returns ErrNotFound.
func (m *Manager) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(m.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

// sweepTemps removes upload temp files left behind by an interrupted upload in
// a previous run. Best-effort: errors are ignored.
func (m *Manager) sweepTemps() {
	des, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), tempPrefix) {
			_ = os.Remove(filepath.Join(m.dir, de.Name()))
		}
	}
}

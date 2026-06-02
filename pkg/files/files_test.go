package files

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestManager returns a Manager over a fresh temp dir with a 1 KiB cap.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return New(t.TempDir(), 1024)
}

func TestListEmpty(t *testing.T) {
	m := newTestManager(t)
	got, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty list, got %d entries", len(got))
	}
}

func TestSaveAndList(t *testing.T) {
	m := newTestManager(t)
	body := []byte("hello world")
	n, err := m.Save("greeting.txt", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n != int64(len(body)) {
		t.Fatalf("Save wrote %d bytes, want %d", n, len(body))
	}

	entries, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "greeting.txt" {
		t.Errorf("name = %q, want greeting.txt", entries[0].Name)
	}
	if entries[0].Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", entries[0].Size, len(body))
	}
	if entries[0].Modified == 0 {
		t.Errorf("modified time not set")
	}
}

func TestSaveOverwrite(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Save("f.txt", strings.NewReader("first")); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if _, err := m.Save("f.txt", strings.NewReader("second-longer")); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	f, _, err := m.Open("f.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != "second-longer" {
		t.Errorf("content = %q, want second-longer", got)
	}
	// Overwrite must not leave duplicates or temp files behind.
	entries, _ := m.List()
	if len(entries) != 1 {
		t.Errorf("want 1 entry after overwrite, got %d", len(entries))
	}
}

func TestSavePathTraversal(t *testing.T) {
	m := newTestManager(t)
	bad := []string{
		"../escape.txt",
		"../../etc/passwd",
		"sub/dir.txt",
		`..\windows.txt`,
		`a\b.txt`,
		".hidden",
		".",
		"..",
		"",
		"with\x00nul",
	}
	for _, name := range bad {
		if _, err := m.Save(name, strings.NewReader("x")); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Save(%q): err = %v, want ErrInvalidName", name, err)
		}
	}
	// Nothing should have escaped the sandbox or landed inside it.
	if _, err := os.Stat(filepath.Join(filepath.Dir(m.Dir()), "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("traversal wrote a file outside the share dir")
	}
	entries, _ := m.List()
	if len(entries) != 0 {
		t.Errorf("want empty share dir after rejected saves, got %d entries", len(entries))
	}
}

func TestSaveTooLarge(t *testing.T) {
	m := New(t.TempDir(), 16) // 16-byte cap
	_, err := m.Save("big.bin", bytes.NewReader(bytes.Repeat([]byte("A"), 17)))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	// A rejected oversize upload must not leave a visible file or temp file.
	entries, _ := m.List()
	if len(entries) != 0 {
		t.Errorf("oversize upload left %d entries behind", len(entries))
	}
	if leftoverTemps(t, m.Dir()) {
		t.Errorf("oversize upload left a temp file behind")
	}
}

func TestSaveExactlyAtLimit(t *testing.T) {
	m := New(t.TempDir(), 16)
	if _, err := m.Save("exact.bin", bytes.NewReader(bytes.Repeat([]byte("A"), 16))); err != nil {
		t.Fatalf("Save at exact limit: %v", err)
	}
}

func TestOpenNotFound(t *testing.T) {
	m := newTestManager(t)
	_, _, err := m.Open("nope.txt")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestOpenTraversal(t *testing.T) {
	m := newTestManager(t)
	if _, _, err := m.Open("../files.go"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
}

func TestDelete(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Save("doomed.txt", strings.NewReader("bye")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := m.Delete("doomed.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := m.Open("doomed.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present after Delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	m := newTestManager(t)
	if err := m.Delete("ghost.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteTraversal(t *testing.T) {
	m := newTestManager(t)
	if err := m.Delete("../files.go"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("err = %v, want ErrInvalidName", err)
	}
	if _, err := os.Stat("files.go"); err != nil {
		t.Errorf("traversal delete may have affected files outside the sandbox")
	}
}

func TestListSkipsHiddenAndDirs(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Save("visible.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir(), ".secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(m.Dir(), "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "visible.txt" {
		t.Errorf("List = %+v, want only visible.txt", entries)
	}
}

func TestSweepTemps(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, tempPrefix+"abc123")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	New(dir, 1024) // constructor sweeps
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temp file not swept on New")
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"plain.txt":            "plain.txt",
		"path/to/file.txt":     "file.txt",
		`win\path\file.txt`:    "file.txt",
		"../../etc/passwd":     "passwd",
		`C:\Users\me\doc.docx`: "doc.docx",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func leftoverTemps(t *testing.T, dir string) bool {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, de := range des {
		if strings.HasPrefix(de.Name(), tempPrefix) {
			return true
		}
	}
	return false
}

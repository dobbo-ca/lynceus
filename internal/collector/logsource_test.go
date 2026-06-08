package collector

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readAll(t *testing.T, tail *FileTail) string {
	t.Helper()
	r, err := tail.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return string(b)
}

func TestFileTail_appendReturnsOnlyNewBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.log")
	if err := os.WriteFile(path, []byte("line one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := NewFileTail(path)
	defer tail.Close()

	if got := readAll(t, tail); got != "line one\n" {
		t.Fatalf("first Read = %q, want %q", got, "line one\n")
	}
	if got := readAll(t, tail); got != "" {
		t.Fatalf("second Read with no new data = %q, want empty", got)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("line two\n")
	_ = f.Close()
	if got := readAll(t, tail); got != "line two\n" {
		t.Fatalf("after append Read = %q, want %q", got, "line two\n")
	}
}

func TestFileTail_cutsOnNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.log")
	if err := os.WriteFile(path, []byte("complete\npartial without newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := NewFileTail(path)
	defer tail.Close()

	// Only the bytes up to and including the last '\n' are returned; the
	// dangling partial line is withheld until its newline arrives.
	if got := readAll(t, tail); got != "complete\n" {
		t.Fatalf("Read = %q, want only the complete line", got)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(" finished\n")
	_ = f.Close()
	if got := readAll(t, tail); got != "partial without newline finished\n" {
		t.Fatalf("Read = %q, want the now-complete second line", got)
	}
}

func TestFileTail_truncationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.log")
	if err := os.WriteFile(path, []byte("aaaa\nbbbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := NewFileTail(path)
	defer tail.Close()
	_ = readAll(t, tail) // drain to current offset

	// copytruncate: same inode, file shrinks to zero then gets new content.
	if err := os.WriteFile(path, []byte("cccc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, tail); got != "cccc\n" {
		t.Fatalf("after truncation Read = %q, want %q", got, "cccc\n")
	}
}

func TestFileTail_rotationReopensNewInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.log")
	if err := os.WriteFile(path, []byte("old1\nold2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := NewFileTail(path)
	defer tail.Close()
	_ = readAll(t, tail) // consume old file

	// logrotate-by-rename: move old aside, create a fresh file at path.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, tail); got != "new1\n" {
		t.Fatalf("after rotation Read = %q, want %q", got, "new1\n")
	}
}

func TestFileTail_missingFileEmptyReaderNoError(t *testing.T) {
	tail := NewFileTail(filepath.Join(t.TempDir(), "does-not-exist.log"))
	defer tail.Close()
	r, err := tail.Read()
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	b, _ := io.ReadAll(r)
	if len(b) != 0 {
		t.Fatalf("missing file should yield empty reader, got %q", string(b))
	}
}

func TestFileTail_capsMaxChunk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg.log")
	// 100 lines of 10 bytes = 1000 bytes; cap at 50 to force a partial read.
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("123456789\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := NewFileTail(path)
	tail.maxChunk = 55 // cap mid-stream; expect cut on the last newline <= 55
	defer tail.Close()

	got := readAll(t, tail)
	if len(got) == 0 || len(got) > 55 {
		t.Fatalf("capped Read len = %d, want 1..55", len(got))
	}
	if got[len(got)-1] != '\n' {
		t.Fatalf("capped Read must end on newline, got %q", got)
	}
	if got != "123456789\n123456789\n123456789\n123456789\n123456789\n" {
		t.Fatalf("capped Read = %q, want five whole lines", got)
	}
}

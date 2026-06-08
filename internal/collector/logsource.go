package collector

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

// LogSource yields newly-appended log bytes since the previous call.
// io.EOF is never returned — an empty reader means "nothing new yet".
// Read is single-goroutine.
type LogSource interface {
	Read() (io.Reader, error)
	io.Closer
}

// FileTail tails a single log file, surviving logrotate/copytruncate.
// Tracks (inode, size): inode change => rotation, size shrink => truncation;
// reopens from offset 0 in either case. Each Read returns a bounded chunk of
// bytes appended since the last offset, cut on the last '\n' so a chunk never
// splits a record (a TAB-prefixed stderr continuation or a CSV row).
type FileTail struct {
	path     string
	f        *os.File
	inode    uint64
	offset   int64
	maxChunk int64 // cap bytes per Read; default 8 MiB
}

// NewFileTail returns a FileTail for path. The file need not exist yet; until
// it appears Read returns an empty reader and a nil error.
func NewFileTail(path string) *FileTail {
	return &FileTail{path: path, maxChunk: 8 << 20}
}

// Read returns the bytes appended since the previous Read, bounded by maxChunk
// and truncated at the last '\n'. A missing file yields an empty reader and a
// nil error. offset advances only past the bytes actually returned, so a
// partial trailing line (no '\n' yet) is re-read on the next call.
func (t *FileTail) Read() (io.Reader, error) {
	if err := t.reopenIfRotated(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return bytes.NewReader(nil), nil
		}
		return nil, err
	}

	fi, err := t.f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size < t.offset {
		// Truncation (copytruncate): the file shrank under us. Start over.
		t.offset = 0
	}
	avail := size - t.offset
	if avail <= 0 {
		return bytes.NewReader(nil), nil
	}
	if avail > t.maxChunk {
		avail = t.maxChunk
	}

	buf := make([]byte, avail)
	n, err := t.f.ReadAt(buf, t.offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]

	// Cut on the last newline so the chunk ends on a record boundary.
	cut := bytes.LastIndexByte(buf, '\n')
	if cut < 0 {
		// No complete line yet — withhold everything, advance nothing.
		return bytes.NewReader(nil), nil
	}
	out := buf[:cut+1]
	t.offset += int64(len(out))
	return bytes.NewReader(out), nil
}

// reopenIfRotated (re)opens t.f when the file is not yet open or its inode
// changed (logrotate-by-rename). On reopen the offset resets to 0.
func (t *FileTail) reopenIfRotated() error {
	fi, err := os.Stat(t.path)
	if err != nil {
		return err
	}
	ino := inodeOf(fi)
	if t.f != nil && ino == t.inode {
		return nil
	}
	if t.f != nil {
		_ = t.f.Close()
		t.f = nil
	}
	f, err := os.Open(t.path)
	if err != nil {
		return err
	}
	t.f = f
	t.inode = ino
	t.offset = 0
	return nil
}

// Close releases the open file handle, if any.
func (t *FileTail) Close() error {
	if t.f == nil {
		return nil
	}
	err := t.f.Close()
	t.f = nil
	return err
}

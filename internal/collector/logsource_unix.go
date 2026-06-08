//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly || solaris

package collector

import (
	"io/fs"
	"syscall"
)

// inodeOf returns the file's inode number on Unix. A changed inode at the same
// path means logrotate renamed the old file aside and created a fresh one.
func inodeOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

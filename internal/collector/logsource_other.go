//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package collector

import "io/fs"

// inodeOf has no portable inode concept off Unix, so it returns 0 and rotation
// is detected only by truncation (size shrink). Production runs on linux; this
// fallback exists so non-Unix cross-compiles don't break.
func inodeOf(fi fs.FileInfo) uint64 { return 0 }

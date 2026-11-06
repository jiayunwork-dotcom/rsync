//go:build linux || darwin

package receiver

import (
	"os"
	"syscall"
)

// sameInode reports whether two file infos describe the same inode, i.e.
// whether the two names are hard links of one another.
func sameInode(a, b os.FileInfo) bool {
	sa, oka := a.Sys().(*syscall.Stat_t)
	sb, okb := b.Sys().(*syscall.Stat_t)
	if !oka || !okb {
		return false
	}
	return sa.Dev == sb.Dev && sa.Ino == sb.Ino
}

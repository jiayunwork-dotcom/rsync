//go:build linux || darwin

package sender

import (
	"io/fs"
	"syscall"
)

func uidFromFileInfo(info fs.FileInfo) (int32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int32(st.Uid), true
}

func gidFromFileInfo(info fs.FileInfo) (int32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int32(st.Gid), true
}

func rdevFromFileInfo(info fs.FileInfo) (int32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int32(st.Rdev), true
}

// devInoFromFileInfo returns the device and inode numbers identifying the
// file's inode, as used for preserving hard links (-H).
func devInoFromFileInfo(info fs.FileInfo) (dev, ino int64, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int64(st.Dev), int64(st.Ino), true
}

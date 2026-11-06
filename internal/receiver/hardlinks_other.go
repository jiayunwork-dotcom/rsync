//go:build !linux && !darwin

package receiver

import "os"

// sameInode reports whether two file infos describe the same inode. Hard link
// preservation (-H) is only supported on Linux and macOS; on other platforms
// assume the inodes differ so that links are (re)created explicitly.
func sameInode(a, b os.FileInfo) bool { return false }

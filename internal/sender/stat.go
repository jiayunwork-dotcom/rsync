//go:build !linux && !darwin

package sender

import "io/fs"

func uidFromFileInfo(fs.FileInfo) (int32, bool) {
	return 0, false
}

func gidFromFileInfo(fs.FileInfo) (int32, bool) {
	return 0, false
}

func rdevFromFileInfo(fs.FileInfo) (int32, bool) {
	return 0, false
}

func devInoFromFileInfo(fs.FileInfo) (dev, ino int64, ok bool) {
	return 0, 0, false
}

//go:build !linux

package agent

import "os"

// statInfo returns device, size, inode and link count where available.
func statInfo(fi os.FileInfo) (dev uint64, size int64, ino uint64, nlink uint64) {
	return 0, fi.Size(), 0, 1
}

//go:build linux

package agent

import (
	"os"
	"syscall"
)

// statInfo returns device, allocated bytes (like du), inode and link count.
func statInfo(fi os.FileInfo) (dev uint64, size int64, ino uint64, nlink uint64) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fi.Size(), 0, 1
	}
	return uint64(st.Dev), st.Blocks * 512, uint64(st.Ino), uint64(st.Nlink)
}

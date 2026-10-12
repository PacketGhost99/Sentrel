//go:build !windows

package app

import "golang.org/x/sys/unix"

func diskUsage(path string) DiskInfo {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return DiskInfo{}
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	return DiskInfo{TotalBytes: total, FreeBytes: free, UsedBytes: total - st.Bfree*uint64(st.Bsize)}
}

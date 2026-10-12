//go:build windows

package app

import "golang.org/x/sys/windows"

func diskUsage(path string) DiskInfo {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return DiskInfo{}
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return DiskInfo{}
	}
	return DiskInfo{TotalBytes: total, FreeBytes: free, UsedBytes: total - totalFree}
}

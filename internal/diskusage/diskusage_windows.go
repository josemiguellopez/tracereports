//go:build windows

package diskusage

import "golang.org/x/sys/windows"

// Of returns the usage of the disk that holds dir.
func Of(dir string) (Usage, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return Usage{}, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return Usage{}, err
	}
	return Usage{Total: total, Free: free}, nil
}

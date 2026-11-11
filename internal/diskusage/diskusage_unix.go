//go:build linux || darwin || freebsd

package diskusage

import "golang.org/x/sys/unix"

// Of returns the usage of the disk that holds dir.
func Of(dir string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return Usage{}, err
	}
	bs := uint64(st.Bsize)
	return Usage{Total: uint64(st.Blocks) * bs, Free: uint64(st.Bavail) * bs}, nil
}

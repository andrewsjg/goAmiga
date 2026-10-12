//go:build darwin || linux

package amiga

import "syscall"

// diskSpace is the size and free space of the filesystem holding dir.
func diskSpace(dir string) (total, free uint64) {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 1 << 30, 1 << 29
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize)
}

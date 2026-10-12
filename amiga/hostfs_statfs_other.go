//go:build !darwin && !linux

package amiga

// diskSpace reports a nominal 1 GB disk, half free, where the host's free
// space isn't looked up.
func diskSpace(dir string) (total, free uint64) { return 1 << 30, 1 << 29 }

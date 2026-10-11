package amiga

import "golang.org/x/sys/unix"

// macOS raises SIGPIPE on a write to a closed socket unless told not to.
func noSigPipe(fd int) { unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_NOSIGPIPE, 1) }

const sendNoSignal = 0

const hostFIONREAD = 0x4004667F

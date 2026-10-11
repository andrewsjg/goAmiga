package amiga

import "golang.org/x/sys/unix"

func noSigPipe(int) {}

// Linux says per send that a closed socket mustn't raise SIGPIPE.
const sendNoSignal = unix.MSG_NOSIGNAL

const hostFIONREAD = unix.TIOCINQ // FIONREAD

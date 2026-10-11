//go:build !darwin && !linux

package amiga

// bsdsocket.library needs the host's BSD socket calls; on other systems the
// library opens but every call fails with ENOSYS.

type hsock struct{}

func (s *hsock) release() {}

type sockTask struct {
	task      uint32
	sig       int8
	spareSigs []int8
	opens     int
	slot      int
	errno     int32
	breakMask uint32
	op        *pendingOp
}

func newSockTask(task uint32, sig int8, slot int, buf uint32) *sockTask {
	return &sockTask{task: task, sig: sig, slot: slot}
}

func (t *sockTask) closeAll()                  {}
func (t *sockTask) setErrno(a *Amiga, e int32) { t.errno = e }

type pendingOp struct {
	fn       int
	regs     [16]uint32
	userMask uint32
	gotUser  uint32
	woken    bool
}

func (op *pendingOp) ready(uint64) bool { return true }
func (op *pendingOp) cancel()           {}

func (n *netBoard) dispatch(t *sockTask, op *pendingOp) (int32, bool) {
	t.setErrno(n.amiga, 78) // ENOSYS
	return -1, false
}

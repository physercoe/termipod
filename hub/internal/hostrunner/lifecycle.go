package hostrunner

import (
	"sync"
	"time"
)

// driverStopDrainTimeout bounds how long a driver's Stop() waits for its
// readLoop to unwind. The loop normally drains the instant Closer closes its
// pipe (scanner hits EOF); the bound is the backstop for a nil or ineffective
// Closer, which would otherwise hang Stop() — and the whole host-runner stop
// path — forever (#77.3). A var so teardown tests can shorten it.
var driverStopDrainTimeout = 5 * time.Second

// waitTimeout blocks until wg drains or d elapses, whichever comes first. It
// returns true if the group drained and false on timeout.
//
// Teardown paths in this package wait on goroutines that can be wedged on a
// blocking syscall a context cancel does not preempt — a child whose stdin
// buffer is full (StdioDriver.Input deliberately does not abort its Write), or a
// readLoop scanner that only unwinds when its pipe closes. An unconditional
// wg.Wait() there can hang the host-runner's stop path forever (#77). Bounding
// the wait keeps teardown live: the straggler is unblocked moments later when
// the driver's transport is closed, so abandoning the wait leaks nothing.
func waitTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

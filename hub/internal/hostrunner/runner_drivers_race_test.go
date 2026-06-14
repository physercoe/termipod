package hostrunner

import (
	"strconv"
	"sync"
	"testing"
)

// TestDriversMapConcurrentAccess exercises the agent-keyed map accessors
// from many goroutines at once — the cross-goroutine pattern that exists
// in production between the main reconcile loop (putDriver / hasDriver /
// driverIDsSnapshot) and the A2A tunnel goroutine (driverIDsSnapshot +
// stopDriver). Before agentsMu (#77) the bare map read/write/delete here
// is a data race the -race detector flags (and a real `fatal error:
// concurrent map ...` panic risk). With the lock it must run clean.
//
// Run with: go test -race ./internal/hostrunner/ -run DriversMapConcurrent
func TestDriversMapConcurrentAccess(t *testing.T) {
	a := &Runner{
		drivers:  map[string]Driver{},
		gateways: map[string]*McpGateway{},
	}

	const workers = 8
	const iters = 200
	ids := make([]string, 16)
	for i := range ids {
		ids[i] = "agent-" + strconv.Itoa(i)
	}

	var wg sync.WaitGroup
	// Writers: register drivers (main loop launchOne path).
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				a.putDriver(ids[(seed+i)%len(ids)], &stubDriver{})
			}
		}(w)
	}
	// Readers: hasDriver + driverIDsSnapshot (reconcile / dedup paths).
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = a.hasDriver(ids[(seed+i)%len(ids)])
				_ = a.driverIDsSnapshot()
			}
		}(w)
	}
	// Removers: stopDriver (reconcile teardown + A2A handleHostExit path).
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				a.stopDriver(ids[(seed+i)%len(ids)])
			}
		}(w)
	}
	wg.Wait()
}

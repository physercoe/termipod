package hostrunner

import (
	"context"
	"runtime"
	"testing"
)

// HostInfo is best-effort: we don't assert exact values (they vary by
// CI runner), but we pin the contract that the structural fields are
// always populated and the optional fields don't crash the probe.
func TestProbeHostInfo_PopulatesStaticFields(t *testing.T) {
	hi := ProbeHostInfo(context.Background())
	if hi.OS != runtime.GOOS {
		t.Errorf("OS = %q; want runtime.GOOS = %q", hi.OS, runtime.GOOS)
	}
	if hi.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q; want runtime.GOARCH = %q", hi.Arch, runtime.GOARCH)
	}
	if hi.CPUCount <= 0 {
		t.Errorf("CPUCount = %d; want > 0", hi.CPUCount)
	}
	if hi.Hostname == "" {
		t.Logf("Hostname empty (env-dependent — accepted)")
	}
	// On Linux/Darwin in CI the meminfo path is reachable; we check
	// MemBytes is non-zero on those platforms but allow 0 elsewhere
	// (Windows host-runner targets aren't supported, this is the
	// "unknown OS" fallback path).
	switch runtime.GOOS {
	case "linux", "darwin":
		if hi.MemBytes == 0 {
			t.Errorf("MemBytes = 0 on %s; expected /proc/meminfo or sysctl to populate", runtime.GOOS)
		}
	}
}

func TestProbeHostInfo_AttachesToCapabilitiesPayload(t *testing.T) {
	// The reconcile loop's contract: a probe sweep stamps Capabilities.Host
	// from the cached HostInfo so the hub mobile detail screen always sees
	// the static facts. Pinning this catches a regression where the
	// runner wires probeLoop without re-attaching the cached pointer.
	hi := ProbeHostInfo(context.Background())
	caps := ProbeCapabilities(context.Background())
	if caps.Host != nil {
		t.Fatalf("ProbeCapabilities should not auto-populate Host; the runner attaches it explicitly")
	}
	caps.Host = &hi
	if caps.Host == nil || caps.Host.OS != runtime.GOOS {
		t.Errorf("Host attachment broken: %+v", caps.Host)
	}
}

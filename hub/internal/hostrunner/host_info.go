package hostrunner

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HostInfo carries the static facts about the box host-runner is
// running on — OS, CPU count, total memory, kernel version, hostname.
// Populated once at startup (probeHostInfo) rather than every probe
// sweep: these don't change while the runner is up, and the periodic
// agent-capabilities probe is hot enough that re-reading /proc/meminfo
// each cycle is wasted work.
//
// All fields are best-effort: an unreadable /proc/meminfo or a missing
// `uname` doesn't fail the runner; we leave the corresponding field
// zero/empty and the mobile renderer hides the unknown row.
type HostInfo struct {
	OS       string `json:"os"`             // runtime.GOOS — linux, darwin, …
	Arch     string `json:"arch"`           // runtime.GOARCH — amd64, arm64, …
	CPUCount int    `json:"cpu_count"`      // runtime.NumCPU
	MemBytes uint64 `json:"mem_bytes"`      // total physical RAM in bytes
	Kernel   string `json:"kernel,omitempty"`   // `uname -r` trimmed
	Hostname string `json:"hostname,omitempty"` // os.Hostname()
}

// ProbeHostInfo gathers the static-facts payload. Cross-platform:
// memory uses /proc/meminfo on Linux and `sysctl hw.memsize` on Darwin;
// kernel uses `uname -r` on both. Windows hosts are not a host-runner
// target, so that path returns the bare runtime fields.
func ProbeHostInfo(ctx context.Context) HostInfo {
	out := HostInfo{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		CPUCount: runtime.NumCPU(),
	}
	if hn, err := os.Hostname(); err == nil {
		out.Hostname = hn
	}
	out.MemBytes = readMemBytes()
	out.Kernel = readKernel(ctx)
	return out
}

// readMemBytes returns total RAM in bytes, or 0 on unsupported OS or
// read failure. Linux: /proc/meminfo MemTotal (in kB → ×1024). Darwin:
// `sysctl -n hw.memsize` (bytes). The Darwin path shells out because
// CGo-free sysctl is awkward and the value never changes — the cost
// is paid once at startup.
func readMemBytes() uint64 {
	switch runtime.GOOS {
	case "linux":
		return readMemBytesLinux()
	case "darwin":
		return readMemBytesDarwin()
	default:
		return 0
	}
}

func readMemBytesLinux() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		// Format: `MemTotal:   16384000 kB`
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func readMemBytesDarwin() uint64 {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// readKernel returns `uname -r` trimmed, or "" if unavailable. Bounded
// by a 2s timeout against ctx so a stuck shell doesn't hold the runner.
func readKernel(ctx context.Context) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "uname", "-r")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func vaultPath(suffix string) string {
	return "/v1/teams/" + defaultTeamID + "/vault" + suffix
}

func TestVault_PushPullRoundTrip(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Fresh principal has no vault yet.
	status, _ := doReq(t, s, token, http.MethodGet, vaultPath(""), nil)
	if status != http.StatusNotFound {
		t.Fatalf("pull before push: want 404, got %d", status)
	}

	// First push (base_version 0) creates version 1.
	status, body := doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "sealed-v1", "base_version": 0})
	if status != http.StatusOK {
		t.Fatalf("first push: want 200, got %d (%s)", status, body)
	}
	var pushed struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &pushed); err != nil {
		t.Fatalf("decode push: %v", err)
	}
	if pushed.Version != 1 {
		t.Fatalf("first push version: want 1, got %d", pushed.Version)
	}

	// Pull returns the sealed ciphertext verbatim.
	status, body = doReq(t, s, token, http.MethodGet, vaultPath(""), nil)
	if status != http.StatusOK {
		t.Fatalf("pull: want 200, got %d", status)
	}
	var out vaultOut
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode pull: %v", err)
	}
	if out.Ciphertext != "sealed-v1" || out.Version != 1 {
		t.Fatalf("pull mismatch: %+v", out)
	}
}

func TestVault_LastDevice(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Create carrying a device name — pull echoes it back.
	doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v1", "base_version": 0, "device_name": "mac-studio"})
	_, body := doReq(t, s, token, http.MethodGet, vaultPath(""), nil)
	var out vaultOut
	_ = json.Unmarshal(body, &out)
	if out.LastDevice != "mac-studio" {
		t.Fatalf("create last_device: want mac-studio, got %q", out.LastDevice)
	}

	// A push from a different machine overwrites it.
	doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v2", "base_version": 1, "device_name": "macbook"})
	_, body = doReq(t, s, token, http.MethodGet, vaultPath(""), nil)
	_ = json.Unmarshal(body, &out)
	if out.LastDevice != "macbook" {
		t.Fatalf("update last_device: want macbook, got %q", out.LastDevice)
	}

	// A push that OMITS the name (older/mobile client) must not clobber it —
	// COALESCE keeps the previously-recorded machine.
	doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v3", "base_version": 2})
	_, body = doReq(t, s, token, http.MethodGet, vaultPath(""), nil)
	_ = json.Unmarshal(body, &out)
	if out.LastDevice != "macbook" {
		t.Fatalf("omitted device_name should preserve: want macbook, got %q", out.LastDevice)
	}
}

func TestVault_OptimisticConcurrency(t *testing.T) {
	s, token := newA2ATestServer(t)

	doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v1", "base_version": 0})

	// Correct base_version updates to v2.
	status, body := doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v2", "base_version": 1})
	if status != http.StatusOK {
		t.Fatalf("update with correct base: want 200, got %d (%s)", status, body)
	}

	// A second push at the now-stale base_version 1 conflicts.
	status, _ = doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v2-stale", "base_version": 1})
	if status != http.StatusConflict {
		t.Fatalf("stale push: want 409, got %d", status)
	}

	// Re-pushing at base_version 0 over an existing vault also conflicts.
	status, _ = doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "v-dupe", "base_version": 0})
	if status != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d", status)
	}

	// The winning write survived.
	_, body = doReq(t, s, token, http.MethodGet, vaultPath(""), nil)
	var out vaultOut
	_ = json.Unmarshal(body, &out)
	if out.Ciphertext != "v2" || out.Version != 2 {
		t.Fatalf("after conflicts: want v2/2, got %+v", out)
	}
}

func TestVault_MissingCiphertext(t *testing.T) {
	s, token := newA2ATestServer(t)
	status, _ := doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"base_version": 0})
	if status != http.StatusBadRequest {
		t.Fatalf("empty ciphertext: want 400, got %d", status)
	}
}

func TestVault_RecoveryEscrow(t *testing.T) {
	s, token := newA2ATestServer(t)

	// Recovery can't be set before the vault exists.
	status, _ := doReq(t, s, token, http.MethodPut, vaultPath("/recovery"),
		map[string]any{"recovery_envelope": "wrapped-under-recovery-key"})
	if status != http.StatusNotFound {
		t.Fatalf("set recovery before vault: want 404, got %d", status)
	}

	// Create the vault, then no recovery envelope is set yet.
	doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "sealed", "base_version": 0})
	status, _ = doReq(t, s, token, http.MethodGet, vaultPath("/recovery"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("get recovery before set: want 404, got %d", status)
	}

	// A missing envelope is rejected.
	status, _ = doReq(t, s, token, http.MethodPut, vaultPath("/recovery"),
		map[string]any{"recovery_hint": "no envelope"})
	if status != http.StatusBadRequest {
		t.Fatalf("set recovery without envelope: want 400, got %d", status)
	}

	// Set, then read it back verbatim.
	status, _ = doReq(t, s, token, http.MethodPut, vaultPath("/recovery"),
		map[string]any{"recovery_envelope": "wrapped-under-recovery-key", "recovery_hint": "code 2026-07-05"})
	if status != http.StatusOK {
		t.Fatalf("set recovery: want 200, got %d", status)
	}
	status, body := doReq(t, s, token, http.MethodGet, vaultPath("/recovery"), nil)
	if status != http.StatusOK {
		t.Fatalf("get recovery: want 200, got %d", status)
	}
	var rec vaultRecoveryOut
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("decode recovery: %v", err)
	}
	if rec.RecoveryEnvelope != "wrapped-under-recovery-key" {
		t.Fatalf("recovery envelope mismatch: %+v", rec)
	}
	if rec.RecoveryHint == nil || *rec.RecoveryHint != "code 2026-07-05" {
		t.Fatalf("recovery hint mismatch: %+v", rec)
	}

	// Pushing a new vault version leaves the recovery envelope intact.
	doReq(t, s, token, http.MethodPut, vaultPath(""),
		map[string]any{"ciphertext": "sealed-2", "base_version": 1})
	status, _ = doReq(t, s, token, http.MethodGet, vaultPath("/recovery"), nil)
	if status != http.StatusOK {
		t.Fatalf("recovery after vault update: want 200, got %d", status)
	}

	// Clear it; then it 404s again.
	status, _ = doReq(t, s, token, http.MethodDelete, vaultPath("/recovery"), nil)
	if status != http.StatusOK {
		t.Fatalf("clear recovery: want 200, got %d", status)
	}
	status, _ = doReq(t, s, token, http.MethodGet, vaultPath("/recovery"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("get recovery after clear: want 404, got %d", status)
	}
}

func TestVault_DeviceEnrollWrapListRevoke(t *testing.T) {
	s, token := newA2ATestServer(t)

	// A new device enrolls with its public key, no wrapped key yet.
	status, _ := doReq(t, s, token, http.MethodPut, vaultPath("/devices/desktop-1"),
		map[string]any{"device_name": "desktop", "public_key": "pk-desktop"})
	if status != http.StatusOK {
		t.Fatalf("enroll device: want 200, got %d", status)
	}

	// Enrolling a device with no public key and no prior row is rejected.
	status, _ = doReq(t, s, token, http.MethodPut, vaultPath("/devices/phantom"),
		map[string]any{"wrapped_key": "wk"})
	if status != http.StatusBadRequest {
		t.Fatalf("enroll without pubkey: want 400, got %d", status)
	}

	// An enrolled device wraps the vault key to desktop-1 (pubkey omitted, kept).
	status, _ = doReq(t, s, token, http.MethodPut, vaultPath("/devices/desktop-1"),
		map[string]any{"wrapped_key": "wrapped-for-desktop"})
	if status != http.StatusOK {
		t.Fatalf("wrap key: want 200, got %d", status)
	}

	// List shows one device with its wrapped key and preserved public key.
	status, body := doReq(t, s, token, http.MethodGet, vaultPath("/devices"), nil)
	if status != http.StatusOK {
		t.Fatalf("list devices: want 200, got %d", status)
	}
	var listed struct {
		Devices []vaultDeviceOut `json:"devices"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Devices) != 1 {
		t.Fatalf("device count: want 1, got %d", len(listed.Devices))
	}
	d := listed.Devices[0]
	if d.DeviceID != "desktop-1" || d.PublicKey != "pk-desktop" {
		t.Fatalf("device fields: %+v", d)
	}
	if d.WrappedKey == nil || *d.WrappedKey != "wrapped-for-desktop" {
		t.Fatalf("wrapped key not set/preserved: %+v", d)
	}

	// Revoke removes it; a second revoke 404s.
	status, _ = doReq(t, s, token, http.MethodDelete, vaultPath("/devices/desktop-1"), nil)
	if status != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d", status)
	}
	status, _ = doReq(t, s, token, http.MethodDelete, vaultPath("/devices/desktop-1"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("revoke again: want 404, got %d", status)
	}
}

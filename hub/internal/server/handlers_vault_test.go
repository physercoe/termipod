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

package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/termipod/hub/internal/auth"
)

// Zero-knowledge SSH key-vault sync (ADR-052 D-4). The hub is a blind blob
// store: it holds only client-side-encrypted ciphertext it can never decrypt,
// keyed to the calling principal, and syncs it across that principal's devices.
// This is the carve-out that amends forbidden-pattern #15 — the hub never holds
// the vault key or any plaintext.
//
// Ownership is server-derived from the token scope (never client-supplied), so a
// caller can only ever reach their own vault. principalFromScope is never empty
// (it falls back to "@principal"), so a token without a handle still gets a
// stable owner key. Because the payload is opaque ciphertext, this access
// scoping is defense-in-depth: the real protection is that only enrolled devices
// hold the key to decrypt it.

// maxVaultBytes caps a pushed vault/device body. SSH key material is small
// (a few KB per key); 2 MiB is far more than a realistic vault.
const maxVaultBytes = 2 << 20

type vaultPushIn struct {
	Ciphertext  string `json:"ciphertext"`
	BaseVersion int    `json:"base_version"`
}

type vaultOut struct {
	Ciphertext string `json:"ciphertext"`
	Version    int    `json:"version"`
	UpdatedAt  string `json:"updated_at"`
}

type vaultDeviceIn struct {
	DeviceName string `json:"device_name,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
	WrappedKey string `json:"wrapped_key,omitempty"`
}

type vaultDeviceOut struct {
	DeviceID   string  `json:"device_id"`
	DeviceName *string `json:"device_name,omitempty"`
	PublicKey  string  `json:"public_key"`
	WrappedKey *string `json:"wrapped_key,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// vaultOwner returns the server-derived principal key for the calling token, or
// false (having written a 401) if the request is unauthenticated.
func (s *Server) vaultOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	tok, ok := auth.FromContext(r.Context())
	if !ok || tok == nil {
		writeErr(w, http.StatusUnauthorized, "unauthenticated")
		return "", false
	}
	return principalFromScope(tok.ScopeJSON), true
}

// GET /v1/teams/{team}/vault — pull the sealed vault blob. 404 when the
// principal has never pushed one (so a fresh client knows to create it).
func (s *Server) handlePullVault(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	var out vaultOut
	err := s.db.QueryRowContext(r.Context(),
		`SELECT ciphertext, version, updated_at
		   FROM key_vaults WHERE team_id = ? AND handle = ?`,
		team, owner).Scan(&out.Ciphertext, &out.Version, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no vault for this principal")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /v1/teams/{team}/vault — push the sealed vault blob with optimistic
// concurrency. base_version=0 creates it; a non-zero base_version updates only
// if it matches the stored version, else 409 (the client must pull and retry).
func (s *Server) handlePushVault(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultBytes)
	var in vaultPushIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "vault too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Ciphertext == "" {
		writeErr(w, http.StatusBadRequest, "ciphertext required")
		return
	}
	now := NowUTC()

	if in.BaseVersion == 0 {
		// First write: insert version 1. A PK collision means a vault already
		// exists, i.e. base_version is stale — writeDBErr maps the constraint
		// to 409, telling the client to pull and retry.
		_, err := s.writeDB.ExecContext(r.Context(),
			`INSERT INTO key_vaults (team_id, handle, ciphertext, version, created_at, updated_at)
			 VALUES (?, ?, ?, 1, ?, ?)`,
			team, owner, in.Ciphertext, now, now)
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
		s.recordAudit(r.Context(), team, "vault.push", "vault", owner,
			"create key vault", map[string]any{"version": 1})
		writeJSON(w, http.StatusOK, map[string]any{"version": 1, "updated_at": now})
		return
	}

	res, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE key_vaults SET ciphertext = ?, version = version + 1, updated_at = ?
		  WHERE team_id = ? AND handle = ? AND version = ?`,
		in.Ciphertext, now, team, owner, in.BaseVersion)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErrHint(w, http.StatusConflict, "vault version conflict",
			Hint{HintText: "another device updated the vault; GET /vault and re-push with the current version"})
		return
	}
	newVersion := in.BaseVersion + 1
	s.recordAudit(r.Context(), team, "vault.push", "vault", owner,
		"update key vault", map[string]any{"version": newVersion})
	writeJSON(w, http.StatusOK, map[string]any{"version": newVersion, "updated_at": now})
}

type vaultRecoveryIn struct {
	RecoveryEnvelope string `json:"recovery_envelope"`
	RecoveryHint     string `json:"recovery_hint,omitempty"`
}

type vaultRecoveryOut struct {
	RecoveryEnvelope string  `json:"recovery_envelope"`
	RecoveryHint     *string `json:"recovery_hint,omitempty"`
	UpdatedAt        *string `json:"updated_at,omitempty"`
}

// PUT /v1/teams/{team}/vault/recovery — set/replace the recovery envelope (the
// vault key wrapped under the director's escrowed recovery key, opaque to the
// hub). The vault must already exist. On a re-key the client re-wraps this too.
func (s *Server) handleSetVaultRecovery(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultBytes)
	var in vaultRecoveryIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "recovery payload too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.RecoveryEnvelope == "" {
		writeErr(w, http.StatusBadRequest, "recovery_envelope required")
		return
	}
	now := NowUTC()
	res, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE key_vaults SET recovery_envelope = ?, recovery_hint = ?, recovery_updated_at = ?
		  WHERE team_id = ? AND handle = ?`,
		in.RecoveryEnvelope, nullIfEmpty(in.RecoveryHint), now, team, owner)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "create the vault before setting recovery")
		return
	}
	s.recordAudit(r.Context(), team, "vault.recovery.set", "vault", owner,
		"set vault recovery envelope", nil)
	writeJSON(w, http.StatusOK, map[string]any{"updated_at": now})
}

// GET /v1/teams/{team}/vault/recovery — fetch the recovery envelope for a
// principal recovering on a fresh device. 404 when no vault or none set.
func (s *Server) handleGetVaultRecovery(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	var env, hint, upd sql.NullString
	err := s.db.QueryRowContext(r.Context(),
		`SELECT recovery_envelope, recovery_hint, recovery_updated_at
		   FROM key_vaults WHERE team_id = ? AND handle = ?`,
		team, owner).Scan(&env, &hint, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "no vault for this principal")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if !env.Valid {
		writeErr(w, http.StatusNotFound, "no recovery envelope set")
		return
	}
	out := vaultRecoveryOut{RecoveryEnvelope: env.String}
	if hint.Valid {
		out.RecoveryHint = &hint.String
	}
	if upd.Valid {
		out.UpdatedAt = &upd.String
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /v1/teams/{team}/vault/recovery — clear the recovery envelope.
func (s *Server) handleDeleteVaultRecovery(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	res, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE key_vaults SET recovery_envelope = NULL, recovery_hint = NULL, recovery_updated_at = NULL
		  WHERE team_id = ? AND handle = ?`,
		team, owner)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "no vault for this principal")
		return
	}
	s.recordAudit(r.Context(), team, "vault.recovery.clear", "vault", owner,
		"clear vault recovery envelope", nil)
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

// GET /v1/teams/{team}/vault/devices — list the principal's enrolled devices
// (their public keys and per-device wrapped keys, all opaque to the hub). A
// new device polls this to discover when an enrolled device has wrapped the
// vault key to it.
func (s *Server) handleListVaultDevices(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT device_id, device_name, public_key, wrapped_key, created_at, updated_at
		   FROM key_vault_devices WHERE team_id = ? AND handle = ?
		  ORDER BY created_at`,
		team, owner)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	devices := []vaultDeviceOut{}
	for rows.Next() {
		var d vaultDeviceOut
		if err := rows.Scan(&d.DeviceID, &d.DeviceName, &d.PublicKey,
			&d.WrappedKey, &d.CreatedAt, &d.UpdatedAt); err != nil {
			s.writeDBErr(w, err)
			return
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// PUT /v1/teams/{team}/vault/devices/{device} — enroll or update a device. A
// new device registers its public_key (wrapped_key empty); an already-enrolled
// device later PUTs the same device_id with wrapped_key set to distribute the
// vault key to it. Fields left empty on an update are preserved.
func (s *Server) handlePutVaultDevice(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	device := chi.URLParam(r, "device")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	if device == "" {
		writeErr(w, http.StatusBadRequest, "device id required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultBytes)
	var in vaultDeviceIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeErr(w, http.StatusRequestEntityTooLarge, "device payload too large")
			return
		}
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	// A public key is mandatory to enroll a brand-new device; on an update
	// (setting wrapped_key) it may be omitted and the stored key is kept.
	if in.PublicKey == "" {
		var one int
		err := s.db.QueryRowContext(r.Context(),
			`SELECT 1 FROM key_vault_devices
			  WHERE team_id = ? AND handle = ? AND device_id = ?`,
			team, owner, device).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusBadRequest, "public_key required to enroll a new device")
			return
		}
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
	}

	now := NowUTC()
	_, err := s.writeDB.ExecContext(r.Context(),
		`INSERT INTO key_vault_devices
		   (id, team_id, handle, device_id, device_name, public_key, wrapped_key, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(team_id, handle, device_id) DO UPDATE SET
		   device_name = COALESCE(excluded.device_name, key_vault_devices.device_name),
		   public_key  = CASE WHEN excluded.public_key <> '' THEN excluded.public_key ELSE key_vault_devices.public_key END,
		   wrapped_key = COALESCE(excluded.wrapped_key, key_vault_devices.wrapped_key),
		   updated_at  = excluded.updated_at`,
		NewID(), team, owner, device, nullIfEmpty(in.DeviceName),
		in.PublicKey, nullIfEmpty(in.WrappedKey), now, now)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.recordAudit(r.Context(), team, "vault.device.enroll", "vault_device", device,
		"enroll vault device "+device, map[string]any{"device_id": device})
	writeJSON(w, http.StatusOK, map[string]any{"device_id": device, "updated_at": now})
}

// DELETE /v1/teams/{team}/vault/devices/{device} — revoke a device (remove its
// envelope). The client is expected to re-key the vault afterward (ADR-052).
func (s *Server) handleDeleteVaultDevice(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	device := chi.URLParam(r, "device")
	owner, ok := s.vaultOwner(w, r)
	if !ok {
		return
	}
	res, err := s.writeDB.ExecContext(r.Context(),
		`DELETE FROM key_vault_devices
		  WHERE team_id = ? AND handle = ? AND device_id = ?`,
		team, owner, device)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "device not found")
		return
	}
	s.recordAudit(r.Context(), team, "vault.device.revoke", "vault_device", device,
		"revoke vault device "+device, map[string]any{"device_id": device})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": device})
}

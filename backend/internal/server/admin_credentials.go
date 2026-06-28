package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/ldm0206/claude-docker/backend/internal/secrets"
	"github.com/ldm0206/claude-docker/backend/internal/store"
)

// ---------------------------------------------------------------------------
// Admin credential-preset CRUD (T7)
// ---------------------------------------------------------------------------
//
// SECURITY MODEL
//
// Credential presets store the secrets a PTY needs to call the upstream LLM
// (api_key, auth_token, base_url, proxies). They are NEVER stored in plaintext
// — the secret fields are sealed with AES-256-GCM (secrets.SealJSON) and only
// the resulting encrypted_blob is persisted.
//
// The API surface deliberately leaks nothing:
//   - GET  /api/admin/credentials → {id,name,note,created_at} ONLY
//   - POST /api/admin/credentials → {id,name} ONLY (the input secrets are
//     consumed and sealed, never echoed back)
//   - PATCH                     → {id,name,note} ONLY
//   - DELETE                    → {ok:true}
//
// There is NO endpoint that returns the decrypted blob. T8 is the only
// consumer: it calls secrets.OpenJSON internally at PTY-spawn time.
//
// masterKey comes from Server (set in New from secrets.MasterKey(envLookup) in
// main.go). If it is nil the credential POST/PATCH return 500 with a clear
// message rather than silently failing or storing plaintext.

// credentialSecretFields is the struct that gets JSON-marshalled and sealed.
// Field order matches the request body for readability; JSON tags pin the wire
// names. NOTE: this struct NEVER appears in any HTTP response — only its sealed
// form is persisted.
type credentialSecretFields struct {
	APIKey     string `json:"api_key,omitempty"`
	AuthToken  string `json:"auth_token,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
	HTTPProxy  string `json:"http_proxy,omitempty"`
	HTTPSProxy string `json:"https_proxy,omitempty"`
	AllProxy   string `json:"all_proxy,omitempty"`
}

// createCredentialReq is POST /api/admin/credentials. The secret fields are
// pointers so we can detect "field present" vs "field absent" on PATCH (where
// only some fields may be re-sealed). On POST we accept either form.
type createCredentialReq struct {
	Name       string  `json:"name"`
	Note       string  `json:"note"`
	APIKey     *string `json:"api_key,omitempty"`
	AuthToken  *string `json:"auth_token,omitempty"`
	BaseURL    *string `json:"base_url,omitempty"`
	HTTPProxy  *string `json:"http_proxy,omitempty"`
	HTTPSProxy *string `json:"https_proxy,omitempty"`
	AllProxy   *string `json:"all_proxy,omitempty"`
}

// updateCredentialReq is PATCH /api/admin/credentials/:id. Every field is
// optional; nil means "leave unchanged".
type updateCredentialReq struct {
	Name       *string `json:"name,omitempty"`
	Note       *string `json:"note,omitempty"`
	APIKey     *string `json:"api_key,omitempty"`
	AuthToken  *string `json:"auth_token,omitempty"`
	BaseURL    *string `json:"base_url,omitempty"`
	HTTPProxy  *string `json:"http_proxy,omitempty"`
	HTTPSProxy *string `json:"https_proxy,omitempty"`
	AllProxy   *string `json:"all_proxy,omitempty"`
}

// credentialListEntry is the ONLY shape ever returned to the client. Adding a
// field here is a security review — never add api_key/encrypted_blob/etc.
type credentialListEntry struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
}

// hasAnySecretField reports whether the create req carries at least one secret
// field. On POST we allow a preset with NO secrets (just name+note) so an admin
// can stage a placeholder; the actual sealing still runs (over an empty struct)
// so the blob column is always populated.
func (b *createCredentialReq) hasAnySecretField() bool {
	return b.APIKey != nil || b.AuthToken != nil || b.BaseURL != nil ||
		b.HTTPProxy != nil || b.HTTPSProxy != nil || b.AllProxy != nil
}

// toSecretFields builds the sealed-struct from the create req.
func (b *createCredentialReq) toSecretFields() credentialSecretFields {
	return credentialSecretFields{
		APIKey:     derefStr(b.APIKey),
		AuthToken:  derefStr(b.AuthToken),
		BaseURL:    derefStr(b.BaseURL),
		HTTPProxy:  derefStr(b.HTTPProxy),
		HTTPSProxy: derefStr(b.HTTPSProxy),
		AllProxy:   derefStr(b.AllProxy),
	}
}

// hasAnySecretField for the update req — used to decide whether to re-seal.
func (b *updateCredentialReq) hasAnySecretField() bool {
	return b.APIKey != nil || b.AuthToken != nil || b.BaseURL != nil ||
		b.HTTPProxy != nil || b.HTTPSProxy != nil || b.AllProxy != nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) handleAdminListCredentials(w http.ResponseWriter, _ *http.Request) {
	list, err := s.db.ListPresets()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "list credentials failed"})
		return
	}
	// CRITICAL: build credentialListEntry from each row. We never marshal the
	// raw CredentialPreset (which carries EncryptedBlob) — that would leak the
	// ciphertext to the client.
	out := make([]credentialListEntry, 0, len(list))
	for _, p := range list {
		out = append(out, credentialListEntry{
			ID:        p.ID,
			Name:      p.Name,
			Note:      p.Note,
			CreatedAt: p.CreatedAt,
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleAdminCreateCredential(w http.ResponseWriter, r *http.Request) {
	var b createCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	if b.Name == "" {
		writeJSON(w, 400, map[string]any{"error": "name required"})
		return
	}
	if s.masterKey == nil {
		// Do NOT fall back to plaintext — fail loudly so the misconfiguration
		// is obvious. The masterKey is loaded once at startup; a nil here means
		// MASTER_KEY was never set.
		writeJSON(w, 500, map[string]any{"error": "credential storage not configured (MASTER_KEY missing)"})
		return
	}
	blob, err := secrets.SealJSON(s.masterKey, b.toSecretFields())
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "seal failed"})
		return
	}
	created, err := s.db.CreatePreset(store.CredentialPreset{
		Name:          b.Name,
		EncryptedBlob: blob,
		Note:          b.Note,
	})
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "create credential failed"})
		return
	}
	// Response intentionally minimal — NEVER echo back the secrets.
	writeJSON(w, 201, map[string]any{
		"id":   created.ID,
		"name": created.Name,
	})
}

func (s *Server) handleAdminUpdateCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid id"})
		return
	}
	var b updateCredentialReq
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]any{"error": "bad body"})
		return
	}
	// Fetch existing so we can merge secret fields (PATCH is partial: if only
	// api_key is provided, the previously-sealed auth_token etc. must survive).
	existing, err := s.db.GetPreset(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "get credential failed"})
		return
	}

	patch := store.PresetPatch{
		Name: b.Name,
		Note: b.Note,
	}

	// If any secret field is present, we must re-seal. We merge onto the
	// previously-sealed struct so omitted secret fields are preserved.
	if b.hasAnySecretField() {
		if s.masterKey == nil {
			writeJSON(w, 500, map[string]any{"error": "credential storage not configured (MASTER_KEY missing)"})
			return
		}
		// Decrypt the existing blob to get the current secret struct.
		var cur credentialSecretFields
		if len(existing.EncryptedBlob) > 0 {
			if err := secrets.OpenJSON(s.masterKey, existing.EncryptedBlob, &cur); err != nil {
				writeJSON(w, 500, map[string]any{"error": "open existing credential failed"})
				return
			}
		}
		// Overlay the provided fields.
		if b.APIKey != nil {
			cur.APIKey = *b.APIKey
		}
		if b.AuthToken != nil {
			cur.AuthToken = *b.AuthToken
		}
		if b.BaseURL != nil {
			cur.BaseURL = *b.BaseURL
		}
		if b.HTTPProxy != nil {
			cur.HTTPProxy = *b.HTTPProxy
		}
		if b.HTTPSProxy != nil {
			cur.HTTPSProxy = *b.HTTPSProxy
		}
		if b.AllProxy != nil {
			cur.AllProxy = *b.AllProxy
		}
		blob, err := secrets.SealJSON(s.masterKey, cur)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": "seal failed"})
			return
		}
		patch.EncryptedBlob = blob
	}

	if err := s.db.UpdatePreset(id, patch); err != nil {
		writeJSON(w, 500, map[string]any{"error": "update credential failed"})
		return
	}
	// Reload for the response (so name/note reflect the merged state).
	updated, err := s.db.GetPreset(id)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	// Response carries ONLY id/name/note — never the blob or secrets.
	writeJSON(w, 200, credentialListEntry{
		ID:        updated.ID,
		Name:      updated.Name,
		Note:      updated.Note,
		CreatedAt: updated.CreatedAt,
	})
}

func (s *Server) handleAdminDeleteCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid id"})
		return
	}
	if _, err := s.db.GetPreset(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"error": "get credential failed"})
		return
	}
	if err := s.db.DeletePreset(id); err != nil {
		writeJSON(w, 500, map[string]any{"error": "delete credential failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

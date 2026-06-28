package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// credential-preset admin CRUD tests (T7)
// ---------------------------------------------------------------------------
//
// SECURITY INVARIANTS verified here:
//   - GET /api/admin/credentials returns ONLY {id,name,note,created_at}.
//     The encrypted_blob and any plaintext secret field (api_key, auth_token,
//     base_url, *_proxy) MUST NEVER appear in the response.
//   - POST/PATCH seal the secret fields with the server's masterKey.
//   - The plaintext cannot be read back via the API (only id/name/note).

// testMasterKey is a fixed 32-byte AES-256-GCM key for the credential tests.
// Using a package-level var keeps the test deterministic without touching env.
var testMasterKey = bytes32(0x42)

func bytes32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}

// newCredTestServer builds a Server with masterKey set (unlike
// newTestServerWithAdmin which passes nil), so the credential endpoints can
// actually seal/open blobs.
func newCredTestServer(t *testing.T) *Server {
	s, _, _ := newTestServerWithAdmin(t)
	s.masterKey = testMasterKey
	return s
}

func postCredential(t *testing.T, s *Server, cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/admin/credentials", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestAdminCredentials_NonAdmin_Forbidden(t *testing.T) {
	s := newCredTestServer(t)
	w := postCredential(t, s, userCookie(t, s), `{"name":"x"}`)
	if w.Code != 403 {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminCredentials_CreateAndGetList_NoSecretsLeaked(t *testing.T) {
	s := newCredTestServer(t)
	cookie := adminCookie(t, s)

	// Create a preset with several secret fields.
	body := `{"name":"anthropic-prod","api_key":"sk-ant-SECRET","auth_token":"tok-SECRET","base_url":"https://api.anthropic.com","http_proxy":"http://proxy:8080","https_proxy":"https://proxy:8443","all_proxy":"socks5://proxy:1080","note":"prod key"}`
	w := postCredential(t, s, cookie, body)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created["id"] == nil {
		t.Fatal("expected id")
	}
	if created["name"] != "anthropic-prod" {
		t.Fatalf("expected name anthropic-prod, got %v", created["name"])
	}
	// Create response must NOT leak any secret.
	assertNoSecretFields(t, "create response", w.Body.String())

	// --- GET list ---
	req := httptest.NewRequest("GET", "/api/admin/credentials", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	bodyStr := w.Body.String()
	assertNoSecretFields(t, "list response", bodyStr)

	var list []map[string]any
	if err := json.NewDecoder(strings.NewReader(bodyStr)).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 preset, got %d", len(list))
	}
	if list[0]["name"] != "anthropic-prod" {
		t.Fatalf("expected name anthropic-prod, got %v", list[0]["name"])
	}
	if list[0]["note"] != "prod key" {
		t.Fatalf("expected note 'prod key', got %v", list[0]["note"])
	}
	// Every entry must have exactly {id,name,note,created_at} and nothing else.
	for k := range list[0] {
		switch k {
		case "id", "name", "note", "created_at":
		default:
			t.Fatalf("list entry leaked field %q", k)
		}
	}
}

// TestAdminCredentials_BlobSealedInDB verifies the POST actually persisted a
// non-empty encrypted_blob AND that the blob does not contain the plaintext.
func TestAdminCredentials_BlobSealedInDB(t *testing.T) {
	s, _, db := newTestServerWithAdmin(t)
	s.masterKey = testMasterKey
	cookie := adminCookie(t, s)

	body := `{"name":"sealed","api_key":"sk-PLAINTEXT-MARKER","note":"n"}`
	w := postCredential(t, s, cookie, body)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d; body=%s", w.Code, w.Body.String())
	}

	// Pull the row directly from the DB and inspect the blob.
	presets, err := db.ListPresets()
	if err != nil {
		t.Fatalf("list presets: %v", err)
	}
	if len(presets) != 1 {
		t.Fatalf("expected 1 preset row, got %d", len(presets))
	}
	p := presets[0]
	if len(p.EncryptedBlob) == 0 {
		t.Fatal("encrypted_blob is empty — seal did not persist")
	}
	if len(p.EncryptedBlob) < 12+16 {
		t.Fatalf("encrypted_blob too short: %d bytes (need >= 28)", len(p.EncryptedBlob))
	}
	// The plaintext marker must NOT be present in the stored blob.
	if strings.Contains(string(p.EncryptedBlob), "PLAINTEXT-MARKER") {
		t.Fatal("encrypted_blob contains the plaintext marker — seal failed")
	}
}

// TestAdminCredentials_PlaintextNotReadableViaAPI verifies there is NO endpoint
// that returns the decrypted secret. We probe GET (list) and PATCH after
// creating a preset; neither should return api_key etc.
func TestAdminCredentials_PlaintextNotReadableViaAPI(t *testing.T) {
	s := newCredTestServer(t)
	cookie := adminCookie(t, s)

	// Seed via POST.
	if w := postCredential(t, s, cookie, `{"name":"rt","api_key":"sk-CANNOT-READ-THIS"}`); w.Code != 201 {
		t.Fatalf("seed: expected 201, got %d; body=%s", w.Code, w.Body.String())
	}

	// GET list — must not contain the secret.
	req := httptest.NewRequest("GET", "/api/admin/credentials", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	assertNoSecretFields(t, "GET list", w.Body.String())
	if strings.Contains(w.Body.String(), "CANNOT-READ-THIS") {
		t.Fatal("GET list leaked the plaintext secret")
	}

	// Parse the id.
	var list []map[string]any
	_ = json.NewDecoder(strings.NewReader(w.Body.String())).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected 1 preset, got %d", len(list))
	}
	id := itoa(int(list[0]["id"].(float64)))

	// PATCH — the response must also not leak.
	patchReq := httptest.NewRequest("PATCH", "/api/admin/credentials/"+id, strings.NewReader(`{"note":"updated"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchReq.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, patchReq)
	if w.Code != 200 {
		t.Fatalf("patch: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	assertNoSecretFields(t, "PATCH response", w.Body.String())
}

func TestAdminCredentials_UpdateReSealsAndDelete(t *testing.T) {
	s, _, db := newTestServerWithAdmin(t)
	s.masterKey = testMasterKey
	cookie := adminCookie(t, s)

	// Create.
	w := postCredential(t, s, cookie, `{"name":"orig","api_key":"sk-ONE"}`)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d; body=%s", w.Code, w.Body.String())
	}
	var created map[string]any
	_ = json.NewDecoder(w.Body).Decode(&created)
	id := itoa(int(created["id"].(float64)))

	// Get the original blob.
	before, _ := db.ListPresets()
	blobBefore := append([]byte(nil), before[0].EncryptedBlob...)

	// PATCH with a new secret — must re-seal (blob changes) and not leak.
	patchBody := `{"api_key":"sk-TWO","note":"re-sealed"}`
	req := httptest.NewRequest("PATCH", "/api/admin/credentials/"+id, strings.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("patch: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	assertNoSecretFields(t, "PATCH response", w.Body.String())

	// Verify the blob changed (re-sealed).
	after, _ := db.ListPresets()
	if len(after) != 1 {
		t.Fatalf("expected 1 row, got %d", len(after))
	}
	if string(after[0].EncryptedBlob) == string(blobBefore) {
		t.Fatal("encrypted_blob did not change after PATCH with new secret")
	}
	if after[0].Note != "re-sealed" {
		t.Fatalf("expected note 're-sealed', got %q", after[0].Note)
	}

	// DELETE.
	req = httptest.NewRequest("DELETE", "/api/admin/credentials/"+id, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: cookie})
	w = httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d; body=%s", w.Code, w.Body.String())
	}
	// Row gone.
	rows, _ := db.ListPresets()
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows after delete, got %d", len(rows))
	}
}

func TestAdminCredentials_CreateValidation(t *testing.T) {
	s := newCredTestServer(t)
	cookie := adminCookie(t, s)
	w := postCredential(t, s, cookie, `{"api_key":"sk-x"}`)
	if w.Code != 400 {
		t.Fatalf("missing name: expected 400, got %d; body=%s", w.Code, w.Body.String())
	}
}

func TestAdminCredentials_NoMasterKey(t *testing.T) {
	// masterKey is nil — POST must fail clearly (not 201, not panic).
	s, _, _ := newTestServerWithAdmin(t)
	// s.masterKey stays nil (newTestServerWithAdmin passes nil).
	cookie := adminCookie(t, s)
	w := postCredential(t, s, cookie, `{"name":"x","api_key":"sk-x"}`)
	if w.Code == 201 {
		t.Fatalf("expected non-201 when masterKey is nil, got 201; body=%s", w.Body.String())
	}
}

// assertNoSecretFields fails the test if body contains any key that should
// never leave the server. We check both the JSON keys and the literal plaintext
// markers used in fixtures.
func assertNoSecretFields(t *testing.T, where, body string) {
	t.Helper()
	banned := []string{
		`"api_key"`, `"auth_token"`, `"base_url"`,
		`"http_proxy"`, `"https_proxy"`, `"all_proxy"`,
		`"encrypted_blob"`,
	}
	for _, b := range banned {
		if strings.Contains(body, b) {
			t.Fatalf("%s leaked secret field %s; body=%s", where, b, body)
		}
	}
}

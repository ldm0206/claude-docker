package store

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestCreateAndGetPreset(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	blob := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0xFF}
	p := CredentialPreset{
		Name:          "api-key-1",
		EncryptedBlob: blob,
		Note:          "test preset",
	}
	created, err := db.CreatePreset(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}
	if created.CreatedAt == 0 {
		t.Fatal("expected CreatedAt to be set")
	}

	got, err := db.GetPreset(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "api-key-1" || got.Note != "test preset" {
		t.Fatalf("got name=%q note=%q, want api-key-1 / test preset", got.Name, got.Note)
	}
	// Blob must be returned verbatim
	if !bytes.Equal(got.EncryptedBlob, blob) {
		t.Fatalf("blob mismatch: got %x, want %x", got.EncryptedBlob, blob)
	}
}

func TestGetPresetNotFound(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.GetPreset(999)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListPresets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	list, err := db.ListPresets()
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	db.CreatePreset(CredentialPreset{Name: "p1", EncryptedBlob: []byte{0x01}, Note: "a"})
	db.CreatePreset(CredentialPreset{Name: "p2", EncryptedBlob: []byte{0x02}, Note: "b"})

	list, err = db.ListPresets()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(list))
	}
}

func TestDeletePreset(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	created, _ := db.CreatePreset(CredentialPreset{Name: "del", EncryptedBlob: []byte{0xAA}, Note: ""})
	if err := db.DeletePreset(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = db.GetPreset(created.ID)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestPresetBlobNotMutated(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	original := []byte{0xCA, 0xFE, 0xBA, 0xBE}
	p := CredentialPreset{Name: "blob-test", EncryptedBlob: original, Note: ""}
	created, err := db.CreatePreset(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, _ := db.GetPreset(created.ID)
	if !bytes.Equal(got.EncryptedBlob, original) {
		t.Fatalf("blob mutated: got %x, want %x", got.EncryptedBlob, original)
	}

	// Also check via ListPresets
	list, _ := db.ListPresets()
	found := false
	for _, lp := range list {
		if lp.ID == created.ID {
			found = true
			if !bytes.Equal(lp.EncryptedBlob, original) {
				t.Fatalf("list blob mutated: got %x, want %x", lp.EncryptedBlob, original)
			}
		}
	}
	if !found {
		t.Fatal("preset not found in list")
	}
}

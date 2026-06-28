package store

import (
	"path/filepath"
	"testing"
)

func TestCreateAndGetTemplate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	tmpl := RoleTemplate{
		Name:           "basic",
		DiskQuotaBytes: 10 << 30,
		CPUQuota:       "1.0",
		MemoryMaxBytes: 512 << 20,
		MaxSessions:    5,
		Permissions:    `{"allow":["read"]}`,
	}
	created, err := db.CreateTemplate(tmpl)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}
	if created.CreatedAt == 0 {
		t.Fatal("expected CreatedAt to be set")
	}

	got, err := db.GetTemplate(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "basic" || got.DiskQuotaBytes != 10<<30 || got.CPUQuota != "1.0" || got.MaxSessions != 5 {
		t.Fatalf("got %+v, want basic template", got)
	}
}

func TestGetTemplateNotFound(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	_, err = db.GetTemplate(999)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListTemplates(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	list, err := db.ListTemplates()
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	db.CreateTemplate(RoleTemplate{Name: "a", DiskQuotaBytes: 1, CPUQuota: "0.5", MemoryMaxBytes: 2, MaxSessions: 3, Permissions: "{}"})
	db.CreateTemplate(RoleTemplate{Name: "b", DiskQuotaBytes: 4, CPUQuota: "1.0", MemoryMaxBytes: 5, MaxSessions: 6, Permissions: "{}"})

	list, err = db.ListTemplates()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(list))
	}
}

func TestDeleteTemplate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	created, _ := db.CreateTemplate(RoleTemplate{Name: "x", DiskQuotaBytes: 1, CPUQuota: "1", MemoryMaxBytes: 1, MaxSessions: 1, Permissions: "{}"})
	if err := db.DeleteTemplate(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = db.GetTemplate(created.ID)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestCreateTemplateSetsCreatedAt(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	created, err := db.CreateTemplate(RoleTemplate{Name: "ts", DiskQuotaBytes: 0, CPUQuota: "1", MemoryMaxBytes: 0, MaxSessions: 1, Permissions: "{}", CreatedAt: 0})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedAt == 0 {
		t.Fatal("CreatedAt should be auto-set when zero")
	}

	// Explicit CreatedAt preserved
	explicit, err := db.CreateTemplate(RoleTemplate{Name: "ts2", DiskQuotaBytes: 0, CPUQuota: "1", MemoryMaxBytes: 0, MaxSessions: 1, Permissions: "{}", CreatedAt: 42})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if explicit.CreatedAt != 42 {
		t.Fatalf("explicit CreatedAt = %d, want 42", explicit.CreatedAt)
	}
}

package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// tmpBackupDir creates a throwaway backup directory backed by a real
// filesystem, so the registry's JSON file actually lands on disk.
func tmpBackupDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "names-test")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestNameRegistry_AssignPersistsOnPiSide(t *testing.T) {
	backupPath := tmpBackupDir(t)
	realFS := fs.NewRealFileSystem()
	reg := newNameRegistry(realFS, backupPath)

	// First assignment mints and persists a name.
	first, err := reg.Assign("UUID-abc", "ABC123")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if first == "" {
		t.Fatal("expected non-empty name")
	}

	// The map file must exist on the Pi side (inside the backup dir), not on
	// any source device.
	if _, err := os.Stat(filepath.Join(backupPath, ".device-names.json")); err != nil {
		t.Fatalf("expected name map on Pi side: %v", err)
	}

	// A second lookup for the same key returns the same name (stable across
	// insertions).
	again, err := reg.Lookup("UUID-abc")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if again != first {
		t.Fatalf("expected stable name %q, got %q", first, again)
	}
}

func TestNameRegistry_AssignReusesExisting(t *testing.T) {
	backupPath := tmpBackupDir(t)
	realFS := fs.NewRealFileSystem()
	reg := newNameRegistry(realFS, backupPath)

	first, _ := reg.Assign("UUID-xyz", "ABC123")
	// A later assign with a different suggested name must NOT overwrite the
	// already-stored mapping for that device.
	again, _ := reg.Assign("UUID-xyz", "DIFFERENT")
	if again != first {
		t.Fatalf("expected existing name %q preserved, got %q", first, again)
	}
}

func TestNameRegistry_AssignEmptySuggestedFails(t *testing.T) {
	backupPath := tmpBackupDir(t)
	realFS := fs.NewRealFileSystem()
	reg := newNameRegistry(realFS, backupPath)

	if _, err := reg.Assign("UUID-none", ""); err == nil {
		t.Fatal("expected error when no name is available for a new device")
	}
}

func TestNameRegistry_MissingFileIsNotAnError(t *testing.T) {
	backupPath := tmpBackupDir(t)
	realFS := fs.NewRealFileSystem()
	reg := newNameRegistry(realFS, backupPath)

	// Lookup before any file exists must return "" with no error.
	name, err := reg.Lookup("never-seen")
	if err != nil {
		t.Fatalf("lookup on missing map: %v", err)
	}
	if name != "" {
		t.Fatalf("expected empty name, got %q", name)
	}
}

func TestNameRegistry_CorruptFileStartsFresh(t *testing.T) {
	backupPath := tmpBackupDir(t)
	realFS := fs.NewRealFileSystem()
	reg := newNameRegistry(realFS, backupPath)

	// Write a corrupt JSON file in place of the map.
	if err := os.WriteFile(filepath.Join(backupPath, ".device-names.json"), []byte("{not json"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	// Lookup must not blow up; it returns "" as if empty.
	name, err := reg.Lookup("anything")
	if err != nil {
		t.Fatalf("lookup on corrupt map: %v", err)
	}
	if name != "" {
		t.Fatalf("expected empty name on corrupt map, got %q", name)
	}

	// A subsequent assign recovers and rewrites a valid file.
	if _, err := reg.Assign("UUID-recover", "ABC123"); err != nil {
		t.Fatalf("assign after corrupt: %v", err)
	}
}

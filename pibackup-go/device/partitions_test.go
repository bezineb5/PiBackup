package device

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	backuppkg "github.com/benjamin/pibackup/pibackup-go/backup"
	fspkg "github.com/benjamin/pibackup/pibackup-go/fs"
	mountpkg "github.com/benjamin/pibackup/pibackup-go/mount"
)

// testLogger returns a quiet logger for partition tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// partitionsTestService builds a device.Service backed by a mock filesystem,
// with /sys/block/<device> populated by the given dir entries.
func partitionsTestService(t *testing.T, device string, entries []fs.DirEntry) *Service {
	t.Helper()
	mockFS := fspkg.NewMockFileSystem()
	mockFS.ReadDirReturns["/sys/block"] = []fs.DirEntry{fspkg.NewMockDirEntry(device, true)}
	mockFS.ReadDirReturns[filepath.Join("/sys/block", device)] = entries
	mountSvc := mountpkg.NewService(testLogger(), mockFS, false)
	backupSvc := backuppkg.NewService(testLogger(), mockFS, nil, &backuppkg.Config{BackupPath: "/tmp/backups"})
	return NewService(testLogger(), mockFS, nil, &Config{
		MountPath:  "/media",
		BackupPath: "/tmp/backups",
	}, mountSvc, backupSvc)
}

func TestPartitions_RecognisesPartitions(t *testing.T) {
	// sda with partitions sda1, sda2 plus some non-partition dirs.
	svc := partitionsTestService(t, "sda", []fs.DirEntry{
		fspkg.NewMockDirEntry("sda1", true),
		fspkg.NewMockDirEntry("sda2", true),
		fspkg.NewMockDirEntry("device", true), // not a partition (no digit suffix)
		fspkg.NewMockDirEntry("power", true),
		fspkg.NewMockDirEntry("queue", true),
	})

	parts := svc.partitions("sda")
	if len(parts) != 2 {
		t.Fatalf("expected 2 partitions, got %d (%v)", len(parts), parts)
	}
	if parts[0] != "sda1" || parts[1] != "sda2" {
		t.Fatalf("expected [sda1 sda2], got %v", parts)
	}
}

func TestPartitions_SortedRegardlessOfOrder(t *testing.T) {
	// Entries returned in a non-sorted order must come back sorted.
	svc := partitionsTestService(t, "sda", []fs.DirEntry{
		fspkg.NewMockDirEntry("sda3", true),
		fspkg.NewMockDirEntry("sda1", true),
		fspkg.NewMockDirEntry("sda2", true),
	})

	parts := svc.partitions("sda")
	want := []string{"sda1", "sda2", "sda3"}
	if len(parts) != len(want) {
		t.Fatalf("expected %d partitions, got %d (%v)", len(want), len(parts), parts)
	}
	for i, w := range want {
		if parts[i] != w {
			t.Fatalf("partitions[%d] = %q, want %q (full: %v)", i, parts[i], w, parts)
		}
	}
}

func TestPartitions_NoPartitions(t *testing.T) {
	// A device with only non-partition subdirectories.
	svc := partitionsTestService(t, "sda", []fs.DirEntry{
		fspkg.NewMockDirEntry("device", true),
		fspkg.NewMockDirEntry("power", true),
	})

	parts := svc.partitions("sda")
	if len(parts) != 0 {
		t.Fatalf("expected no partitions, got %v", parts)
	}
}

func TestPartitions_ReadDirErrorReturnsEmpty(t *testing.T) {
	// Device not present in the mock: ReadDir fails.
	mockFS := fspkg.NewMockFileSystem()
	mountSvc := mountpkg.NewService(testLogger(), mockFS, false)
	backupSvc := backuppkg.NewService(testLogger(), mockFS, nil, &backuppkg.Config{BackupPath: "/tmp/backups"})
	svc := NewService(testLogger(), mockFS, nil, &Config{
		MountPath: "/media", BackupPath: "/tmp/backups",
	}, mountSvc, backupSvc)

	parts := svc.partitions("sda")
	if parts != nil {
		t.Fatalf("expected nil on read error, got %v", parts)
	}
}

func TestHasPartitions_AndFirstPartition_DeriveFromPartitions(t *testing.T) {
	svc := partitionsTestService(t, "sda", []fs.DirEntry{
		fspkg.NewMockDirEntry("sda2", true),
		fspkg.NewMockDirEntry("sda1", true),
	})

	if !svc.hasPartitions("sda") {
		t.Fatal("hasPartitions: expected true")
	}
	if got := svc.firstPartition("sda"); got != "sda1" {
		t.Fatalf("firstPartition = %q, want %q (must be the lowest, not the dir order)", got, "sda1")
	}

	// Empty device.
	svcEmpty := partitionsTestService(t, "sdb", nil)
	if svcEmpty.hasPartitions("sdb") {
		t.Fatal("hasPartitions: expected false for empty device")
	}
	if got := svcEmpty.firstPartition("sdb"); got != "" {
		t.Fatalf("firstPartition on empty = %q, want \"\"", got)
	}
}

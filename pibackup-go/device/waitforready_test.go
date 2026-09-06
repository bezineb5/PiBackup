package device

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	backuppkg "github.com/benjamin/pibackup/pibackup-go/backup"
	fspkg "github.com/benjamin/pibackup/pibackup-go/fs"
	mountpkg "github.com/benjamin/pibackup/pibackup-go/mount"
)

// readyTestService builds a device.Service wired to a mock filesystem with the
// minimum needed to exercise WaitForReady: device detection (via the USB
// subsystem symlink) and medium presence (via the size file).
func readyTestService(mockFS *fspkg.MockFileSystem) *Service {
	logger := slog.New(slog.NewTextHandler(&devNull{}, &slog.HandlerOptions{Level: slog.LevelError}))
	mountSvc := mountpkg.NewService(logger, mockFS, false)
	backupSvc := backuppkg.NewService(logger, mockFS, nil, &backuppkg.Config{BackupPath: "/tmp/backups"})
	return NewService(logger, mockFS, nil, &Config{
		MountPath:  "/media",
		BackupPath: "/tmp/backups",
	}, mountSvc, backupSvc)
}

// markReady configures the mock so the given device looks like USB storage
// with a medium present: a /sys/block/<dev> directory, a USB subsystem
// symlink, and a non-zero size file.
func markReady(mockFS *fspkg.MockFileSystem, device string) {
	mockFS.StatReturns[filepath.Join("/sys/block", device)] = fspkg.NewMockFileInfo(device, 0, true)
	mockFS.ReadDirReturns["/sys/block"] = []fs.DirEntry{fspkg.NewMockDirEntry(device, true)}
	// No partition subdirectories.
	mockFS.ReadDirReturns[filepath.Join("/sys/block", device)] = nil
	// USB subsystem symlink target.
	mockFS.ReadlinkReturns[filepath.Join("/sys/block", device, "device", "subsystem")] = "/sys/bus/usb"
	// Non-zero medium size.
	mockFS.ReadFileReturns[filepath.Join("/sys/block", device, "size")] = []byte("1024")
}

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

func TestWaitForReady_Immediate(t *testing.T) {
	mockFS := fspkg.NewMockFileSystem()
	svc := readyTestService(mockFS)
	markReady(mockFS, "sda")

	if !svc.WaitForReady(context.Background(), "sda") {
		t.Fatal("expected device to be ready immediately")
	}
}

func TestWaitForReady_NeverReady(t *testing.T) {
	mockFS := fspkg.NewMockFileSystem()
	svc := readyTestService(mockFS)

	// Shorten the retry budget so the test completes quickly.
	prevAttempts, prevInterval := readyRetryAttempts, readyRetryInterval
	readyRetryAttempts, readyRetryInterval = 3, 10 * time.Millisecond
	readyRetryMaxInterval = 50 * time.Millisecond
	t.Cleanup(func() {
		readyRetryAttempts, readyRetryInterval = prevAttempts, prevInterval
		readyRetryMaxInterval = 2 * time.Second
	})

	// Device absent: IsUSBStorage returns false every poll, so WaitForReady
	// must exhaust its attempts and return false (not hang).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if svc.WaitForReady(ctx, "sda") {
		t.Fatal("expected device to never become ready")
	}
}

func TestWaitForReady_RespectsContextCancellation(t *testing.T) {
	mockFS := fspkg.NewMockFileSystem()
	svc := readyTestService(mockFS)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if svc.WaitForReady(ctx, "sda") {
		t.Fatal("expected not ready after cancellation")
	}
	// Must return well before the full retry budget (~2s+).
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("WaitForReady did not honour cancellation: took %v", elapsed)
	}
}

func TestWaitForReady_BecomesReadyAfterDelay(t *testing.T) {
	mockFS := fspkg.NewMockFileSystem()
	svc := readyTestService(mockFS)

	// Shorten the retry budget so the test completes quickly.
	prevAttempts, prevInterval := readyRetryAttempts, readyRetryInterval
	readyRetryAttempts = 20
	readyRetryInterval = 50 * time.Millisecond
	readyRetryMaxInterval = 200 * time.Millisecond
	t.Cleanup(func() {
		readyRetryAttempts, readyRetryInterval = prevAttempts, prevInterval
		readyRetryMaxInterval = 2 * time.Second
	})

	go func() {
		time.Sleep(100 * time.Millisecond)
		markReady(mockFS, "sda")
	}()

	if !svc.WaitForReady(context.Background(), "sda") {
		t.Fatal("expected device to become ready after the delay")
	}
}

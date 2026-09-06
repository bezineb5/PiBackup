package backup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/benjamin/pibackup/pibackup-go/fs"
)

// nameRegistry persists a mapping from a device's stable identifier (its blkid
// UUID or serial) to a friendly backup name, entirely on the Pi side. It lets
// us produce consistent backup folder names across insertions without ever
// writing back to the source card.
type nameRegistry struct {
	path string
	fs   fs.FileSystem
	mu   sync.Mutex
}

type nameMap map[string]string

// newNameRegistry creates a registry backed by a JSON file stored alongside
// the backups (so it lives on the destination disk, never on the source).
func newNameRegistry(fs fs.FileSystem, backupPath string) *nameRegistry {
	return &nameRegistry{
		path: filepath.Join(backupPath, ".device-names.json"),
		fs:   fs,
	}
}

// Lookup returns the name previously stored for the given deviceKey, or "".
func (r *nameRegistry) Lookup(deviceKey string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, err := r.read()
	if err != nil {
		return "", err
	}
	return m[deviceKey], nil
}

// Assign returns the existing name for deviceKey if one exists, otherwise
// generates a new name, persists the mapping, and returns it.
func (r *nameRegistry) Assign(deviceKey, suggested string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	m, err := r.read()
	if err != nil {
		return "", err
	}

	if name, ok := m[deviceKey]; ok && name != "" {
		return name, nil
	}

	name := suggested
	if name == "" {
		return "", fmt.Errorf("no name available for device %q", deviceKey)
	}
	m[deviceKey] = name

	if err := r.write(m); err != nil {
		return "", fmt.Errorf("persist device name: %w", err)
	}
	return name, nil
}

// read loads the map from disk. A missing file is treated as an empty map.
func (r *nameRegistry) read() (nameMap, error) {
	data, err := r.fs.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nameMap{}, nil
		}
		return nil, fmt.Errorf("read device name map %s: %w", r.path, err)
	}

	var m nameMap
	if err := json.Unmarshal(data, &m); err != nil {
		// A corrupt map should not block backups; start fresh.
		return nameMap{}, nil
	}
	if m == nil {
		return nameMap{}, nil
	}
	return m, nil
}

// write atomically persists the map. The FileSystem abstraction has no
// atomic-replace primitive, so we write directly; the file is small and the
// window for corruption is minimal. Callers hold the mutex.
func (r *nameRegistry) write(m nameMap) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return r.fs.WriteFile(r.path, data, 0644)
}

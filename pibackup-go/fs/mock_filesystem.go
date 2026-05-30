package fs

import (
	"io/fs"
	"os"
	"sync"
	"time"
)

// MockFileSystem is a mock implementation of FileSystem for testing
type MockFileSystem struct {
	mu sync.Mutex

	// Call tracking
	OpenCalls     []string
	StatCalls     []string
	ReadFileCalls []string
	ReadDirCalls  []string
	MkdirAllCalls []struct {
		path string
		perm os.FileMode
	}
	WriteFileCalls []struct {
		name string
		data []byte
		perm os.FileMode
	}
	RemoveCalls    []string
	RemoveAllCalls []string
	ReadlinkCalls  []string

	// Return value configuration
	OpenReturns     map[string]fs.File
	OpenErrors      map[string]error
	StatReturns     map[string]fs.FileInfo
	StatErrors      map[string]error
	ReadFileReturns map[string][]byte
	ReadFileErrors  map[string]error
	ReadDirReturns  map[string][]fs.DirEntry
	ReadDirErrors   map[string]error
	MkdirAllErrors  map[string]error
	WriteFileErrors map[string]error
	RemoveErrors    map[string]error
	RemoveAllErrors map[string]error
	ReadlinkReturns map[string]string
	ReadlinkErrors  map[string]error
}

// Open implements fs.FS
func (m *MockFileSystem) Open(name string) (fs.File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.OpenCalls = append(m.OpenCalls, name)
	if f, ok := m.OpenReturns[name]; ok {
		return f, m.OpenErrors[name]
	}
	return nil, m.OpenErrors[name]
}

// Stat implements fs.StatFS
func (m *MockFileSystem) Stat(name string) (fs.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.StatCalls = append(m.StatCalls, name)
	if info, ok := m.StatReturns[name]; ok {
		return info, m.StatErrors[name]
	}
	return nil, m.StatErrors[name]
}

// ReadFile implements fs.ReadFileFS
func (m *MockFileSystem) ReadFile(name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ReadFileCalls = append(m.ReadFileCalls, name)
	if data, ok := m.ReadFileReturns[name]; ok {
		return data, m.ReadFileErrors[name]
	}
	return nil, m.ReadFileErrors[name]
}

// ReadDir implements fs.FS
func (m *MockFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ReadDirCalls = append(m.ReadDirCalls, name)
	if entries, ok := m.ReadDirReturns[name]; ok {
		return entries, m.ReadDirErrors[name]
	}
	return nil, m.ReadDirErrors[name]
}

// MkdirAll implements FileSystem
func (m *MockFileSystem) MkdirAll(path string, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.MkdirAllCalls = append(m.MkdirAllCalls, struct {
		path string
		perm os.FileMode
	}{path, perm})
	return m.MkdirAllErrors[path]
}

// WriteFile implements FileSystem
func (m *MockFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.WriteFileCalls = append(m.WriteFileCalls, struct {
		name string
		data []byte
		perm os.FileMode
	}{name, data, perm})
	return m.WriteFileErrors[name]
}

// Remove implements FileSystem
func (m *MockFileSystem) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RemoveCalls = append(m.RemoveCalls, name)
	return m.RemoveErrors[name]
}

// RemoveAll implements FileSystem
func (m *MockFileSystem) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.RemoveAllCalls = append(m.RemoveAllCalls, path)
	return m.RemoveAllErrors[path]
}

// Readlink implements FileSystem
func (m *MockFileSystem) Readlink(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ReadlinkCalls = append(m.ReadlinkCalls, name)
	if target, ok := m.ReadlinkReturns[name]; ok {
		return target, m.ReadlinkErrors[name]
	}
	return "", m.ReadlinkErrors[name]
}

// NewMockFileSystem creates a new MockFileSystem
func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		OpenReturns:     make(map[string]fs.File),
		OpenErrors:      make(map[string]error),
		StatReturns:     make(map[string]fs.FileInfo),
		StatErrors:      make(map[string]error),
		ReadFileReturns: make(map[string][]byte),
		ReadFileErrors:  make(map[string]error),
		ReadDirReturns:  make(map[string][]fs.DirEntry),
		ReadDirErrors:   make(map[string]error),
		MkdirAllErrors:  make(map[string]error),
		WriteFileErrors: make(map[string]error),
		RemoveErrors:    make(map[string]error),
		RemoveAllErrors: make(map[string]error),
		ReadlinkReturns: make(map[string]string),
		ReadlinkErrors:  make(map[string]error),
	}
}

// MockFile implements fs.File for testing
type MockFile struct {
	name   string
	data   []byte
	pos    int64
	closed bool
}

func (m *MockFile) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{name: m.name, size: int64(len(m.data))}, nil
}

func (m *MockFile) Read(b []byte) (n int, err error) {
	if m.closed {
		return 0, os.ErrClosed
	}
	if m.pos >= int64(len(m.data)) {
		return 0, nil // EOF
	}
	n = copy(b, m.data[m.pos:])
	m.pos += int64(n)
	return n, nil
}

func (m *MockFile) Close() error {
	m.closed = true
	return nil
}

// mockFileInfo implements fs.FileInfo for testing
type mockFileInfo struct {
	name string
	size int64
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0644 }
func (m *mockFileInfo) ModTime() time.Time { return time.Now() }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() interface{}   { return nil }

// NewMockFileInfo creates a mock FileInfo
func NewMockFileInfo(name string, size int64, isDir bool) fs.FileInfo {
	if isDir {
		return &mockDirInfo{name: name}
	}
	return &mockFileInfo{name: name, size: size}
}

// mockDirInfo implements fs.FileInfo for directories
type mockDirInfo struct {
	name string
}

func (m *mockDirInfo) Name() string       { return m.name }
func (m *mockDirInfo) Size() int64        { return 0 }
func (m *mockDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0755 }
func (m *mockDirInfo) ModTime() time.Time { return time.Now() }
func (m *mockDirInfo) IsDir() bool        { return true }
func (m *mockDirInfo) Sys() interface{}   { return nil }

// NewMockDirEntry creates a mock DirEntry
func NewMockDirEntry(name string, isDir bool) fs.DirEntry {
	if isDir {
		return &mockDirEntry{name: name, info: &mockDirInfo{name: name}}
	}
	return &mockDirEntry{name: name, info: &mockFileInfo{name: name, size: 0}}
}

// mockDirEntry implements fs.DirEntry for testing
type mockDirEntry struct {
	name string
	info fs.FileInfo
}

func (m *mockDirEntry) Name() string               { return m.name }
func (m *mockDirEntry) IsDir() bool                { return m.info.IsDir() }
func (m *mockDirEntry) Type() fs.FileMode          { return m.info.Mode() }
func (m *mockDirEntry) Info() (fs.FileInfo, error) { return m.info, nil }
func (m *mockDirEntry) Sys() interface{}           { return nil }

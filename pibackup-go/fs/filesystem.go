// Package fs provides a minimal filesystem abstraction for testability.
// It extends io/fs.FS with write operations needed by the application.
package fs

import (
	"io/fs"
	"os"
)

// FileSystem is a minimal filesystem abstraction combining io/fs.FS with write operations.
type FileSystem interface {
	// Embed io/fs interfaces for read operations
	fs.FS
	fs.StatFS
	fs.ReadFileFS

	// Write operations (not in io/fs)
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	Remove(name string) error
	RemoveAll(path string) error

	// Additional read operation not in io/fs
	Readlink(name string) (string, error)

	// Explicitly declare ReadDir for clarity (it's in fs.FS but we want to be explicit)
	ReadDir(name string) ([]fs.DirEntry, error)
}

// RealFileSystem implements FileSystem using the OS filesystem
type RealFileSystem struct{}

// Open implements fs.FS
func (RealFileSystem) Open(name string) (fs.File, error) {
	return os.Open(name)
}

// Stat implements fs.StatFS
func (RealFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

// ReadFile implements fs.ReadFileFS
func (RealFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

// ReadDir implements fs.FS
func (RealFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	// os.DirEntry implements fs.DirEntry, so we can convert the slice directly
	// Using copy is more idiomatic than a manual loop
	result := make([]fs.DirEntry, len(entries))
	copy(result, entries)
	return result, nil
}

// MkdirAll implements FileSystem
func (RealFileSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// WriteFile implements FileSystem
func (RealFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

// Remove implements FileSystem
func (RealFileSystem) Remove(name string) error {
	return os.Remove(name)
}

// RemoveAll implements FileSystem
func (RealFileSystem) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// Readlink implements FileSystem
func (RealFileSystem) Readlink(name string) (string, error) {
	return os.Readlink(name)
}

// NewRealFileSystem creates a new RealFileSystem
func NewRealFileSystem() FileSystem {
	return RealFileSystem{}
}

package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MemFile mock representation of a file in memory.
type MemFile struct {
	content []byte
	isDir   bool
	mode    os.FileMode
	modTime time.Time
}

// MemFileSystem implements FileSystem in memory.
type MemFileSystem struct {
	files       map[string]*MemFile
	dirsCreated []string
}

// NewMemFileSystem creates a new MemFileSystem.
func NewMemFileSystem() *MemFileSystem {
	return &MemFileSystem{
		files: make(map[string]*MemFile),
	}
}

// AddFile adds a file to the mock filesystem.
func (m *MemFileSystem) AddFile(path string, content []byte, mode os.FileMode) {
	m.files[filepath.Clean(path)] = &MemFile{
		content: content,
		isDir:   false,
		mode:    mode,
		modTime: time.Now(),
	}
}

// AddDir adds a directory to the mock filesystem.
func (m *MemFileSystem) AddDir(path string) {
	m.files[filepath.Clean(path)] = &MemFile{
		content: nil,
		isDir:   true,
		mode:    os.ModeDir | 0o755,
		modTime: time.Now(),
	}
}

func (m *MemFileSystem) ReadFile(path string) ([]byte, error) {
	cleaned := filepath.Clean(path)
	f, ok := m.files[cleaned]
	if !ok {
		return nil, os.ErrNotExist
	}
	if f.isDir {
		return nil, fmt.Errorf("is a directory: %s", path)
	}
	return f.content, nil
}

func (m *MemFileSystem) WriteFile(path string, content []byte, perm os.FileMode) error {
	cleaned := filepath.Clean(path)
	m.files[cleaned] = &MemFile{
		content: content,
		isDir:   false,
		mode:    perm,
		modTime: time.Now(),
	}
	return nil
}

func (m *MemFileSystem) Stat(path string) (os.FileInfo, error) {
	cleaned := filepath.Clean(path)
	f, ok := m.files[cleaned]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &MemFileInfo{
		name:    filepath.Base(cleaned),
		size:    int64(len(f.content)),
		mode:    f.mode,
		modTime: f.modTime,
		isDir:   f.isDir,
	}, nil
}

func (m *MemFileSystem) MkdirAll(path string, perm os.FileMode) error {
	cleaned := filepath.Clean(path)
	m.dirsCreated = append(m.dirsCreated, cleaned)
	parts := strings.Split(cleaned, string(filepath.Separator))
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = filepath.Join(current, part)
		}
		if _, ok := m.files[current]; !ok {
			m.files[current] = &MemFile{
				isDir:   true,
				mode:    os.ModeDir | perm,
				modTime: time.Now(),
			}
		}
	}
	return nil
}

func (m *MemFileSystem) ReadDir(path string) ([]os.DirEntry, error) {
	cleaned := filepath.Clean(path)
	f, ok := m.files[cleaned]
	if !ok {
		return nil, os.ErrNotExist
	}
	if !f.isDir {
		return nil, fmt.Errorf("not a directory: %s", path)
	}

	var entries []os.DirEntry
	prefix := cleaned + string(filepath.Separator)
	if cleaned == "." {
		prefix = ""
	}

	for k, file := range m.files {
		if k == cleaned {
			continue
		}
		// Check if it's a direct child of cleaned
		if strings.HasPrefix(k, prefix) {
			rel, _ := filepath.Rel(cleaned, k)
			if !strings.Contains(rel, string(filepath.Separator)) {
				entries = append(entries, &MemDirEntry{
					name:  rel,
					isDir: file.isDir,
					mode:  file.mode,
					info: &MemFileInfo{
						name:    rel,
						size:    int64(len(file.content)),
						mode:    file.mode,
						modTime: file.modTime,
						isDir:   file.isDir,
					},
				})
			}
		}
	}
	return entries, nil
}

// MemFileInfo mock FileInfo.
type MemFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func (fi *MemFileInfo) Name() string       { return fi.name }
func (fi *MemFileInfo) Size() int64        { return fi.size }
func (fi *MemFileInfo) Mode() os.FileMode  { return fi.mode }
func (fi *MemFileInfo) ModTime() time.Time { return fi.modTime }
func (fi *MemFileInfo) IsDir() bool        { return fi.isDir }
func (fi *MemFileInfo) Sys() any           { return nil }

// MemDirEntry mock DirEntry.
type MemDirEntry struct {
	name  string
	isDir bool
	mode  os.FileMode
	info  os.FileInfo
}

func (d *MemDirEntry) Name() string               { return d.name }
func (d *MemDirEntry) IsDir() bool                { return d.isDir }
func (d *MemDirEntry) Type() fs.FileMode          { return d.mode.Type() }
func (d *MemDirEntry) Info() (os.FileInfo, error) { return d.info, nil }

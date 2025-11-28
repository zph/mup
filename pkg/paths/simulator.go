package paths

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// REQ-PM-023: PathSimulator provides test simulation harness for validating
// path logic without filesystem I/O operations
type PathSimulator struct {
	mu          sync.RWMutex
	files       map[string]bool       // path -> isDir
	symlinks    map[string]string     // symlink path -> target path
	operations  []string              // operation log for validation
}

// NewPathSimulator creates a new in-memory filesystem simulator
func NewPathSimulator() *PathSimulator {
	return &PathSimulator{
		files:      make(map[string]bool),
		symlinks:   make(map[string]string),
		operations: make([]string, 0),
	}
}

// MkdirAll simulates creating a directory hierarchy
func (s *PathSimulator) MkdirAll(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Normalize path
	path = filepath.Clean(path)

	// Mark the full path as a directory
	s.files[path] = true

	// Create all parent directories
	for dir := filepath.Dir(path); dir != "." && dir != "/" && dir != path; dir = filepath.Dir(dir) {
		s.files[dir] = true
		// Stop if we've already created this parent
		if _, exists := s.files[dir]; exists && dir == filepath.Dir(dir) {
			break
		}
	}

	s.operations = append(s.operations, fmt.Sprintf("mkdir -p %s", path))
	return nil
}

// CreateFile simulates creating a file
func (s *PathSimulator) CreateFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = filepath.Clean(path)

	// Create parent directory if needed
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		parts := strings.Split(dir, string(filepath.Separator))
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
			s.files[current] = true
		}
	}

	s.files[path] = false // false = file
	s.operations = append(s.operations, fmt.Sprintf("touch %s", path))
	return nil
}

// Symlink simulates creating a symlink
func (s *PathSimulator) Symlink(target, linkPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	linkPath = filepath.Clean(linkPath)
	s.symlinks[linkPath] = target
	s.operations = append(s.operations, fmt.Sprintf("ln -s %s %s", target, linkPath))
	return nil
}

// Remove simulates removing a file or symlink
func (s *PathSimulator) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = filepath.Clean(path)
	delete(s.files, path)
	delete(s.symlinks, path)
	s.operations = append(s.operations, fmt.Sprintf("rm %s", path))
	return nil
}

// Exists checks if a path exists (file, directory, or symlink)
func (s *PathSimulator) Exists(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = filepath.Clean(path)
	_, fileExists := s.files[path]
	_, symlinkExists := s.symlinks[path]
	return fileExists || symlinkExists
}

// IsDir checks if a path is a directory
func (s *PathSimulator) IsDir(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = filepath.Clean(path)
	isDir, exists := s.files[path]
	return exists && isDir
}

// IsSymlink checks if a path is a symlink
func (s *PathSimulator) IsSymlink(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = filepath.Clean(path)
	_, exists := s.symlinks[path]
	return exists
}

// ReadSymlink returns the target of a symlink
func (s *PathSimulator) ReadSymlink(path string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = filepath.Clean(path)
	target, exists := s.symlinks[path]
	if !exists {
		return "", fmt.Errorf("not a symlink: %s", path)
	}
	return target, nil
}

// GetOperations returns the log of operations for validation
func (s *PathSimulator) GetOperations() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ops := make([]string, len(s.operations))
	copy(ops, s.operations)
	return ops
}

// Reset clears all state for test isolation
func (s *PathSimulator) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.files = make(map[string]bool)
	s.symlinks = make(map[string]string)
	s.operations = make([]string, 0)
}

// ListFiles returns all files and directories (for debugging)
func (s *PathSimulator) ListFiles() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	return paths
}

// ListSymlinks returns all symlinks (for debugging)
func (s *PathSimulator) ListSymlinks() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	links := make(map[string]string, len(s.symlinks))
	for link, target := range s.symlinks {
		links[link] = target
	}
	return links
}

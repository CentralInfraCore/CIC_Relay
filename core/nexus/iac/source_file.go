package iac

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileSource implements IaCSource for the local file system.
type FileSource struct {
	RootPath string
}

// NewFileSource creates a new FileSource rooted at the given path.
func NewFileSource(rootPath string) *FileSource {
	return &FileSource{
		RootPath: rootPath,
	}
}

// FetchConfig walks the directory tree and reads all YAML/JSON files.
func (s *FileSource) FetchConfig(ctx context.Context) (map[string][]byte, error) {
	config := make(map[string][]byte)

	err := filepath.WalkDir(s.RootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip .git directory
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process YAML and JSON files
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Store with relative path as key
		relPath, err := filepath.Rel(s.RootPath, path)
		if err != nil {
			return err
		}
		config[relPath] = content

		return nil
	})

	if err != nil {
		return nil, err
	}

	return config, nil
}

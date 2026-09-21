// Package shared provides common utilities used by both the backend web server
// and the report generator CLI.
package shared

import (
	"os"
	"path/filepath"
	"strings"
)

// GetLines reads the contents of a file, filters out empty lines and lines
// starting with "#" (comments), and returns a slice containing the remaining lines.
func GetLines(filename string) ([]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	allLines := strings.Split(string(data), "\n")
	var lines []string

	for _, line := range allLines {
		if !strings.HasPrefix(line, "#") && len(line) > 0 {
			lines = append(lines, line)
		}
	}

	return lines, nil
}

// EnsureFile creates the parent directories and the file itself if they don't
// already exist. If the file already exists it is left untouched.
func EnsureFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

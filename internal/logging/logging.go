// Package logging sends the standard logger to both stderr and a size-capped
// log file, and can read the tail of that file for the GUI.
package logging

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxBytes = 2 << 20 // rotate at 2 MB
	keepOld  = 1
)

var (
	mu   sync.Mutex
	path string
	file *os.File
)

// Setup routes log output to <dataDir>/output/breaklist.log as well as stderr.
func Setup(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	path = filepath.Join(dataDir, "output", "breaklist.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	file = f
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetOutput(io.MultiWriter(os.Stderr, rotatingWriter{}))
	return nil
}

// Path returns the log file path ("" before Setup).
func Path() string { return path }

type rotatingWriter struct{}

func (rotatingWriter) Write(p []byte) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return len(p), nil
	}
	if info, err := file.Stat(); err == nil && info.Size()+int64(len(p)) > maxBytes {
		_ = file.Close()
		old := path + ".1"
		_ = os.Remove(old)
		_ = os.Rename(path, old)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			file = nil
			return len(p), nil
		}
		file = f
	}
	return file.Write(p)
}

// Tail returns the last n lines of the log file.
func Tail(n int) []string {
	mu.Lock()
	p := path
	mu.Unlock()
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	// Include the rotated file if the current one is short.
	if bytes.Count(data, []byte("\n")) < n {
		if old, err := os.ReadFile(p + ".1"); err == nil {
			data = append(old, data...)
		}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

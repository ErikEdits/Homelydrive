// Package logbuf keeps the most recent log lines in memory (for the "Protokoll"
// tab of the server window) and writes them to a size-rotated log file.
package logbuf

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Line is one log message.
type Line struct {
	Seq   int64     `json:"seq"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// Buffer is an io.Writer that keeps the last N lines and mirrors them to a file.
type Buffer struct {
	mu      sync.Mutex
	lines   []Line
	max     int
	seq     int64
	file    *os.File
	path    string
	written int64
	extra   io.Writer
}

const maxFileSize = 10 << 20

// New creates a buffer keeping max lines. If dir is non-empty, logs are also
// written to dir/server.log (rotated at 10 MB, 3 old files kept). extra (e.g.
// os.Stderr) receives a copy of every line.
func New(max int, dir string, extra io.Writer) *Buffer {
	b := &Buffer{max: max, extra: extra}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o750); err == nil {
			b.path = filepath.Join(dir, "server.log")
			b.openFile()
		}
	}
	return b
}

func (b *Buffer) openFile() {
	f, err := os.OpenFile(b.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	b.file = f
	if fi, err := f.Stat(); err == nil {
		b.written = fi.Size()
	}
}

func (b *Buffer) rotate() {
	b.file.Close()
	for i := 2; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", b.path, i), fmt.Sprintf("%s.%d", b.path, i+1))
	}
	os.Rename(b.path, b.path+".1")
	b.written = 0
	b.openFile()
}

// Write implements io.Writer for slog text handlers. Each call is one record.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.extra != nil {
		b.extra.Write(p)
	}
	if b.file != nil {
		if b.written+int64(len(p)) > maxFileSize {
			b.rotate()
		}
		if b.file != nil {
			n, _ := b.file.Write(p)
			b.written += int64(n)
		}
	}
	text := string(p)
	for len(text) > 0 && (text[len(text)-1] == '\n' || text[len(text)-1] == '\r') {
		text = text[:len(text)-1]
	}
	b.seq++
	b.lines = append(b.lines, Line{Seq: b.seq, Time: time.Now(), Level: levelOf(text), Text: text})
	if len(b.lines) > b.max {
		b.lines = append([]Line(nil), b.lines[len(b.lines)-b.max:]...)
	}
	return len(p), nil
}

func levelOf(s string) string {
	for _, l := range []string{"ERROR", "WARN", "DEBUG"} {
		if contains(s, "level="+l) {
			return l
		}
	}
	return "INFO"
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Since returns all buffered lines with a sequence number greater than seq.
func (b *Buffer) Since(seq int64) []Line {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Line{}
	for _, l := range b.lines {
		if l.Seq > seq {
			out = append(out, l)
		}
	}
	return out
}

// Path returns the log file location ("" if file logging is off).
func (b *Buffer) Path() string { return b.path }

// Package fsname validates and sanitizes file names and relative paths so that
// they are safe on both Windows and Linux servers.
package fsname

import (
	"errors"
	"path"
	"runtime"
	"strings"
	"unicode/utf8"
)

var ErrInvalidPath = errors.New("invalid path")

// InternalPrefix marks folders the server keeps for itself inside mounted
// folders (trash, temporary uploads). They are hidden from all listings.
const InternalPrefix = ".homelydrive-"

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// Clean normalizes a client supplied relative path ("Fotos/2026/a.jpg").
// The result never starts or ends with "/" and the root is "". Paths that
// try to escape the root or touch internal folders are rejected.
func Clean(p string) (string, error) {
	if !utf8.ValidString(p) || strings.ContainsRune(p, 0) {
		return "", ErrInvalidPath
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", ErrInvalidPath
		}
		if strings.HasPrefix(part, InternalPrefix) {
			return "", ErrInvalidPath
		}
	}
	p = path.Clean(p)
	if p == "." {
		return "", nil
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", ErrInvalidPath
	}
	return p, nil
}

// ValidName reports whether name can be used as a single file name.
func ValidName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	if strings.HasPrefix(name, InternalPrefix) || len(name) > 255 {
		return false
	}
	if runtime.GOOS == "windows" {
		return Sanitize(name) == name
	}
	return utf8.ValidString(name)
}

// Sanitize turns name into something every supported OS accepts, replacing
// forbidden characters with "_". Used for uploads so that a file called
// "Screenshot 12:00.png" from a phone still lands on a Windows server.
func Sanitize(name string) string {
	name = strings.ToValidUTF8(name, "_")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32, r == '/', r == '\\':
			b.WriteRune('_')
		case runtime.GOOS == "windows" && strings.ContainsRune(`<>:"|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if runtime.GOOS == "windows" {
		out = strings.TrimRight(out, " .")
		base := out
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsReserved[strings.ToUpper(base)] {
			out = "_" + out
		}
	}
	if strings.HasPrefix(out, InternalPrefix) {
		out = "_" + out
	}
	if out == "" || out == "." || out == ".." {
		out = "_"
	}
	for len(out) > 255 {
		_, size := utf8.DecodeLastRuneInString(out)
		out = out[:len(out)-size]
	}
	return out
}

// SanitizePath sanitizes every element of a relative path.
func SanitizePath(p string) (string, error) {
	p, err := Clean(p)
	if err != nil || p == "" {
		return p, err
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = Sanitize(part)
	}
	return strings.Join(parts, "/"), nil
}

// Join joins a parent path and a name using "/".
func Join(parent, name string) string {
	if parent == "" {
		return name
	}
	if name == "" {
		return parent
	}
	return parent + "/" + name
}

// Split returns parent and name of a relative path.
func Split(p string) (parent, name string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

// Ext returns the lower-case extension without the dot ("jpg").
func Ext(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[i+1:])
}

// IsWithin reports whether p equals base or lies below it.
func IsWithin(p, base string) bool {
	if base == "" {
		return true
	}
	return p == base || strings.HasPrefix(p, base+"/")
}

// Rebase replaces the prefix oldBase of p with newBase.
func Rebase(p, oldBase, newBase string) string {
	if p == oldBase {
		return newBase
	}
	return Join(newBase, strings.TrimPrefix(p, oldBase+"/"))
}

// Numbered returns "name (n).ext" for conflict resolution.
func Numbered(name string, n int) string {
	ext := ""
	base := name
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		base, ext = name[:i], name[i:]
	}
	return base + " (" + itoa(n) + ")" + ext
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

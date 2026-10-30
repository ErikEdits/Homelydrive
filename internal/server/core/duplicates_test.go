package core

import "testing"

func TestCopyName(t *testing.T) {
	for name, want := range map[string]bool{
		"IMG_1 (1).jpg":        true,
		"IMG_1(2).jpg":         true,
		"Bild - Kopie.jpg":     true,
		"Bild - Kopie (2).jpg": true,
		"Bild_copy.jpg":        true,
		"Kopie von Bild.jpg":   true,
		"Copy of Bild.jpg":     true,
		"IMG_20240101.jpg":     false,
		"Bericht (2024).pdf":   false,
		"copyright.txt":        false,
		"Kopierer.pdf":         false,
	} {
		if got := copyName.MatchString(name); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
}

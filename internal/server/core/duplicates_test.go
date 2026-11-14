package core

import "testing"

func TestOriginalName(t *testing.T) {
	for name, want := range map[string]string{
		"087 - Las albinas (1).mp4": "087 - Las albinas.mp4",
		"Bild - Kopie (2).jpg":      "Bild.jpg",
		"Kopie von Bild.jpg":        "Bild.jpg",
		"Bericht (2024).pdf":        "Bericht (2024).pdf",
		"README":                    "README",
		"Notiz (1)":                 "Notiz",
	} {
		if got := originalName(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

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
		if got := isCopyName(name); got != want {
			t.Errorf("%q: %v, want %v", name, got, want)
		}
	}
}

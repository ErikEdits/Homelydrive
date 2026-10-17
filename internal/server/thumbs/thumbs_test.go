package thumbs

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndEdit(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for x := 0; x < 200; x++ {
		for y := 0; y < 200; y++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	src := filepath.Join(dir, "a.png")
	f, _ := os.Create(src)
	png.Encode(f, img)
	f.Close()
	dst := filepath.Join(dir, "t", "a.jpg")
	if err := Generate(context.Background(), src, "image", dst, 100); err != nil {
		t.Fatal(err)
	}
	tf, _ := os.Open(dst)
	cfg, _ := jpeg.DecodeConfig(tf)
	tf.Close()
	if cfg.Width != 100 || cfg.Height != 50 {
		t.Fatalf("thumb size %dx%d", cfg.Width, cfg.Height)
	}
	out := filepath.Join(dir, "edited.png")
	ext, err := Edit(src, out, 90, &Crop{X: 0, Y: 0, W: 0.5, H: 1})
	if err != nil || ext != "png" {
		t.Fatal(ext, err)
	}
	of, _ := os.Open(out)
	res, _ := png.Decode(of)
	of.Close()
	if b := res.Bounds(); b.Dx() != 200 || b.Dy() != 200 {
		t.Fatalf("edited size %v", b)
	}
	r, _, _, _ := res.At(10, 10).RGBA()
	if r < 0xf000 {
		t.Fatal("crop kept the wrong half")
	}
}

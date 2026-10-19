// Package thumbs creates preview images (thumbnails) for photos and videos,
// reads the photo date from EXIF data and performs simple photo edits
// (rotate, crop).
package thumbs

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	xdraw "golang.org/x/image/draw"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

var ErrUnsupported = errors.New("no preview available for this file")

// maxPixels guards against decompression bombs (about 100 megapixels).
const maxPixels = 100_000_000

var ffmpegPath string

func init() {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		ffmpegPath = p
	}
}

// HaveFFmpeg reports whether video thumbnails are available.
func HaveFFmpeg() bool { return ffmpegPath != "" }

// Generate writes a JPEG thumbnail of src (fitting into size×size) to dst.
func Generate(ctx context.Context, src, category, dst string, size int) error {
	var img image.Image
	var err error
	switch category {
	case "image":
		img, err = decodeOriented(src, size)
		if err != nil && ffmpegPath != "" {
			img, err = ffmpegFrame(ctx, src, size, false)
		}
	case "video":
		if ffmpegPath == "" {
			return ErrUnsupported
		}
		img, err = ffmpegFrame(ctx, src, size, true)
	default:
		return ErrUnsupported
	}
	if err != nil {
		return err
	}
	img = fit(img, size)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, flatten(img), &jpeg.Options{Quality: 82}); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// Avatar writes a square JPEG profile picture of size×size pixels, cut from
// the middle of the image at src, to w.
func Avatar(ctx context.Context, src string, w io.Writer, size int) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	cfg, _, cfgErr := image.DecodeConfig(f)
	f.Close()
	var img image.Image
	if cfgErr == nil && cfg.Width > 0 && cfg.Height > 0 {
		// Scale down while decoding, but keep the shorter side >= size.
		fitTo := 0
		if short := min(cfg.Width, cfg.Height); short > size {
			fitTo = max(cfg.Width, cfg.Height)*size/short + 1
		}
		img, err = decodeOriented(src, fitTo)
	}
	if img == nil && ffmpegPath != "" {
		img, err = ffmpegFrame(ctx, src, 4*size, false) // HEIC and other formats
	}
	if img == nil || err != nil {
		return ErrUnsupported
	}
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	square := image.Rect(0, 0, side, side).Add(b.Min).Add(image.Pt((b.Dx()-side)/2, (b.Dy()-side)/2))
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, square, xdraw.Over, nil)
	return jpeg.Encode(w, flatten(out), &jpeg.Options{Quality: 88})
}

// decodeOriented decodes an image and applies its EXIF orientation. If fitTo
// is > 0 the image is scaled down first, which makes rotation much cheaper.
func decodeOriented(path string, fitTo int) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, ErrUnsupported
	}
	f.Seek(0, io.SeekStart)
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	if fitTo > 0 {
		img = fit(img, fitTo)
	}
	f.Seek(0, io.SeekStart)
	return applyOrientation(img, orientation(f)), nil
}

func orientation(r io.Reader) int {
	x, err := exif.Decode(r)
	if err != nil {
		return 1
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}
	v, err := tag.Int(0)
	if err != nil {
		return 1
	}
	return v
}

// TakenAt returns the EXIF capture time of a photo (zero if unknown).
func TakenAt(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return time.Time{}
	}
	t, err := x.DateTime()
	if err != nil || t.Year() < 1971 {
		return time.Time{}
	}
	return t
}

func ffmpegFrame(ctx context.Context, src string, size int, seek bool) (image.Image, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := []string{"-v", "error"}
	if seek {
		args = append(args, "-ss", "1")
	}
	args = append(args, "-i", src, "-frames:v", "1",
		"-vf", "scale='min("+strconv.Itoa(size*2)+",iw)':-2", "-f", "image2", "-c:v", "png", "pipe:1")
	out, err := exec.CommandContext(ctx, ffmpegPath, args...).Output()
	if (err != nil || len(out) == 0) && seek {
		// Very short video: take the first frame instead.
		return ffmpegFrame(ctx, src, size, false)
	}
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(out))
}

func fit(img image.Image, size int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= size && h <= size {
		return img
	}
	if w >= h {
		h = h * size / w
		w = size
	} else {
		w = w * size / h
		h = size
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// flatten draws transparent images on white so JPEG encoding looks right.
func flatten(img image.Image) image.Image {
	if _, ok := img.(*image.YCbCr); ok {
		return img
	}
	dst := image.NewRGBA(img.Bounds())
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	return dst
}

// applyOrientation rotates/flips img according to the EXIF orientation value.
func applyOrientation(img image.Image, o int) image.Image {
	switch o {
	case 2:
		return flipH(img)
	case 3:
		return rotate(img, 180)
	case 4:
		return flipH(rotate(img, 180))
	case 5:
		return flipH(rotate(img, 90))
	case 6:
		return rotate(img, 90)
	case 7:
		return flipH(rotate(img, 270))
	case 8:
		return rotate(img, 270)
	}
	return img
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// rotate turns img clockwise by deg (90, 180 or 270).
func rotate(img image.Image, deg int) image.Image {
	src := toRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	var dst *image.RGBA
	switch deg {
	case 90, 270:
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	case 180:
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	default:
		return img
	}
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
		for x := 0; x < w; x++ {
			var dx, dy int
			switch deg {
			case 90:
				dx, dy = h-1-y, x
			case 180:
				dx, dy = w-1-x, h-1-y
			case 270:
				dx, dy = y, w-1-x
			}
			o := dy*dst.Stride + dx*4
			copy(dst.Pix[o:o+4], row[x*4:x*4+4])
		}
	}
	return dst
}

func flipH(img image.Image) image.Image {
	src := toRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s := y*src.Stride + x*4
			d := y*dst.Stride + (w-1-x)*4
			copy(dst.Pix[d:d+4], src.Pix[s:s+4])
		}
	}
	return dst
}

// Crop is a crop rectangle in fractions (0..1) of the oriented image.
type Crop struct {
	X, Y, W, H float64
}

// Edit applies a crop and a clockwise rotation to the photo at src and writes
// the result to dst in the same format (JPEG or PNG; others become PNG).
// It returns the extension of the written format.
func Edit(src, dst string, rotateDeg int, crop *Crop) (string, error) {
	img, err := decodeOriented(src, 0)
	if err != nil {
		return "", err
	}
	if crop != nil && crop.W > 0 && crop.H > 0 {
		b := img.Bounds()
		r := image.Rect(
			b.Min.X+int(crop.X*float64(b.Dx())),
			b.Min.Y+int(crop.Y*float64(b.Dy())),
			b.Min.X+int((crop.X+crop.W)*float64(b.Dx())),
			b.Min.Y+int((crop.Y+crop.H)*float64(b.Dy())),
		).Intersect(b)
		if r.Empty() {
			return "", errors.New("empty crop")
		}
		c := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(c, c.Bounds(), img, r.Min, draw.Src)
		img = c
	}
	rotateDeg = ((rotateDeg % 360) + 360) % 360
	if rotateDeg != 0 {
		img = rotate(img, rotateDeg)
	}
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	_, format, _ := image.DecodeConfig(f)
	f.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer out.Close()
	switch format {
	case "jpeg":
		return "jpg", jpeg.Encode(out, flatten(img), &jpeg.Options{Quality: 92})
	case "gif":
		return "gif", gif.Encode(out, img, nil)
	default:
		return "png", png.Encode(out, img)
	}
}

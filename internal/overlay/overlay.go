// Package overlay draws overlays (text, clock, image, lower third) as transparent pictures at
// the output's scale, in Go with the Go fonts, so that they look the same everywhere and need no
// fonts installed. The engine puts them over the program.
package overlay

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // image overlays: the decoders
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"strings"
	"time"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/biggaeroza/bga-mix/internal/project"
)

// Defaults.
const (
	DefaultSize   = 0.05 // text height, fraction of the output's height
	DefaultLowerW = 0.45 // lower third bar, fraction of the output's width
	DefaultImageW = 0.15
)

var (
	regular, bold *opentype.Font
	defaults      = struct{ text, lowerBG, accent color.NRGBA }{
		color.NRGBA{255, 255, 255, 255}, color.NRGBA{0, 0, 0, 0xc0}, color.NRGBA{0xe5, 0x39, 0x35, 255}}
)

func init() {
	regular, _ = opentype.Parse(goregular.TTF)
	bold, _ = opentype.Parse(gobold.TTF)
}

func face(f *opentype.Font, px float64) font.Face {
	fc, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: math.Max(4, px), DPI: 72, Hinting: font.HintingFull})
	return fc
}

func colour(s string, def color.NRGBA) color.NRGBA {
	if c, ok := project.ParseColour(s); ok {
		return c
	}
	return def
}

// Picture is a drawn overlay and where it goes on the output (pixels).
type Picture struct {
	Img  *image.NRGBA
	X, Y int
}

// ClockText is a clock overlay's text at t.
func ClockText(ov project.Overlay, t time.Time) string {
	f, ok := project.ClockFormats[ov.Format]
	if !ok {
		f = project.ClockFormats["time"]
	}
	return t.Format(f)
}

// Draw draws an overlay for an output (w×h); dir resolves an image's path, now is the clock's
// time.
func Draw(ov project.Overlay, w, h int, dir string, now time.Time) (Picture, error) {
	size := ov.Size
	if size <= 0 {
		size = DefaultSize
	}
	px := size * float64(h)
	p := Picture{X: int(math.Round(ov.X * float64(w))), Y: int(math.Round(ov.Y * float64(h)))}
	var err error
	switch ov.Type {
	case project.OverlayText:
		p.Img = textBox(ov.Text, px, colour(ov.Colour, defaults.text), colour(ov.Background, color.NRGBA{}))
	case project.OverlayClock:
		p.Img = textBox(ClockText(ov, now), px, colour(ov.Colour, defaults.text), colour(ov.Background, color.NRGBA{}))
	case project.OverlayLowerThird:
		bw := ov.W
		if bw <= 0 {
			bw = DefaultLowerW
		}
		p.Img = lowerThird(ov.Text, ov.Subtitle, int(math.Round(bw*float64(w))), px,
			colour(ov.Colour, defaults.text), colour(ov.Background, defaults.lowerBG), colour(ov.Accent, defaults.accent))
	case project.OverlayImage:
		iw := ov.W
		if iw <= 0 {
			iw = DefaultImageW
		}
		p.Img, err = picture(project.Resolve(dir, ov.Path), int(math.Round(iw*float64(w))))
	default:
		err = fmt.Errorf("unknown overlay type %q", ov.Type)
	}
	if err != nil {
		return p, err
	}
	if ov.Opacity != nil && *ov.Opacity < 1 {
		fade(p.Img, *ov.Opacity)
	}
	// keep it on the picture (the compositing element draws nothing that starts outside)
	p.X = max(0, min(p.X, w-1))
	p.Y = max(0, min(p.Y, h-1))
	return p, nil
}

// textBox is a line of text, on a box with padding when bg is not transparent.
func textBox(text string, px float64, fg, bg color.NRGBA) *image.NRGBA {
	fc := face(regular, px)
	defer fc.Close()
	m := fc.Metrics()
	pad := int(math.Round(px * 0.3))
	tw := font.MeasureString(fc, text).Ceil()
	th := (m.Ascent + m.Descent).Ceil()
	img := image.NewNRGBA(image.Rect(0, 0, max(1, tw+2*pad), max(1, th+2*pad)))
	if bg.A > 0 {
		draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	}
	shadow := bg.A < 0x80 // text without a solid box gets a soft shadow to stay readable
	drawText(img, fc, text, pad, pad+m.Ascent.Ceil(), fg, shadow)
	return img
}

// lowerThird is a bar w pixels wide: an accent strip, the name in bold and the title below it.
func lowerThird(name, title string, w int, px float64, fg, bg, accent color.NRGBA) *image.NRGBA {
	nf, tf := face(bold, px), face(regular, px*0.7)
	defer nf.Close()
	defer tf.Close()
	pad := int(math.Round(px * 0.45))
	strip := max(2, int(math.Round(px*0.18)))
	nm, tm := nf.Metrics(), tf.Metrics()
	h := pad
	if name != "" {
		h += (nm.Ascent + nm.Descent).Ceil()
	}
	if title != "" {
		h += (tm.Ascent + tm.Descent).Ceil()
	}
	h += pad
	img := image.NewNRGBA(image.Rect(0, 0, max(strip+1, w), h))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, strip, h), &image.Uniform{accent}, image.Point{}, draw.Src)
	x, y := strip+pad, pad
	if name != "" {
		drawText(img, nf, name, x, y+nm.Ascent.Ceil(), fg, false)
		y += (nm.Ascent + nm.Descent).Ceil()
	}
	if title != "" {
		c := fg
		c.A = uint8(float64(c.A) * 0.85)
		drawText(img, tf, title, x, y+tm.Ascent.Ceil(), c, false)
	}
	return img
}

func drawText(img *image.NRGBA, fc font.Face, text string, x, baseline int, c color.NRGBA, shadow bool) {
	d := &font.Drawer{Dst: img, Face: fc}
	if shadow {
		off := max(1, fc.Metrics().Height.Ceil()/18)
		d.Src = &image.Uniform{color.NRGBA{0, 0, 0, uint8(int(c.A) * 3 / 4)}}
		d.Dot = fixed.P(x+off, baseline+off)
		d.DrawString(text)
	}
	d.Src = &image.Uniform{c}
	d.Dot = fixed.P(x, baseline)
	d.DrawString(text)
}

// picture reads an image file and scales it to w pixels wide, keeping its shape.
func picture(path string, w int) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: not an image BGAMix reads (PNG, JPEG, GIF, BMP, TIFF, WebP): %v", path, err)
	}
	b := src.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return nil, errors.New("an empty image")
	}
	w = max(1, w)
	h := max(1, int(math.Round(float64(w)*float64(b.Dy())/float64(b.Dx()))))
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst, nil
}

func fade(img *image.NRGBA, a float64) {
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = uint8(float64(img.Pix[i]) * a)
	}
}

// WritePNG writes a picture as PNG (fast compression: they are rewritten often).
func WritePNG(w io.Writer, img image.Image) error {
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	return enc.Encode(w, img)
}

// Visible reports whether an overlay is shown with the operator's switch on and a layout on air.
func Visible(ov project.Overlay, on bool, layout string) bool {
	if !on {
		return false
	}
	if len(ov.Layouts) == 0 {
		return true
	}
	for _, l := range ov.Layouts {
		if l == layout {
			return true
		}
	}
	return false
}

// Fade is an overlay's fade time.
func Fade(ov project.Overlay) float64 {
	if ov.FadeS <= 0 {
		return project.DefaultFadeS
	}
	return ov.FadeS
}

// Strip trims a text for one line.
func Strip(s string) string { return strings.Join(strings.Fields(s), " ") }

package overlay

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/biggaeroza/bga-mix/internal/project"
)

func TestText(t *testing.T) {
	p, err := Draw(project.Overlay{Type: "text", Text: "Hello", X: 0.1, Y: 0.5, Background: "#00ff00"}, 1280, 720, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b := p.Img.Bounds()
	// 0.05 × 720 = 36 px text: the box is a little taller, and "Hello" wider than tall
	if b.Dy() < 40 || b.Dy() > 70 || b.Dx() < b.Dy() || p.X != 128 || p.Y != 360 {
		t.Errorf("%v at %d,%d", b, p.X, p.Y)
	}
	if c := p.Img.NRGBAAt(1, 1); c != (color.NRGBA{0, 255, 0, 255}) {
		t.Errorf("box %v", c)
	}
	white := 0
	for i := 0; i < len(p.Img.Pix); i += 4 {
		if p.Img.Pix[i] > 200 && p.Img.Pix[i+1] > 200 && p.Img.Pix[i+2] > 200 {
			white++
		}
	}
	if white < 100 {
		t.Errorf("%d white text pixels", white)
	}
	big, _ := Draw(project.Overlay{Type: "text", Text: "Hello", Size: 0.1}, 1280, 720, "", time.Now())
	if big.Img.Bounds().Dy() < 2*b.Dy()-10 {
		t.Errorf("size 0.1 not twice as tall: %v vs %v", big.Img.Bounds(), b)
	}
	clear, _ := Draw(project.Overlay{Type: "text", Text: "x"}, 1280, 720, "", time.Now())
	if clear.Img.NRGBAAt(0, 0).A != 0 {
		t.Error("no background asked for, but there is one")
	}
}

func TestLowerThird(t *testing.T) {
	p, _ := Draw(project.Overlay{Type: "lower_third", Text: "Ada Lovelace", Subtitle: "Analyst", X: 0.05, Y: 0.75, Accent: "#0000ff"}, 1920, 1080, "", time.Now())
	if p.Img.Bounds().Dx() != 864 { // 0.45 × 1920
		t.Errorf("width %d", p.Img.Bounds().Dx())
	}
	if c := p.Img.NRGBAAt(2, 10); c != (color.NRGBA{0, 0, 255, 255}) {
		t.Errorf("accent %v", c)
	}
	if c := p.Img.NRGBAAt(p.Img.Bounds().Dx()-2, 2); c.A != 0xc0 {
		t.Errorf("bar %v", c)
	}
	one, _ := Draw(project.Overlay{Type: "lower_third", Text: "Ada Lovelace"}, 1920, 1080, "", time.Now())
	if one.Img.Bounds().Dy() >= p.Img.Bounds().Dy() {
		t.Error("one line is not shorter than two")
	}
}

func TestClockAndImage(t *testing.T) {
	at := time.Date(2026, 11, 19, 9, 5, 7, 0, time.UTC)
	if s := ClockText(project.Overlay{Type: "clock"}, at); s != "09:05:07" {
		t.Error(s)
	}
	if s := ClockText(project.Overlay{Type: "clock", Format: "date"}, at); s != "19 November 2026" {
		t.Error(s)
	}
	dir := t.TempDir()
	src := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+3] = 255, 128 // half-transparent red
	}
	f, _ := os.Create(filepath.Join(dir, "logo.png"))
	png.Encode(f, src)
	f.Close()
	o := 0.5
	p, err := Draw(project.Overlay{Type: "image", Path: "logo.png", W: 0.25, Opacity: &o}, 1280, 720, dir, at)
	if err != nil {
		t.Fatal(err)
	}
	if b := p.Img.Bounds(); b.Dx() != 320 || b.Dy() != 160 {
		t.Errorf("scaled to %v", b)
	}
	if c := p.Img.NRGBAAt(100, 50); c.R != 255 || c.A < 60 || c.A > 68 { // 128 × 0.5
		t.Errorf("pixel %v", c)
	}
	if _, err := Draw(project.Overlay{Type: "image", Path: "nope.png"}, 1280, 720, dir, at); err == nil {
		t.Error("a missing image")
	}
}

func TestVisible(t *testing.T) {
	ov := project.Overlay{Layouts: []string{"a"}}
	if !Visible(ov, true, "a") || Visible(ov, true, "b") || Visible(ov, false, "a") || !Visible(project.Overlay{}, true, "z") {
		t.Error("Visible")
	}
}

package engine

import (
	"image/color"
	"path/filepath"
	"testing"
	"time"

	"github.com/biggaeroza/bga-mix/internal/project"
)

// Overlays over the program: a green text box always; a blue image only on layout "b"; a lower
// third hidden, then shown live for 1 s with new words. Checked in the recording.
func TestOverlaysInRecording(t *testing.T) {
	dir := t.TempDir()
	makePNG(t, filepath.Join(dir, "blue.png"), color.RGBA{0, 0, 255, 255})
	p := &project.Project{FormatVersion: 1, Name: "ov", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{{ID: "bg", Type: "test", Pattern: "red"}},
		Layouts: []project.Layout{{ID: "a", Boxes: []project.Box{{Feed: "bg", W: 1, H: 1}}},
			{ID: "b", Boxes: []project.Box{{Feed: "bg", W: 1, H: 1}}}},
		Overlays: []project.Overlay{
			{ID: "box", Type: "text", Text: "  ", X: 0.05, Y: 0.05, Size: 0.15, Background: "#00ff00", FadeS: 0.1},
			{ID: "logo", Type: "image", Path: "blue.png", X: 0.7, Y: 0.05, W: 0.25, Layouts: []string{"b"}, FadeS: 0.1},
			{ID: "l3", Type: "lower_third", Text: "Someone", X: 0, Y: 0.7, W: 1, Background: "#ffff00", Hidden: true, FadeS: 0.1},
		},
		Recordings: []project.Recording{{Path: "out.mkv", Container: "mkv"}},
	}
	name, title := "Ada Lovelace", "Analyst"
	s, evs := live(t, p, dir, Options{}, 4*time.Second,
		step{1 * time.Second, func(s *Show) { must(t, s.SetLayout("b", nil)) }},
		step{2 * time.Second, func(s *Show) { must(t, s.OverlayCommand("l3", nil, &name, &title, 1)) }},
		step{2500 * time.Millisecond, func(s *Show) {
			o := s.Status().Overlays
			if len(o) != 3 || !o[2].Visible || o[2].Text != name || o[1].Visible != true {
				t.Errorf("status %+v", o)
			}
		}},
	)
	if err := s.OverlayCommand("nope", nil, nil, nil, 0); err == nil {
		t.Error("an unknown overlay")
	}
	frames := decode(t, s.Recordings()[0])
	expectColours(t, frames, 0.1, 0.1, map[float64]string{0.5: "green", 3.5: "green"})             // always
	expectColours(t, frames, 0.82, 0.12, map[float64]string{0.5: "red", 1.6: "blue", 3.5: "blue"}) // on layout b
	expectColours(t, frames, 0.9, 0.76, map[float64]string{1.6: "red", 2.5: "yellow", 3.6: "red"}) // shown for 1 s
	if n := count(evs, "", "overlay"); n != 2 {
		t.Errorf("%d overlay events", n)
	}
}

package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/biggaeroza/bga-mix/internal/project"
)

func testProject() *project.Project {
	a := 0.5
	return &project.Project{FormatVersion: 1, Name: "My Show!", Output: project.Output{Width: 1280, Height: 720, FPS: 30},
		Feeds:      []project.Feed{{ID: "cam", Type: "test"}, {ID: "clip", Type: "file", Path: `my "clip".mp4`, End: "hold"}, {ID: "card", Type: "image", Path: "/abs/card.png"}},
		Layouts:    []project.Layout{{ID: "pip", Boxes: []project.Box{{Feed: "cam", W: 1, H: 1}, {Feed: "clip", X: 0.6, Y: 0.05, W: 0.351, H: 0.35, Alpha: &a}}}},
		Recordings: []project.Recording{{Path: "rec/{project}-{date}"}, {Path: "rec/x", Container: "mkv", Codec: "h265", BitrateKbps: 9000}},
	}
}

// Boxes become pixel placements (even sizes), in layout order; feeds not shown are transparent.
func TestPlace(t *testing.T) {
	p := testProject()
	pp := Place(p, p.Start())
	if c := pp["cam"]; c != (PadProps{0, 0, 1280, 720, 1, 1}) {
		t.Errorf("cam %+v", c)
	}
	if c := pp["clip"]; c.X != 768 || c.Y != 36 || c.W != 450 || c.H != 252 || c.Z != 2 || c.Alpha != 0.5 {
		t.Errorf("clip %+v", c)
	}
	if c := pp["card"]; c.Alpha != 0 {
		t.Errorf("card not in the layout but visible: %+v", c)
	}
}

func TestPipelines(t *testing.T) {
	p := testProject()
	f := FeedPipeline(p.Feeds[1], p.Output, "/show", "ch")
	for _, want := range []string{`location="/show/my \"clip\".mp4"`, "decodebin", "videoscale add-borders=true", "colorimetry=bt709", `intervideosink channel="ch"`} {
		if !strings.Contains(f, want) {
			t.Errorf("file feed lacks %q: %s", want, f)
		}
	}
	if img := FeedPipeline(p.Feeds[2], p.Output, "/show", "ch"); !strings.Contains(img, `location="/abs/card.png"`) || !strings.Contains(img, "imagefreeze is-live=true") {
		t.Errorf("image feed: %s", img)
	}
	m := MixerPipeline(p, p.Start(), "R", []string{"/r/a.mp4", "/r/b.mkv"})
	for _, want := range []string{
		"start-time-selection=first", "sink_1::xpos=768", "sink_1::alpha=0.5", "sink_2::alpha=0",
		`intervideosrc channel="bgamix-R-clip" timeout=`, // held (end hold)
		"mp4mux fragment-duration=1000 streamable=true", `location="/r/a.mp4"`,
		"x265enc bitrate=9000", "matroskamux",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mixer lacks %q", want)
		}
	}
	if strings.Contains(m, `channel="bgamix-R-cam" timeout`) {
		t.Error("a test feed should go black, not be held, when it stops")
	}
}

func TestRecordingPath(t *testing.T) {
	p := testProject()
	at := time.Date(2026, 11, 18, 9, 5, 7, 0, time.UTC)
	if got := RecordingPath(p.Recordings[0], p, "/show", at); got != "/show/rec/My-Show-2026-11-18.mp4" {
		t.Errorf("%s", got)
	}
	if got := RecordingPath(project.Recording{Path: "/abs/{time}.mov"}, p, "/show", at); got != "/abs/09-05-07.mov" {
		t.Errorf("%s", got)
	}
}

func TestBitrate(t *testing.T) {
	o := project.Output{Width: 1280, Height: 720, FPS: 30}
	if b := Bitrate(project.Recording{}, o); b != 4100 {
		t.Errorf("720p30 h264 %d", b)
	}
	if b := Bitrate(project.Recording{Codec: "h265"}, o); b != 2500 {
		t.Errorf("720p30 h265 %d", b)
	}
}

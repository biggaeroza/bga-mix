package engine

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"
	"github.com/go-gst/go-gst/pkg/gstapp"

	"github.com/biggaeroza/bga-mix/internal/project"
)

// gstRun runs a finite pipeline to its end.
func gstRun(t *testing.T, desc string) {
	t.Helper()
	Init()
	pl, err := launch(desc)
	if err != nil {
		t.Fatal(err)
	}
	defer pl.SetState(gst.StateNull)
	pl.SetState(gst.StatePlaying)
	bus := pl.GetBus()
	for {
		msg := bus.TimedPop(30 * gst.Second)
		if msg == nil {
			t.Fatalf("timed out: %s", desc)
		}
		switch msg.Type() {
		case gst.MessageEOS:
			return
		case gst.MessageError:
			debug, err := msg.ParseError()
			t.Fatalf("%v %s: %s", err, debug, desc)
		}
	}
}

// makeClip writes a Matroska clip with video (the patterns in turn, frames each at 25 fps) and
// an Opus audio track, so the file feed meets a file with sound.
func makeClip(t *testing.T, path string, frames int, patterns ...string) {
	t.Helper()
	desc := "concat name=c ! videoconvert ! x264enc speed-preset=ultrafast key-int-max=5 ! h264parse ! matroskamux name=m ! filesink location=" + quote(path)
	for _, p := range patterns {
		desc += fmt.Sprintf(" videotestsrc pattern=%s num-buffers=%d ! video/x-raw,width=320,height=180,framerate=25/1 ! c.", p, frames)
	}
	desc += fmt.Sprintf(" audiotestsrc num-buffers=%d ! audioconvert ! opusenc ! m.", len(patterns)*frames*2)
	gstRun(t, desc)
}

func makePNG(t *testing.T, path string, c color.RGBA) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for y := 0; y < 36; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

type frame struct {
	w, h int
	rgb  []byte
	pts  time.Duration
}

// at is the colour at a point given as fractions of the picture.
func (f frame) at(fx, fy float64) (r, g, b int) {
	x, y := int(fx*float64(f.w)), int(fy*float64(f.h))
	i := (y*f.w + x) * 3
	return int(f.rgb[i]), int(f.rgb[i+1]), int(f.rgb[i+2])
}

// colour names the dominant colour at a point: red, green, blue, yellow, black, white, other.
func (f frame) colour(fx, fy float64) string {
	r, g, b := f.at(fx, fy)
	hi := func(v int) bool { return v > 160 }
	lo := func(v int) bool { return v < 70 }
	switch {
	case hi(r) && lo(g) && lo(b):
		return "red"
	case lo(r) && hi(g) && lo(b):
		return "green"
	case lo(r) && lo(g) && hi(b):
		return "blue"
	case hi(r) && hi(g) && lo(b):
		return "yellow"
	case lo(r) && lo(g) && lo(b):
		return "black"
	case hi(r) && hi(g) && hi(b):
		return "white"
	}
	return fmt.Sprintf("other(%d,%d,%d)", r, g, b)
}

// decode reads a recording's frames as RGB.
func decode(t *testing.T, path string) []frame {
	t.Helper()
	Init()
	pl, err := launch("filesrc location=" + quote(path) + " ! decodebin ! videoconvert ! video/x-raw,format=RGB ! appsink name=s sync=false")
	if err != nil {
		t.Fatal(err)
	}
	defer pl.SetState(gst.StateNull)
	sink := pl.GetByName("s").(gstapp.AppSink)
	pl.SetState(gst.StatePlaying)
	var out []frame
	for {
		smp := sink.TryPullSample(10 * gst.Second)
		if smp == nil {
			break
		}
		st := smp.GetCaps().GetStructure(0)
		w, _ := st.GetInt("width")
		h, _ := st.GetInt("height")
		buf := smp.GetBuffer()
		mi, ok := buf.Map(gst.MapRead)
		if !ok {
			t.Fatal("cannot map a frame")
		}
		out = append(out, frame{w: int(w), h: int(h), rgb: append([]byte(nil), mi.Data()...), pts: time.Duration(buf.PTS())})
		mi.Unmap()
	}
	if len(out) == 0 {
		t.Fatalf("%s: no frames decoded", path)
	}
	return out
}

func alpha(v float64) *float64 { return &v }

// runShow runs a show for a while and returns its recording's frames and its events.
func runShow(t *testing.T, p *project.Project, dir string, d time.Duration) ([]frame, []Event) {
	t.Helper()
	s, err := Start(p, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var evs []Event
	deadline := time.After(d)
loop:
	for {
		select {
		case e := <-s.Events():
			evs = append(evs, e)
			if e.Fatal {
				t.Fatalf("mixer: %s", e.Text)
			}
		case <-deadline:
			break loop
		}
	}
	s.Stop()
	for len(s.Events()) > 0 {
		evs = append(evs, <-s.Events())
	}
	for _, e := range evs {
		if e.Kind == "error" {
			t.Errorf("%s error: %s", e.Feed, e.Text)
		}
	}
	return decode(t, s.Recordings()[0]), evs
}

func count(evs []Event, feed, kind string) int {
	n := 0
	for _, e := range evs {
		if e.Feed == feed && e.Kind == kind {
			n++
		}
	}
	return n
}

// A layout of four feeds (two test patterns, a looping video file with sound, a still image)
// lands where it says in the recording, at the right frame rate; the 1 s clip loops in a 3 s
// show.
func TestLayoutInRecording(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer for a few seconds")
	}
	dir := t.TempDir()
	makeClip(t, filepath.Join(dir, "blue.mkv"), 25, "blue")
	makePNG(t, filepath.Join(dir, "yellow.png"), color.RGBA{255, 255, 0, 255})
	p := &project.Project{FormatVersion: 1, Name: "test", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{
			{ID: "red", Type: "test", Pattern: "red"},
			{ID: "green", Type: "test", Pattern: "green"},
			{ID: "clip", Type: "file", Path: "blue.mkv"},
			{ID: "logo", Type: "image", Path: "yellow.png"},
		},
		Layouts: []project.Layout{{ID: "quad", Boxes: []project.Box{
			{Feed: "red", W: 1, H: 1},
			{Feed: "green", X: 0.5, W: 0.5, H: 0.5},
			{Feed: "clip", Y: 0.5, W: 0.5, H: 0.5},
			{Feed: "logo", X: 0.5, Y: 0.5, W: 0.25, H: 0.25, Alpha: alpha(1)},
		}}},
		Recordings: []project.Recording{{Path: "out/{project}"}},
	}
	if err := p.Validate(dir); err != nil {
		t.Fatal(err)
	}
	frames, evs := runShow(t, p, dir, 3*time.Second)
	t.Logf("%d frames, %d loops", len(frames), count(evs, "clip", "looped"))
	if n := len(frames); n < 60 || n > 85 {
		t.Errorf("%d frames in 3 s at 25 fps", n)
	}
	last := frames[len(frames)-1]
	for _, c := range []struct {
		fx, fy float64
		want   string
	}{{0.25, 0.25, "red"}, {0.75, 0.25, "green"}, {0.25, 0.75, "blue"}, {0.62, 0.62, "yellow"}, {0.9, 0.9, "red"}} {
		if got := last.colour(c.fx, c.fy); got != c.want {
			t.Errorf("at (%.2f, %.2f): %s, want %s", c.fx, c.fy, got, c.want)
		}
	}
	if count(evs, "clip", "looped") < 1 {
		t.Error("the 1 s clip did not loop in 3 s")
	}
}

// A clip's end: "hold" keeps its last frame, "black" goes black (after intervideosrc's 1 s).
func TestEndBehaviour(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	for _, c := range []struct{ end, want string }{{"hold", "blue"}, {"black", "black"}} {
		t.Run(c.end, func(t *testing.T) {
			dir := t.TempDir()
			makeClip(t, filepath.Join(dir, "short.mkv"), 12, "blue") // about 0.5 s
			p := &project.Project{FormatVersion: 1, Name: "end", Output: project.Output{Width: 320, Height: 180, FPS: 25},
				Feeds:      []project.Feed{{ID: "clip", Type: "file", Path: "short.mkv", End: c.end}},
				Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
				Recordings: []project.Recording{{Path: "out/x.mkv", Container: "mkv"}},
			}
			frames, evs := runShow(t, p, dir, 2500*time.Millisecond)
			if got := frames[len(frames)-1].colour(0.5, 0.5); got != c.want {
				t.Errorf("end %s: the last frame is %s, want %s", c.end, got, c.want)
			}
			if count(evs, "clip", "ended") != 1 {
				t.Errorf("ended events: %d", count(evs, "clip", "ended"))
			}
		})
	}
}

// In and out points: a clip that is 1 s red then 1 s blue, played from 1.2 s to 1.8 s and held,
// never shows its red part.
func TestInOutPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	dir := t.TempDir()
	makeClip(t, filepath.Join(dir, "two.mkv"), 25, "red", "blue")
	p := &project.Project{FormatVersion: 1, Name: "inout", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:      []project.Feed{{ID: "clip", Type: "file", Path: "two.mkv", InS: 1.2, OutS: 1.8, End: "hold"}},
		Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
		Recordings: []project.Recording{{Path: "out/x.mp4"}},
	}
	frames, _ := runShow(t, p, dir, 2*time.Second)
	red, blue := 0, 0
	for _, f := range frames {
		switch f.colour(0.5, 0.5) {
		case "red":
			red++
		case "blue":
			blue++
		}
	}
	t.Logf("%d frames: %d blue, %d red", len(frames), blue, red)
	if red > 0 || blue < len(frames)/2 {
		t.Errorf("in/out: %d red and %d blue frames of %d", red, blue, len(frames))
	}
}

// The other containers and H.265 record and decode.
func TestContainers(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	for _, r := range []project.Recording{{Path: "out/a", Container: "mkv"}, {Path: "out/b", Container: "mov"}, {Path: "out/c", Codec: "h265"}} {
		t.Run(r.Path, func(t *testing.T) {
			p := &project.Project{FormatVersion: 1, Name: "c", Output: project.Output{Width: 320, Height: 180, FPS: 25},
				Feeds:      []project.Feed{{ID: "g", Type: "test", Pattern: "green"}},
				Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "g", W: 1, H: 1}}}},
				Recordings: []project.Recording{r},
			}
			frames, _ := runShow(t, p, t.TempDir(), 1500*time.Millisecond)
			if len(frames) < 25 || frames[len(frames)-1].colour(0.5, 0.5) != "green" {
				t.Errorf("%d frames, last %s", len(frames), frames[len(frames)-1].colour(0.5, 0.5))
			}
		})
	}
}

// A recording survives a crash: a show killed outright (SIGKILL, no clean stop) leaves an MP4
// that plays up to its last complete fragment.
func TestCrashSafeRecording(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	if os.Getenv("BGAMIX_CRASH_HELPER") != "" { // the show that gets killed
		p := &project.Project{FormatVersion: 1, Name: "crash", Output: project.Output{Width: 320, Height: 180, FPS: 25},
			Feeds:      []project.Feed{{ID: "g", Type: "test", Pattern: "green"}},
			Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "g", W: 1, H: 1}}}},
			Recordings: []project.Recording{{Path: os.Getenv("BGAMIX_CRASH_HELPER")}},
		}
		if _, err := Start(p, "", Options{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour)
	}
	out := filepath.Join(t.TempDir(), "crash.mp4")
	cmd := exec.Command(os.Args[0], "-test.run", "^TestCrashSafeRecording$")
	cmd.Env = append(os.Environ(), "BGAMIX_CRASH_HELPER="+out)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3500 * time.Millisecond)
	cmd.Process.Kill() // SIGKILL
	cmd.Wait()
	frames := decode(t, out)
	t.Logf("%d frames after the kill", len(frames))
	if len(frames) < 50 || frames[len(frames)-1].colour(0.5, 0.5) != "green" {
		t.Errorf("%d frames, last %s", len(frames), frames[len(frames)-1].colour(0.5, 0.5))
	}
}

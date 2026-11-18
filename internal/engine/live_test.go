package engine

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/biggaeroza/bga-mix/internal/project"
)

// step is something done to a running show at a time after its start.
type step struct {
	at time.Duration
	do func(s *Show)
}

// live runs a show, doing the steps at their times, stops it at end and returns it with its
// events (feed errors are not test failures here; mixer errors are).
func live(t *testing.T, p *project.Project, dir string, o Options, end time.Duration, steps ...step) (*Show, []Event) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	s, err := Start(p, dir, o)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	var evs []Event
	collect := func(until time.Duration) {
		for {
			left := until - time.Since(t0)
			if left <= 0 {
				return
			}
			select {
			case e := <-s.Events():
				evs = append(evs, e)
				if e.Fatal {
					t.Fatalf("mixer: %s", e.Text)
				}
			case <-time.After(left):
				return
			}
		}
	}
	for _, st := range steps {
		collect(st.at)
		st.do(s)
	}
	collect(end)
	s.Stop()
	for len(s.Events()) > 0 {
		evs = append(evs, <-s.Events())
	}
	return s, evs
}

// colourAt is the colour at (fx, fy) of the frame nearest sec seconds into the recording.
func colourAt(frames []frame, sec, fx, fy float64) string {
	best := frames[0]
	for _, f := range frames {
		if (f.pts - time.Duration(sec*1e9)).Abs() < (best.pts - time.Duration(sec*1e9)).Abs() {
			best = f
		}
	}
	return best.colour(fx, fy)
}

func expectColours(t *testing.T, frames []frame, fx, fy float64, want map[float64]string) {
	t.Helper()
	for sec, c := range want {
		if got := colourAt(frames, sec, fx, fy); got != c {
			t.Errorf("at %.2f s (%.2f, %.2f): %s, want %s", sec, fx, fy, got, c)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Error(err)
	}
}

func twoColourShow(dir string) *project.Project {
	return &project.Project{FormatVersion: 1, Name: "live", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{{ID: "red", Type: "test", Pattern: "red"}, {ID: "green", Type: "test", Pattern: "green"}},
		Layouts: []project.Layout{
			{ID: "red", Boxes: []project.Box{{Feed: "red", W: 1, H: 1}}},
			{ID: "green", Boxes: []project.Box{{Feed: "green", W: 1, H: 1}}},
			{ID: "pip", Boxes: []project.Box{{Feed: "red", W: 1, H: 1}, {Feed: "green", X: 0.5, Y: 0.5, W: 0.5, H: 0.5}}},
		},
		Recordings: []project.Recording{{Path: filepath.Join(dir, "out.mkv"), Container: "mkv"}},
	}
}

// Layouts switch live: a cut is immediate, a mix blends over its duration.
func TestLiveLayout(t *testing.T) {
	dir := t.TempDir()
	p := twoColourShow(dir)
	var midMix string
	s, evs := live(t, p, dir, Options{}, 4500*time.Millisecond,
		step{1 * time.Second, func(s *Show) { must(t, s.SetLayout("green", nil)) }},
		step{2 * time.Second, func(s *Show) { must(t, s.SetLayout("pip", nil)) }},
		step{3 * time.Second, func(s *Show) {
			must(t, s.SetLayout("red", &project.Transition{Kind: project.TransitionMix, DurationS: 1}))
		}},
		step{3500 * time.Millisecond, func(s *Show) {
			if st := s.Status(); !st.Transitioning || st.Layout != "red" || st.Previous != "pip" {
				t.Errorf("mid-mix status %+v", st)
			}
		}},
	)
	if err := s.SetLayout("nope", nil); err == nil {
		t.Error("an unknown layout was accepted")
	}
	frames := decode(t, s.Recordings()[0])
	expectColours(t, frames, 0.25, 0.25, map[float64]string{0.6: "red", 1.4: "green", 1.8: "green", 2.4: "red", 4.3: "red"})
	expectColours(t, frames, 0.75, 0.75, map[float64]string{0.6: "red", 1.4: "green", 2.4: "green", 2.8: "green", 4.3: "red"})
	// half-way through the mix the inset is half faded
	midMix = colourAt(frames, 3.5, 0.75, 0.75)
	if midMix == "red" || midMix == "green" {
		t.Errorf("mid-mix inset is plain %s", midMix)
	}
	if n := count(evs, "", "layout"); n != 3 {
		t.Errorf("%d layout events", n)
	}
}

// Files are controlled live: pause, seek (shown while paused), play to the end, restart.
func TestTransport(t *testing.T) {
	dir := t.TempDir()
	makeClip(t, filepath.Join(dir, "rb.mkv"), 25, "red", "blue") // 1 s red, 1 s blue
	p := &project.Project{FormatVersion: 1, Name: "transport", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:      []project.Feed{{ID: "clip", Type: "file", Path: "rb.mkv", End: "hold"}},
		Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
		Recordings: []project.Recording{{Path: "out.mkv", Container: "mkv"}},
	}
	s, evs := live(t, p, dir, Options{}, 4500*time.Millisecond,
		step{300 * time.Millisecond, func(s *Show) { must(t, s.FeedCommand("clip", "pause", 0)) }},
		step{time.Second, func(s *Show) {
			f := s.Status().Feeds[0]
			if f.State != "paused" || f.PositionS > 0.6 || f.DurationS < 1.9 {
				t.Errorf("paused status %+v", f)
			}
			must(t, s.FeedCommand("clip", "seek", 1.5))
		}},
		step{2 * time.Second, func(s *Show) { must(t, s.FeedCommand("clip", "play", 0)) }},
		step{3200 * time.Millisecond, func(s *Show) {
			if f := s.Status().Feeds[0]; f.State != "ended" {
				t.Errorf("after the end %+v", f)
			}
			must(t, s.FeedCommand("clip", "restart", 0))
		}},
	)
	if err := s.FeedCommand("clip", "next", 0); err == nil {
		t.Error("next on a file")
	}
	frames := decode(t, s.Recordings()[0])
	expectColours(t, frames, 0.5, 0.5, map[float64]string{0.8: "red", 1.5: "blue", 2.8: "blue", 3.6: "red"})
	if n := count(evs, "clip", "ended"); n != 1 {
		t.Errorf("%d ended events", n)
	}
}

// A playlist plays its items in turn; "return" switches back to the previous layout at its end.
func TestPlaylistReturn(t *testing.T) {
	dir := t.TempDir()
	makeClip(t, filepath.Join(dir, "r.mkv"), 15, "red")
	makeClip(t, filepath.Join(dir, "b.mkv"), 15, "blue")
	p := &project.Project{FormatVersion: 1, Name: "playlist", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{{ID: "cam", Type: "test", Pattern: "green"},
			{ID: "pl", Type: "playlist", Items: []string{"r.mkv", "b.mkv"}, End: "return"}},
		Layouts: []project.Layout{{ID: "cams", Boxes: []project.Box{{Feed: "cam", W: 1, H: 1}}},
			{ID: "clips", Boxes: []project.Box{{Feed: "pl", W: 1, H: 1}}}},
		Recordings: []project.Recording{{Path: "out.mkv", Container: "mkv"}},
	}
	s, evs := live(t, p, dir, Options{}, 3*time.Second,
		step{500 * time.Millisecond, func(s *Show) {
			must(t, s.FeedCommand("pl", "restart", 0))
			must(t, s.SetLayout("clips", nil))
		}},
	)
	for _, e := range evs {
		t.Logf("%s %s %s %s", e.Time.Format("05.000"), e.Feed, e.Kind, e.Text)
	}
	frames := decode(t, s.Recordings()[0])
	tl := ""
	for i := 0; i < len(frames); i += 2 {
		tl += frames[i].colour(0.5, 0.5)[:1]
	}
	t.Logf("timeline (every 80 ms): %s", tl)
	// each item plays its 0.6 s plus the start of its pipeline (about 0.15 s)
	expectColours(t, frames, 0.5, 0.5, map[float64]string{0.3: "green", 1.0: "red", 1.85: "blue", 2.7: "green"})
	if count(evs, "pl", "next") != 1 || count(evs, "pl", "ended") != 1 {
		t.Errorf("events %+v", evs)
	}
	if st := s.Status(); st.Layout != "cams" {
		t.Errorf("layout %s after the playlist ended", st.Layout)
	}
}

// Recordings are controlled live: a manual recording started, paused (the pause left out) and
// stopped; an automatic one in segments; a clean ISO recording of a feed.
func TestRecordingControls(t *testing.T) {
	dir := t.TempDir()
	p := twoColourShow(dir)
	p.StartLayout = "pip"
	p.Recordings = []project.Recording{
		{Path: "seg.mp4", SegmentS: 2},
		{Path: "manual.mkv", Container: "mkv", Manual: true},
	}
	p.Feeds[1].ISO = &project.Recording{Path: "iso-{feed}.mkv", Container: "mkv"}
	live(t, p, dir, Options{}, 5500*time.Millisecond,
		step{500 * time.Millisecond, func(s *Show) {
			if st := s.Status().Recordings; st[1].State != "idle" || st[0].State != "recording" {
				t.Errorf("before start %+v", st)
			}
			must(t, s.RecordingCommand("program-2", "start"))
		}},
		step{1500 * time.Millisecond, func(s *Show) { must(t, s.RecordingCommand("program-2", "pause")) }},
		step{2500 * time.Millisecond, func(s *Show) { must(t, s.RecordingCommand("program-2", "resume")) }},
		step{3500 * time.Millisecond, func(s *Show) {
			if r := s.Status().Recordings[1]; r.ElapsedS < 1.7 || r.ElapsedS > 2.3 || r.Bytes == 0 {
				t.Errorf("recorded time %+v", r)
			}
			must(t, s.RecordingCommand("program-2", "stop"))
		}},
	)
	segs, _ := filepath.Glob(filepath.Join(dir, "seg-*.mp4"))
	if len(segs) < 2 {
		t.Fatalf("segments %v", segs)
	}
	sort.Strings(segs)
	for _, f := range segs[:len(segs)-1] { // the last is cut short by the stop
		if fr := decode(t, f); len(fr) < 25 {
			t.Errorf("%s: %d frames", f, len(fr))
		}
	}
	man := decode(t, filepath.Join(dir, "manual.mkv"))
	if d := man[len(man)-1].pts - man[0].pts; d < 1600*time.Millisecond || d > 2400*time.Millisecond {
		t.Errorf("manual recording %v long (2 s recorded, 1 s paused)", d)
	}
	iso := decode(t, filepath.Join(dir, "iso-green.mkv"))
	if c := iso[len(iso)/2].colour(0.1, 0.1); c != "green" {
		t.Errorf("ISO top-left %s (clean feed, not the program)", c)
	}
}

// A feed that fails is left transparent and reconnected with back-off; the mix goes on.
func TestReconnect(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "clip.mkv")
	os.WriteFile(bad, []byte("not a video"), 0o644)
	good := filepath.Join(t.TempDir(), "good.mkv")
	makeClip(t, good, 25, "blue")
	p := &project.Project{FormatVersion: 1, Name: "reconnect", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{{ID: "bg", Type: "test", Pattern: "red"}, {ID: "clip", Type: "file", Path: "clip.mkv"}},
		Layouts: []project.Layout{{ID: "pip", Boxes: []project.Box{{Feed: "bg", W: 1, H: 1},
			{Feed: "clip", X: 0.5, Y: 0.5, W: 0.5, H: 0.5}}}},
		Recordings: []project.Recording{{Path: "out.mkv", Container: "mkv"}},
	}
	s, evs := live(t, p, dir, Options{}, 4*time.Second,
		step{400 * time.Millisecond, func(s *Show) {
			if f := s.Status().Feeds[1]; f.State != "restarting" || f.Error == "" || !f.Blank {
				t.Errorf("failed feed %+v", f)
			}
			b, _ := os.ReadFile(good)
			os.WriteFile(bad, b, 0o644)
		}},
	)
	frames := decode(t, s.Recordings()[0])
	expectColours(t, frames, 0.75, 0.75, map[float64]string{0.5: "red", 3.5: "blue"})
	expectColours(t, frames, 0.25, 0.25, map[float64]string{0.5: "red", 3.5: "red"})
	reconnected := false
	for _, e := range evs {
		reconnected = reconnected || (e.Feed == "clip" && e.Kind == "started" && e.Text == "reconnected")
	}
	if count(evs, "clip", "restarting") < 1 || !reconnected {
		t.Errorf("events %+v", evs)
	}
}

// Previews are small JPEGs of the program and every feed.
func TestPreviews(t *testing.T) {
	dir := t.TempDir()
	p := twoColourShow(dir)
	p.Recordings = nil
	live(t, p, dir, Options{Previews: true}, 1500*time.Millisecond,
		step{1400 * time.Millisecond, func(s *Show) {
			for _, k := range []string{"program", "red", "green"} {
				b, at, ok := s.Preview(k)
				if !ok || time.Since(at) > time.Second {
					t.Errorf("%s: no recent preview", k)
					continue
				}
				img, err := jpeg.Decode(bytes.NewReader(b))
				if err != nil || img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 {
					t.Errorf("%s: %v %v", k, err, img.Bounds())
				}
			}
			if _, _, ok := s.Preview("nope"); ok {
				t.Error("preview of an unknown feed")
			}
		}},
	)
}

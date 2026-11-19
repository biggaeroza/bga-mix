package app

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/biggaeroza/bga-mix/internal/project"
)

func gstLaunch(t *testing.T, args ...string) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	if out, err := exec.Command("gst-launch-1.0", append([]string{"-q"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("gst-launch: %v\n%s", err, out)
	}
}

// makeMedia writes a 3 s 320×240 25 fps clip with sound, a PNG and a broken file.
func makeMedia(t *testing.T, dir string) (clip, png, bad string) {
	clip, png, bad = filepath.Join(dir, "clip.mkv"), filepath.Join(dir, "card.png"), filepath.Join(dir, "broken.mp4")
	gstLaunch(t, "videotestsrc", "num-buffers=75", "!", "video/x-raw,width=320,height=240,framerate=25/1", "!", "x264enc", "!", "matroskamux", "name=m", "!", "filesink", "location="+clip,
		"audiotestsrc", "num-buffers=140", "!", "opusenc", "!", "m.")
	gstLaunch(t, "videotestsrc", "num-buffers=1", "pattern=red", "!", "video/x-raw,width=200,height=100", "!", "pngenc", "!", "filesink", "location="+png)
	os.WriteFile(bad, []byte("this is not a video"), 0o644)
	return
}

func TestProbe(t *testing.T) {
	dir := t.TempDir()
	clip, png, bad := makeMedia(t, dir)
	s := New()
	m, err := s.Probe(clip)
	if err != nil || !m.OK || m.Kind != "video" || m.Width != 320 || m.Height != 240 || m.FPS != 25 || m.DurationS < 2.9 || m.DurationS > 3.1 ||
		!strings.Contains(m.Codec, "264") || m.Audio == "" {
		t.Errorf("clip %s %v", desc(m), err)
	}
	if img, err := jpeg.Decode(bytes.NewReader(m.Thumb)); err != nil || img.Bounds().Dx() != thumbW {
		t.Errorf("thumbnail %v", err)
	}
	if m2, _ := s.Probe(clip); m2 != m {
		t.Error("not cached")
	}
	if m, err := s.Probe(png); err != nil || !m.OK || m.Kind != "image" || m.Width != 200 || m.DurationS != 0 {
		t.Errorf("png %s %v", desc(m), err)
	}
	if m, err := s.Probe(bad); err != nil || m.OK || m.Error == "" {
		t.Errorf("broken %s %v", desc(m), err)
	}
	if _, err := s.Probe(filepath.Join(dir, "nope.mp4")); err == nil {
		t.Error("missing file")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	for _, f := range []string{"a.MP4", "b.png", "notes.txt", ".hidden.mp4", "show.bgamix"} {
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range l.Entries {
		got = append(got, e.Name+":"+e.Kind)
	}
	if strings.Join(got, " ") != "sub: a.MP4:video b.png:image show.bgamix:show" || l.Parent != filepath.Dir(dir) {
		t.Errorf("%v %s", got, l.Parent)
	}
	if Relative("/show/a.bgamix", "/show/media/x.mp4") != "media/x.mp4" || Relative("/show/a.bgamix", "/other/x.mp4") != "/other/x.mp4" || Relative("", "/x") != "/x" {
		t.Error("Relative")
	}
}

func TestStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	s := New()
	p := NewProject()
	p.Output = project.Output{Width: 320, Height: 180, FPS: 25}
	p.Feeds = append(p.Feeds, project.Feed{ID: "gone", Type: "file", Path: "/nope.mp4"})
	if err := s.Start(p, ""); err == nil || !strings.Contains(err.Error(), "feeds[2].path") {
		t.Fatalf("a show with a missing file started: %v", err)
	}
	p.Feeds = p.Feeds[:2]
	if err := s.Start(p, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if s.Show() == nil || s.Show().Status().Layout != "full" {
		t.Fatal("not running")
	}
	s.Show().SetLayout("pip", nil)
	time.Sleep(100 * time.Millisecond)
	s.Stop()
	evs, seq := s.Events(0)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	k := strings.Join(kinds, " ")
	if !strings.HasPrefix(k, "started layout") || !strings.HasSuffix(k, "stopped") || seq != len(evs) {
		t.Errorf("events %s (seq %d)", k, seq)
	}
	seen := map[string]int{}
	for _, e := range evs {
		seen[e.Kind+" "+e.Text+" "+e.Time.String()]++
		if seen[e.Kind+" "+e.Text+" "+e.Time.String()] > 1 {
			t.Errorf("logged twice: %s %s", e.Kind, e.Text)
		}
	}
	if later, _ := s.Events(seq - 1); len(later) != 1 {
		t.Errorf("after %d: %d", seq-1, len(later))
	}
	if s.Show() != nil {
		t.Error("still running")
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	s := New()
	p := NewProject()
	if err := s.Save(filepath.Join(dir, "x"), p); err != nil {
		t.Fatal(err)
	}
	q, err := s.Open(filepath.Join(dir, "x.bgamix"))
	if err != nil || q.Name != p.Name || len(q.Layouts) != 2 {
		t.Errorf("%+v %v", q, err)
	}
	p.Output.FPS = 0
	if err := s.Save(filepath.Join(dir, "y.bgamix"), p); err == nil {
		t.Error("an invalid project was saved")
	}
}

func desc(m *Media) string { c := *m; c.Thumb = nil; return fmt.Sprintf("%+v", c) }

package engine

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"
	"github.com/go-gst/go-gst/pkg/gstapp"

	"github.com/biggaeroza/bga-mix/internal/project"
)

// makeToneClip writes a clip whose picture is one colour and whose sound is a loud tone.
func makeToneClip(t *testing.T, path, pattern string, frames int) {
	t.Helper()
	gstRun(t, fmt.Sprintf("videotestsrc pattern=%s num-buffers=%d ! video/x-raw,width=320,height=180,framerate=25/1 ! x264enc speed-preset=ultrafast key-int-max=5 ! h264parse ! matroskamux name=m ! filesink location=%s"+
		" audiotestsrc wave=sine freq=880 volume=0.5 num-buffers=%d samplesperbuffer=1920 ! audio/x-raw,rate=48000 ! audioconvert ! opusenc ! m.", pattern, frames, quote(path), frames))
}

// waitLevel waits up to 1.5 s for a meter to satisfy ok.
func waitLevel(s *Show, key string, ok func(Level) bool) (Level, bool) {
	var lv Level
	for t0 := time.Now(); time.Since(t0) < 1500*time.Millisecond; time.Sleep(50 * time.Millisecond) {
		lv = s.Levels()[key]
		if ok(lv) {
			return lv, true
		}
	}
	return lv, false
}

func loud(lv Level) bool   { return lv.RMSDB > -20 }
func silent(lv Level) bool { return lv.PeakDB < -70 }

// Audio follows video: a feed is heard while it is on air; its fader, mute and mode work live.
func TestAudioFollowsVideo(t *testing.T) {
	dir := t.TempDir()
	makeToneClip(t, filepath.Join(dir, "tone.mkv"), "blue", 50)
	p := &project.Project{FormatVersion: 1, Name: "afv", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds: []project.Feed{{ID: "cam", Type: "test", Pattern: "red"}, {ID: "clip", Type: "file", Path: "tone.mkv"}},
		Layouts: []project.Layout{{ID: "cam", Boxes: []project.Box{{Feed: "cam", W: 1, H: 1}}},
			{ID: "clip", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
	}
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	s, err := Start(p, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if lv, ok := waitLevel(s, "clip", loud); !ok {
		t.Errorf("the clip's own meter (before its fader) %+v", lv)
	}
	if lv, ok := waitLevel(s, "program", silent); !ok {
		t.Errorf("off air but heard: %+v", lv)
	}
	s.SetLayout("clip", nil)
	if lv, ok := waitLevel(s, "program", loud); !ok {
		t.Errorf("on air but not heard: %+v", lv)
	}
	time.Sleep(300 * time.Millisecond) // a full reading (the first may cover the cut)
	full := s.Levels()["program"]
	mute := true
	must(t, s.AudioCommand("clip", nil, &mute, ""))
	if lv, ok := waitLevel(s, "program", silent); !ok {
		t.Errorf("muted but heard: %+v", lv)
	}
	mute = false
	db := -20.0
	must(t, s.AudioCommand("clip", &db, &mute, ""))
	time.Sleep(400 * time.Millisecond)
	if lv := s.Levels()["program"]; math.Abs(full.RMSDB-20-lv.RMSDB) > 2 {
		t.Errorf("at -20 dB: %.1f dB, was %.1f", lv.RMSDB, full.RMSDB)
	}
	s.SetLayout("cam", nil)
	if lv, ok := waitLevel(s, "program", silent); !ok {
		t.Errorf("cut away but heard: %+v", lv)
	}
	must(t, s.AudioCommand("clip", nil, nil, "on"))
	if lv, ok := waitLevel(s, "program", func(l Level) bool { return l.RMSDB > -45 }); !ok {
		t.Errorf("mode on, off air, not heard: %+v", lv)
	}
	if f := s.Status().Feeds[1]; !f.HasAudio || f.Audio != "on" || f.VolumeDB != -20 || f.Level == nil {
		t.Errorf("status %+v", f)
	}
	if err := s.AudioCommand("cam", nil, &mute, ""); err == nil {
		t.Error("a test pattern has no sound to mute")
	}
}

// decodeAudio reads a recording's sound as mono samples (48 kHz) with the first one's time.
func decodeAudio(t *testing.T, path string) ([]float32, time.Duration) {
	t.Helper()
	Init()
	pl, err := launch("filesrc location=" + quote(path) + " ! decodebin ! audioconvert ! audioresample ! audio/x-raw,format=F32LE,channels=1,rate=48000 ! appsink name=s sync=false")
	if err != nil {
		t.Fatal(err)
	}
	defer pl.SetState(gst.StateNull)
	pl.SetState(gst.StatePlaying)
	sink := pl.GetByName("s").(gstapp.AppSink)
	var out []float32
	first := time.Duration(-1)
	for {
		smp := sink.TryPullSample(5 * gst.Second)
		if smp == nil {
			break
		}
		buf := smp.GetBuffer()
		if first < 0 {
			first = time.Duration(buf.PTS())
		}
		mi, _ := buf.Map(gst.MapRead)
		d := mi.Data()
		for i := 0; i+4 <= len(d); i += 4 {
			out = append(out, math.Float32frombits(binary.LittleEndian.Uint32(d[i:])))
		}
		mi.Unmap()
	}
	return out, first
}

// The recording has the program's sound, in step with its picture: a clip that turns white and
// beeps at the same moment shows both within a frame or two in the recording.
func TestRecordedSoundInSync(t *testing.T) {
	dir := t.TempDir()
	clip := filepath.Join(dir, "flash.mkv")
	gstRun(t, "concat name=v ! x264enc speed-preset=ultrafast key-int-max=5 ! h264parse ! matroskamux name=m ! filesink location="+quote(clip)+
		" videotestsrc pattern=black num-buffers=25 ! video/x-raw,width=320,height=180,framerate=25/1 ! v."+
		" videotestsrc pattern=white num-buffers=25 ! video/x-raw,width=320,height=180,framerate=25/1 ! v."+
		" concat name=a ! audioconvert ! opusenc ! m."+
		" audiotestsrc wave=silence num-buffers=25 samplesperbuffer=1920 ! audio/x-raw,rate=48000,channels=1 ! a."+
		" audiotestsrc wave=sine freq=1000 volume=0.6 num-buffers=25 samplesperbuffer=1920 ! audio/x-raw,rate=48000,channels=1 ! a.")
	p := &project.Project{FormatVersion: 1, Name: "sync", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:      []project.Feed{{ID: "clip", Type: "file", Path: "flash.mkv", End: "hold"}},
		Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
		Recordings: []project.Recording{{Path: "out.mp4"}},
	}
	s, _ := live(t, p, dir, Options{}, 3*time.Second)
	frames := decode(t, s.Recordings()[0])
	var white time.Duration = -1
	for _, f := range frames {
		if f.colour(0.5, 0.5) == "white" {
			white = f.pts
			break
		}
	}
	samples, a0 := decodeAudio(t, s.Recordings()[0])
	var beep time.Duration = -1
	for i, v := range samples {
		if math.Abs(float64(v)) > 0.2 {
			beep = a0 + time.Duration(i)*time.Second/48000
			break
		}
	}
	t.Logf("white at %v, beep at %v: sound %+v after picture", white, beep, beep-white)
	if white < 0 || beep < 0 {
		t.Fatalf("no flash (%v) or no beep (%v) in the recording", white, beep)
	}
	if d := (beep - white).Abs(); d > 80*time.Millisecond {
		t.Errorf("sound and picture %v apart", d)
	}
}

func freePort(t *testing.T) int {
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

// One show streams over SRT; another takes it in as a stream feed and records it: picture and
// sound arrive.
func TestSRTLoop(t *testing.T) {
	dir := t.TempDir()
	makeToneClip(t, filepath.Join(dir, "tone.mkv"), "green", 50)
	port := freePort(t)
	send := &project.Project{FormatVersion: 1, Name: "send", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:   []project.Feed{{ID: "clip", Type: "file", Path: "tone.mkv"}},
		Layouts: []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
		Streams: []project.Stream{{URL: fmt.Sprintf("srt://127.0.0.1:%d?mode=listener", port), VideoKbps: 800}},
	}
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	sender, err := Start(send, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Stop()
	recv := &project.Project{FormatVersion: 1, Name: "recv", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:      []project.Feed{{ID: "net", Type: "stream", URL: fmt.Sprintf("srt://127.0.0.1:%d", port), Audio: "on"}},
		Layouts:    []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "net", W: 1, H: 1}}}},
		Recordings: []project.Recording{{Path: "got.mkv", Container: "mkv"}},
	}
	r, evs := live(t, recv, dir, Options{}, 5*time.Second)
	if st := sender.Status().Streams[0]; st.State != "live" || st.Encoder == "" {
		t.Errorf("sender %+v", st)
	}
	frames := decode(t, r.Recordings()[0])
	if c := frames[len(frames)-10].colour(0.5, 0.5); c != "green" {
		t.Errorf("received picture %s (events %+v)", c, evs)
	}
	samples, _ := decodeAudio(t, r.Recordings()[0])
	peak := 0.0
	for _, v := range samples[len(samples)/2:] {
		peak = math.Max(peak, math.Abs(float64(v)))
	}
	if peak < 0.2 {
		t.Errorf("received sound peaks at %.2f", peak)
	}
}

// A UDP MPEG-TS stream (as sent by an encoder or another program) is a feed.
func TestUDPFeed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	dir := t.TempDir()
	port := freePort(t)
	Init()
	tx, err := launch(fmt.Sprintf("videotestsrc is-live=true pattern=blue ! video/x-raw,width=320,height=180,framerate=25/1 ! x264enc tune=zerolatency speed-preset=ultrafast key-int-max=10 ! h264parse ! mpegtsmux alignment=7 ! udpsink host=127.0.0.1 port=%d", port))
	if err != nil {
		t.Fatal(err)
	}
	tx.SetState(gst.StatePlaying)
	defer tx.SetState(gst.StateNull)
	p := &project.Project{FormatVersion: 1, Name: "udp", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:      []project.Feed{{ID: "bg", Type: "test", Pattern: "red"}, {ID: "net", Type: "stream", URL: fmt.Sprintf("udp://127.0.0.1:%d", port)}},
		Layouts:    []project.Layout{{ID: "pip", Boxes: []project.Box{{Feed: "bg", W: 1, H: 1}, {Feed: "net", X: 0.5, Y: 0.5, W: 0.5, H: 0.5}}}},
		Recordings: []project.Recording{{Path: "out.mkv", Container: "mkv"}},
	}
	s, _ := live(t, p, dir, Options{}, 4*time.Second)
	frames := decode(t, s.Recordings()[0])
	last := frames[len(frames)-5]
	if a, b := last.colour(0.25, 0.25), last.colour(0.75, 0.75); a != "red" || b != "blue" {
		t.Errorf("background %s, stream %s", a, b)
	}
}

// A stream that cannot connect is retried with back-off; stopping it ends the retries.
func TestStreamReconnects(t *testing.T) {
	dir := t.TempDir()
	p := twoColourShow(dir)
	p.Recordings = nil
	p.Streams = []project.Stream{{URL: fmt.Sprintf("srt://127.0.0.1:%d?mode=caller", freePort(t))}}
	// srtsink tries to connect for about 3 s before it reports the receiver missing
	s, evs := live(t, p, dir, Options{}, 6*time.Second,
		step{5500 * time.Millisecond, func(s *Show) {
			st := s.Status().Streams[0]
			if st.State != "reconnecting" && (st.State != "live" || st.Error == "") {
				t.Errorf("stream %+v", st)
			}
			must(t, s.RecordingCommand("stream-1", "stop"))
			if st := s.Status().Streams[0]; st.State != "idle" {
				t.Errorf("after stop %+v", st)
			}
		}},
	)
	_ = s
	if count(evs, "", "restarting")+count(evs, "", "warning") < 1 {
		t.Errorf("no retry: %+v", evs)
	}
}

// A looping clip's sound goes on through its loops, a seek and a restart (a flushing seek while
// playing once silenced it for seconds).
func TestSoundThroughLoopsAndSeeks(t *testing.T) {
	dir := t.TempDir()
	makeToneClip(t, filepath.Join(dir, "tone.mkv"), "blue", 40) // 1.6 s
	p := &project.Project{FormatVersion: 1, Name: "loops", Output: project.Output{Width: 320, Height: 180, FPS: 25},
		Feeds:   []project.Feed{{ID: "clip", Type: "file", Path: "tone.mkv"}},
		Layouts: []project.Layout{{ID: "full", Boxes: []project.Box{{Feed: "clip", W: 1, H: 1}}}},
	}
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	s, err := Start(p, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	time.Sleep(700 * time.Millisecond)
	var line string
	silentFor := 0
	for i := 0; i < 48; i++ { // 6 s: about 3 loops, a seek at 2 s and a restart at 4 s
		switch i {
		case 16:
			must(t, s.FeedCommand("clip", "seek", 1.2))
		case 32:
			must(t, s.FeedCommand("clip", "restart", 0))
		}
		time.Sleep(125 * time.Millisecond)
		lv := s.Levels()["clip"]
		line += fmt.Sprintf("%.0f ", lv.PeakDB)
		if lv.PeakDB < -60 {
			silentFor++
		} else {
			silentFor = 0
		}
		if silentFor > 3 {
			t.Fatalf("the sound stopped for over 0.4 s: %s", line)
		}
	}
	if n := count(collect(s), "clip", "looped"); n < 2 {
		t.Errorf("%d loops", n)
	}
}

// collect drains a show's pending events.
func collect(s *Show) []Event {
	var evs []Event
	for len(s.Events()) > 0 {
		evs = append(evs, <-s.Events())
	}
	return evs
}

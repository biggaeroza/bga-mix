package engine

import (
	"os"
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
	f := FeedPipeline(p.Feeds[1], 0, p.Output, "/show", "ch", "")
	for _, want := range []string{`location="/show/my \"clip\".mp4"`, "decodebin", "videoscale add-borders=true", "colorimetry=bt709", `intervideosink name=out show-preroll-frame=false channel="ch"`} {
		if !strings.Contains(f, want) {
			t.Errorf("file feed lacks %q: %s", want, f)
		}
	}
	if img := FeedPipeline(p.Feeds[2], 0, p.Output, "/show", "ch", ""); !strings.Contains(img, `location="/abs/card.png"`) || !strings.Contains(img, "imagefreeze is-live=true") {
		t.Errorf("image feed: %s", img)
	}
	m := MixerPipeline(p, p.Start(), "R")
	for _, want := range []string{
		"start-time-selection=first", "sink_1::xpos=768", "sink_1::alpha=0.5", "sink_2::alpha=0",
		`intervideosrc channel="bgamix-R-f-clip" timeout=`, // held (end hold)
		`intervideosink channel="bgamix-R-program"`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mixer lacks %q", want)
		}
	}
	if strings.Contains(m, `channel="bgamix-R-f-cam" timeout`) {
		t.Error("a test feed should go black, not be held, when it stops")
	}
	r := RecordingPipeline(p.Recordings[0], p.Output, "ch", false, "", "software", "/r/a.mp4")
	for _, want := range []string{"mp4mux fragment-duration=1000 streamable=true", `location="/r/a.mp4"`, "buffer-mode=unbuffered", "x264enc bitrate=4100"} {
		if !strings.Contains(r, want) {
			t.Errorf("recording lacks %q: %s", want, r)
		}
	}
	r = RecordingPipeline(p.Recordings[1], p.Output, "ch", false, "", "software", "/r/b.mkv")
	if !strings.Contains(r, "x265enc bitrate=9000") || !strings.Contains(r, "matroskamux") {
		t.Errorf("h265 mkv: %s", r)
	}
	seg := project.Recording{Path: "x", SegmentS: 60, SegmentMB: 500}
	r = RecordingPipeline(seg, p.Output, "ch", false, "ach", "software", "/r/s.mp4")
	for _, want := range []string{"splitmuxsink", "muxer-factory=mp4mux", `muxer-properties="properties,fragment-duration=1000,streamable=true"`,
		`location="/r/s-%03d.mp4"`, "max-size-time=60000000000", "max-size-bytes=500000000", "start-index=1", "voaacenc bitrate=160000", "mux.audio_%u"} {
		if !strings.Contains(r, want) {
			t.Errorf("segmented lacks %q: %s", want, r)
		}
	}
	if pv := PreviewPipeline(p.Output, "ch", true); !strings.Contains(pv, "width=320,height=180") || !strings.Contains(pv, "appsink name=jpeg") {
		t.Errorf("preview: %s", pv)
	}
}

// A mix moves boxes linearly, keeping the target's order.
func TestLerp(t *testing.T) {
	a, b := PadProps{0, 0, 100, 100, 1, 1}, PadProps{100, 50, 50, 50, 3, 0}
	if m := Lerp(a, b, 0.5); m != (PadProps{50, 25, 75, 75, 3, 0.5}) {
		t.Errorf("%+v", m)
	}
	if Lerp(a, b, 1.5) != b || Lerp(a, b, -1).X != 0 {
		t.Error("clamped")
	}
}

// A recording never overwrites: -2, -3 … are added.
func TestFreePath(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/a.mp4"
	if FreePath(p, false) != p {
		t.Fatal("free")
	}
	os.WriteFile(p, nil, 0o644)
	if got := FreePath(p, false); got != dir+"/a-2.mp4" {
		t.Error(got)
	}
	os.WriteFile(dir+"/s-001.mp4", nil, 0o644)
	if got := FreePath(dir+"/s.mp4", true); got != dir+"/s-2.mp4" {
		t.Error(got)
	}
}

func TestRecordingPath(t *testing.T) {
	p := testProject()
	at := time.Date(2026, 11, 18, 9, 5, 7, 0, time.UTC)
	if got := RecordingPath(p.Recordings[0], p, "", "/show", at); got != "/show/rec/My-Show-2026-11-18.mp4" {
		t.Errorf("%s", got)
	}
	if got := RecordingPath(project.Recording{Path: "/abs/{feed}-{time}.mov"}, p, "cam 1", "/show", at); got != "/abs/cam-1-09-05-07.mov" {
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

func TestNewSources(t *testing.T) {
	o := project.Output{Width: 1280, Height: 720, FPS: 30}
	for _, c := range []struct {
		f    project.Feed
		want []string
	}{
		{project.Feed{Type: "camera"}, []string{`v4l2src device="/dev/video0" ! decodebin`}},
		{project.Feed{Type: "camera", Device: "/dev/video2", Width: 1920, Height: 1080, FPS: 30},
			[]string{`capsfilter caps="video/x-raw,width=1920,height=1080,framerate=30/1;image/jpeg,width=1920,height=1080,framerate=30/1"`}},
		{project.Feed{Type: "stream", URL: "srt://cam:9000?mode=caller"}, []string{`uridecodebin name=dec uri="srt://cam:9000?mode=caller&latency=200"`, `interaudiosink async=false channel="a"`}},
		{project.Feed{Type: "stream", URL: "rtsp://cam/live", LatencyMS: 80}, []string{`uri="rtsp://cam/live"`}},
		{project.Feed{Type: "audio", Pattern: "tone"}, []string{"audiotestsrc is-live=true wave=sine", `interaudiosink channel="a"`}},
		{project.Feed{Type: "audio", Device: "alsa_input.x"}, []string{`pulsesrc device="alsa_input.x"`}},
		{project.Feed{Type: "file", Path: "x.mp4"}, []string{`dec. ! audio/x-raw ! queue ! audioconvert ! audioresample ! audio/x-raw,format=F32LE`}},
	} {
		got := FeedPipeline(c.f, 0, o, "/s", "v", "a")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%+v lacks %q: %s", c.f, w, got)
			}
		}
	}
	if strings.Contains(FeedPipeline(project.Feed{Type: "file", Path: "x"}, 0, o, "", "v", ""), "interaudiosink") {
		t.Error("sound carried without a channel")
	}
}

func TestAudioMixer(t *testing.T) {
	m := AudioMixerPipeline([]string{"clip", "mic"}, "R")
	for _, w := range []string{"audiomixer name=amix", "level name=lv-program", `interaudiosink channel="bgamix-R-program-audio"`, "wave=silence",
		`interaudiosrc channel="bgamix-R-a-clip"`, "level name=lv-mic", "volume name=vol-mic volume=0"} {
		if !strings.Contains(m, w) {
			t.Errorf("lacks %q", w)
		}
	}
	lv, ok := parseLevel("level, endtime=(guint64)1, rms=(GValueArray)< -9.03, -12.5 >, peak=(GValueArray)< -6.02, -inf >, decay=(GValueArray)< -6, -6 >;")
	if !ok || lv.RMSDB != -9.03 || lv.PeakDB != -6.02 {
		t.Errorf("%+v %v", lv, ok)
	}
	if lv, ok := parseLevel("level, rms=(GValueArray)< -inf, -inf >, peak=(GValueArray)< -inf, -inf >"); !ok || lv.PeakDB != -100 {
		t.Errorf("silence %+v", lv)
	}
}

func TestStreams(t *testing.T) {
	o := project.Output{Width: 1280, Height: 720, FPS: 30}
	rtmp := project.Stream{URL: "rtmp://a.rtmp.youtube.com/live2/", Key: "abcd-1234"}
	p := StreamPipeline(rtmp, o, "R", "software")
	for _, w := range []string{"flvmux name=mux streamable=true", `rtmp2sink name=out location="rtmp://a.rtmp.youtube.com/live2/abcd-1234"`,
		"key-int-max=60", "x264enc bitrate=4100", "voaacenc bitrate=160000", `interaudiosrc channel="bgamix-R-program-audio"`} {
		if !strings.Contains(p, w) {
			t.Errorf("rtmp lacks %q: %s", w, p)
		}
	}
	if d := StreamDisplay(rtmp); strings.Contains(d, "abcd") {
		t.Errorf("the key shows: %s", d)
	}
	srt := project.Stream{URL: "srt://example.org:9000", Codec: "h265", VideoKbps: 3000, LatencyMS: 500}
	p = StreamPipeline(srt, o, "R", "va")
	for _, w := range []string{"mpegtsmux name=mux alignment=7", `srtsink name=out wait-for-connection=false uri="srt://example.org:9000?latency=500"`,
		"video/x-raw,format=NV12 ! vah265enc bitrate=3000 key-int-max=60", "h265parse"} {
		if !strings.Contains(p, w) {
			t.Errorf("srt lacks %q: %s", w, p)
		}
	}
}

// Encoders: software always works; asking for one that is not here falls back with a note.
func TestEncoders(t *testing.T) {
	if testing.Short() {
		t.Skip("runs GStreamer")
	}
	if k, note := ChooseEncoder("software", "h264"); k != "software" || note != "" {
		t.Error(k, note)
	}
	if !EncoderWorks("software", "h264") || !EncoderWorks("software", "h265") {
		t.Error("x264 or x265 does not work")
	}
	for _, e := range Encoders() {
		t.Logf("%s (%s): works %v", e.Kind, e.Element, e.Works)
		if !e.Works {
			if k, note := ChooseEncoder(e.Kind, "h264"); k != "software" || note == "" {
				t.Errorf("%s: %s %q", e.Kind, k, note)
			}
		}
	}
}

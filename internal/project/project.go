// Package project is the .bgamix file: a show as versioned JSON (format_version 1): the output
// picture, the feeds, the layouts that place them, and the recordings. Files are decoded strictly
// (unknown fields are errors) and validated with the path of every problem.
//
// Layout boxes are given as fractions of the output picture (0 to 1), so a project does not
// depend on the output resolution.
package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FormatVersion is the format this package reads and writes.
const FormatVersion = 1

// Project is a .bgamix file.
type Project struct {
	FormatVersion int      `json:"format_version"`
	Name          string   `json:"name"`
	Notes         string   `json:"notes,omitempty"`
	Output        Output   `json:"output"`
	Feeds         []Feed   `json:"feeds"`
	Layouts       []Layout `json:"layouts"`
	StartLayout   string   `json:"start_layout,omitempty"` // default the first layout
	// Transition is the default when switching layouts (a switch may override it).
	Transition *Transition `json:"transition,omitempty"`
	Recordings []Recording `json:"recordings,omitempty"`
	// Streams send the program (picture and sound) to a server.
	Streams []Stream `json:"streams,omitempty"`
	// Overlays are drawn over the program, above every feed, in order (later on top).
	Overlays []Overlay `json:"overlays,omitempty"`
}

// Overlay types.
const (
	OverlayText       = "text"        // a line of text, optionally on a box
	OverlayClock      = "clock"       // the time of day
	OverlayImage      = "image"       // a picture file (PNG transparency kept): a logo, a bug
	OverlayLowerThird = "lower_third" // a name and a title on a bar, for introducing people
)

// Clock formats.
var ClockFormats = map[string]string{
	"time":      "15:04:05",
	"time_hm":   "15:04",
	"date_time": "2006-01-02 15:04:05",
	"date":      "2 January 2006",
}

// Overlay is something drawn over the program: text, a clock, an image or a lower third. It is
// shown when the operator has it on (it starts on unless Hidden) and the layout on air is one
// of Layouts (none listed: every layout); it fades in and out over FadeS.
type Overlay struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"` // text, clock, image, lower_third
	// text: the text; lower_third: its first line (a name)
	Text string `json:"text,omitempty"`
	// lower_third: its second line (a title, a place)
	Subtitle string `json:"subtitle,omitempty"`
	// clock: time (default), time_hm, date_time, date
	Format string `json:"format,omitempty"`
	// image: the file, relative to the project file
	Path string `json:"path,omitempty"`
	// X, Y: the top-left corner, in fractions of the output picture
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// W: an image's width (its height follows its shape), a lower third's bar (default 0.45),
	// in fractions of the output's width
	W float64 `json:"w,omitempty"`
	// Size: the text's height in fractions of the output's height (default 0.05)
	Size float64 `json:"size,omitempty"`
	// Colours, as #rgb, #rrggbb or #rrggbbaa: the text (default white), the box behind it
	// (text and clock: none by default; lower third: #000000c0) and a lower third's accent
	// strip (default #e53935)
	Colour     string `json:"colour,omitempty"`
	Background string `json:"background,omitempty"`
	Accent     string `json:"accent,omitempty"`
	// Opacity 0 to 1 (default 1)
	Opacity *float64 `json:"opacity,omitempty"`
	// Layouts it is shown on (empty: all)
	Layouts []string `json:"layouts,omitempty"`
	// Hidden: it starts off, to be shown by the operator
	Hidden bool `json:"hidden,omitempty"`
	// FadeS: how long it fades in and out (default 0.3 s)
	FadeS float64 `json:"fade_s,omitempty"`
}

// DefaultFadeS is an overlay's fade when none is given.
const DefaultFadeS = 0.3

// Stream sends the program to a streaming server or another machine.
type Stream struct {
	Name string `json:"name,omitempty"`
	// URL: rtmp:// or rtmps:// (YouTube, Twitch, Facebook …; FLV with H.264 and AAC), or
	// srt://host:port (MPEG-TS; ?mode=listener to wait for the receiver).
	URL string `json:"url"`
	// Key is the stream key, added to an RTMP URL (kept out of the status and logs).
	Key       string `json:"key,omitempty"`
	VideoKbps int    `json:"video_kbps,omitempty"` // default by resolution, as recordings
	AudioKbps int    `json:"audio_kbps,omitempty"` // default 160
	Codec     string `json:"codec,omitempty"`      // h264 (default); h265 over SRT only
	Encoder   string `json:"encoder,omitempty"`
	Manual    bool   `json:"manual,omitempty"`     // waits to be started
	LatencyMS int    `json:"latency_ms,omitempty"` // SRT (default 200 ms)
}

// Transitions between layouts.
const (
	TransitionCut = "cut" // at once
	TransitionMix = "mix" // boxes move, resize and fade from one layout to the other
)

// Transition is how one layout becomes the next.
type Transition struct {
	Kind      string  `json:"kind"`                 // cut, mix
	DurationS float64 `json:"duration_s,omitempty"` // mix: default 0.5 s
}

// DefaultMixS is a mix's duration when none is given.
const DefaultMixS = 0.5

// Output is the mixed picture.
type Output struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	FPS    int `json:"fps"`
}

// Feed types.
const (
	FeedTest     = "test"     // a generated test pattern
	FeedFile     = "file"     // a video file
	FeedImage    = "image"    // a still image
	FeedPlaylist = "playlist" // video files played in turn
	FeedCamera   = "camera"   // a camera or capture card (Video4Linux)
	FeedStream   = "stream"   // a network stream: rtsp://, srt://, udp://, rtmp://, http(s)://
	FeedAudio    = "audio"    // sound only: a microphone or line input, or a test tone
)

// Audio modes of a feed.
const (
	AudioFollow = "follow" // heard while the feed is in the layout on air (default)
	AudioOn     = "on"     // always heard
	AudioOff    = "off"    // never heard
)

// HasVideo reports whether a feed type has a picture (and so can be in layouts).
func HasVideo(t string) bool { return t != FeedAudio }

// HasAudio reports whether a feed type may carry sound into the mix.
func HasAudio(t string) bool {
	return t == FeedFile || t == FeedPlaylist || t == FeedStream || t == FeedAudio
}

// Stream URL schemes a stream feed takes.
var StreamSchemes = []string{"rtsp", "rtsps", "srt", "udp", "rtmp", "rtmps", "http", "https"}

// Encoders that recordings and streams may ask for ("" or auto: the first that works of the
// hardware encoders, else x264/x265).
var Encoders = []string{"auto", "software", "va", "vaapi", "nvenc", "qsv"}

// End behaviours of a file feed when it reaches its end (or its out point).
const (
	EndLoop  = "loop"  // start again from the in point (default)
	EndHold  = "hold"  // keep showing the last frame
	EndBlack = "black" // show black
	// EndReturn holds the last frame and switches back to the layout shown before the current
	// one (a clip that runs once and returns to the cameras).
	EndReturn = "return"
)

// Feed is one source.
type Feed struct {
	ID   string `json:"id"`   // letters, digits, - and _; referenced by layouts
	Name string `json:"name"` // shown in the UI
	Type string `json:"type"` // test, file, image, playlist, camera, stream, audio
	// test: a GStreamer videotestsrc pattern (default "smpte"); audio: "tone" for a 440 Hz test
	// tone instead of a device
	Pattern string `json:"pattern,omitempty"`
	// camera: the device (default /dev/video0); audio: the input device (PulseAudio/PipeWire
	// source name; default the system's input)
	Device string `json:"device,omitempty"`
	// camera: the picture asked of the device (0: its default)
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	FPS    int `json:"fps,omitempty"`
	// stream: the URL; LatencyMS is the buffer for rtsp and srt (default 200 ms)
	URL       string `json:"url,omitempty"`
	LatencyMS int    `json:"latency_ms,omitempty"`
	// Audio: follow (default), on or off; VolumeDB its level (0 dB: as it is; -60 to +12);
	// Mute starts it muted.
	Audio    string  `json:"audio,omitempty"`
	VolumeDB float64 `json:"volume_db,omitempty"`
	Mute     bool    `json:"mute,omitempty"`
	// file, image: relative to the project file
	Path string `json:"path,omitempty"`
	// file
	InS  float64 `json:"in_s,omitempty"`  // start here (seconds)
	OutS float64 `json:"out_s,omitempty"` // stop here; 0: the end of the file
	// End: what a file does at its end, or a playlist after its last item: loop (default),
	// hold, black, return.
	End string `json:"end,omitempty"`
	// playlist: the files, played in order (relative to the project file)
	Items []string `json:"items,omitempty"`
	// ISO records this feed on its own, clean (before mixing), as well as the program. Its path
	// may use {feed}.
	ISO *Recording `json:"iso,omitempty"`
}

// Layout places feeds on the output.
type Layout struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Boxes []Box  `json:"boxes"` // drawn in order: later boxes on top
}

// Box is a feed's place in a layout, in fractions of the output picture.
type Box struct {
	Feed  string   `json:"feed"`
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	W     float64  `json:"w"`
	H     float64  `json:"h"`
	Alpha *float64 `json:"alpha,omitempty"` // 0 to 1; default 1
}

// Recording writes the program (the mixed output) to a file.
type Recording struct {
	// Path is relative to the project file; {project}, {date} (2006-01-02) and {time}
	// (15-04-05) are replaced when the recording starts. The extension follows the container
	// when the path has none.
	Path      string `json:"path"`
	Container string `json:"container,omitempty"` // mp4 (default; fragmented, crash-safe), mkv, mov
	Codec     string `json:"codec,omitempty"`     // h264 (default), h265
	// BitrateKbps is the video bitrate; default by resolution (data in the engine).
	BitrateKbps int `json:"bitrate_kbps,omitempty"`
	// Manual: the recording waits to be started (otherwise it starts with the show).
	Manual bool `json:"manual,omitempty"`
	// Encoder: auto (default), software, va, vaapi, nvenc or qsv.
	Encoder string `json:"encoder,omitempty"`
	// NoAudio leaves the sound out; AudioKbps is the AAC bitrate (default 160).
	NoAudio   bool `json:"no_audio,omitempty"`
	AudioKbps int  `json:"audio_kbps,omitempty"`
	// Segments: a new file every SegmentS seconds or SegmentMB megabytes (0: no limit), named
	// with -001, -002 … before the extension.
	SegmentS  float64 `json:"segment_s,omitempty"`
	SegmentMB float64 `json:"segment_mb,omitempty"`
}

// Problems is a list of validation problems, each with its path.
type Problems []string

func (p Problems) Error() string {
	return fmt.Sprintf("%d problem(s):\n  %s", len(p), strings.Join(p, "\n  "))
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// Patterns are the test patterns offered (GStreamer videotestsrc nicks).
var Patterns = []string{"smpte", "snow", "black", "white", "red", "green", "blue", "checkers-8",
	"circular", "blink", "smpte75", "zone-plate", "gamut", "ball", "smpte100", "bar", "pinwheel",
	"spokes", "gradient", "colors"}

// Validate checks the project; dir is the project's folder (to check that files exist; "" skips
// that).
func (p *Project) Validate(dir string) error {
	var ps Problems
	add := func(path, f string, a ...any) { ps = append(ps, path+": "+fmt.Sprintf(f, a...)) }
	if p.FormatVersion != FormatVersion {
		add("format_version", "must be %d", FormatVersion)
	}
	o := p.Output
	if o.Width < 16 || o.Width > 7680 || o.Width%2 != 0 {
		add("output.width", "an even number of pixels, 16 to 7680")
	}
	if o.Height < 16 || o.Height > 4320 || o.Height%2 != 0 {
		add("output.height", "an even number of pixels, 16 to 4320")
	}
	if o.FPS < 1 || o.FPS > 120 {
		add("output.fps", "1 to 120 frames per second")
	}
	feeds := map[string]bool{}
	for i, f := range p.Feeds {
		at := fmt.Sprintf("feeds[%d]", i)
		if !idRe.MatchString(f.ID) {
			add(at+".id", "letters, digits, - and _, up to 40 characters, starting with a letter or digit")
		} else if feeds[f.ID] {
			add(at+".id", "%q is used by another feed", f.ID)
		}
		feeds[f.ID] = true
		switch f.Type {
		case FeedTest:
			if f.Pattern != "" && !contains(Patterns, f.Pattern) {
				add(at+".pattern", "one of %s", strings.Join(Patterns, ", "))
			}
		case FeedPlaylist:
			if len(f.Items) == 0 {
				add(at+".items", "give at least one file")
			}
			for k, it := range f.Items {
				if strings.TrimSpace(it) == "" {
					add(fmt.Sprintf("%s.items[%d]", at, k), "empty path")
				} else if dir != "" {
					if _, err := os.Stat(resolve(dir, it)); err != nil {
						add(fmt.Sprintf("%s.items[%d]", at, k), "%v", err)
					}
				}
			}
			if !validEnd(f.End) {
				add(at+".end", "loop, hold, black or return")
			}
		case FeedFile, FeedImage:
			if f.Path == "" {
				add(at+".path", "give the file")
			} else if dir != "" {
				if _, err := os.Stat(resolve(dir, f.Path)); err != nil {
					add(at+".path", "%v", err)
				}
			}
			if f.Type == FeedFile {
				if f.InS < 0 || f.OutS < 0 || (f.OutS > 0 && f.OutS <= f.InS) {
					add(at, "in_s and out_s must not be negative, and out_s (0: the end) must come after in_s")
				}
				if !validEnd(f.End) {
					add(at+".end", "loop, hold, black or return")
				}
			}
		case FeedCamera:
			if f.Width < 0 || f.Height < 0 || f.FPS < 0 || f.Width > 7680 || f.Height > 4320 || f.FPS > 240 {
				add(at, "width, height and fps: 0 (the camera's default) or a size and rate the camera offers")
			}
		case FeedStream:
			if err := checkURL(f.URL, StreamSchemes); err != "" {
				add(at+".url", "%s", err)
			}
			if f.LatencyMS < 0 || f.LatencyMS > 10000 {
				add(at+".latency_ms", "0 to 10000")
			}
		case FeedAudio:
			if f.Pattern != "" && f.Pattern != "tone" {
				add(at+".pattern", `"tone" or nothing (a device)`)
			}
		default:
			add(at+".type", "test, file, image, playlist, camera, stream or audio")
		}
		if f.Audio != "" && f.Audio != AudioFollow && f.Audio != AudioOn && f.Audio != AudioOff {
			add(at+".audio", "follow, on or off")
		}
		if f.VolumeDB < -60 || f.VolumeDB > 12 {
			add(at+".volume_db", "-60 to +12 dB")
		}
		if f.ISO != nil {
			validateRecording(f.ISO, at+".iso", add)
		}
	}
	if len(p.Feeds) == 0 {
		add("feeds", "add at least one feed")
	}
	layouts := map[string]bool{}
	for i, l := range p.Layouts {
		at := fmt.Sprintf("layouts[%d]", i)
		if !idRe.MatchString(l.ID) {
			add(at+".id", "letters, digits, - and _, up to 40 characters")
		} else if layouts[l.ID] {
			add(at+".id", "%q is used by another layout", l.ID)
		}
		layouts[l.ID] = true
		seen := map[string]bool{}
		for j, b := range l.Boxes {
			bt := fmt.Sprintf("%s.boxes[%d]", at, j)
			if !feeds[b.Feed] {
				add(bt+".feed", "no feed %q", b.Feed)
			} else if f := p.Feed(b.Feed); f != nil && !HasVideo(f.Type) {
				add(bt+".feed", "%q is sound only: it has no picture to place", b.Feed)
			} else if seen[b.Feed] {
				add(bt+".feed", "feed %q is already in this layout (a feed appears once per layout)", b.Feed)
			}
			seen[b.Feed] = true
			if b.W <= 0 || b.H <= 0 || b.W > 1 || b.H > 1 {
				add(bt, "w and h must be above 0 and at most 1 (fractions of the output)")
			}
			if b.X < -1 || b.X > 1 || b.Y < -1 || b.Y > 1 {
				add(bt, "x and y must be within -1 and 1 (fractions of the output)")
			}
			if b.Alpha != nil && (*b.Alpha < 0 || *b.Alpha > 1) {
				add(bt+".alpha", "0 to 1")
			}
		}
	}
	if len(p.Layouts) == 0 {
		add("layouts", "add at least one layout")
	}
	if p.StartLayout != "" && !layouts[p.StartLayout] {
		add("start_layout", "no layout %q", p.StartLayout)
	}
	if t := p.Transition; t != nil {
		if t.Kind != TransitionCut && t.Kind != TransitionMix {
			add("transition.kind", "cut or mix")
		}
		if t.DurationS < 0 || t.DurationS > 60 {
			add("transition.duration_s", "0 to 60 s")
		}
	}
	for i := range p.Recordings {
		validateRecording(&p.Recordings[i], fmt.Sprintf("recordings[%d]", i), add)
	}
	for i, st := range p.Streams {
		at := fmt.Sprintf("streams[%d]", i)
		if err := checkURL(st.URL, []string{"rtmp", "rtmps", "srt"}); err != "" {
			add(at+".url", "%s", err)
		}
		if st.VideoKbps < 0 || st.VideoKbps > 200000 {
			add(at+".video_kbps", "0 (automatic) to 200000")
		}
		if st.AudioKbps < 0 || st.AudioKbps > 512 {
			add(at+".audio_kbps", "0 (160) to 512")
		}
		if st.Codec != "" && st.Codec != "h264" && st.Codec != "h265" {
			add(at+".codec", "h264 or h265")
		} else if st.Codec == "h265" && strings.HasPrefix(st.URL, "rtmp") {
			add(at+".codec", "RTMP carries H.264 only")
		}
		if st.Encoder != "" && !contains(Encoders, st.Encoder) {
			add(at+".encoder", "one of %s", strings.Join(Encoders, ", "))
		}
		if st.LatencyMS < 0 || st.LatencyMS > 10000 {
			add(at+".latency_ms", "0 to 10000")
		}
	}
	ovs := map[string]bool{}
	for i, ov := range p.Overlays {
		at := fmt.Sprintf("overlays[%d]", i)
		if !idRe.MatchString(ov.ID) {
			add(at+".id", "letters, digits, - and _, up to 40 characters")
		} else if ovs[ov.ID] {
			add(at+".id", "%q is used by another overlay", ov.ID)
		}
		ovs[ov.ID] = true
		switch ov.Type {
		case OverlayText:
			if strings.TrimSpace(ov.Text) == "" {
				add(at+".text", "give the text")
			}
		case OverlayLowerThird:
			if strings.TrimSpace(ov.Text) == "" && strings.TrimSpace(ov.Subtitle) == "" {
				add(at+".text", "give a name or a title")
			}
		case OverlayClock:
			if _, ok := ClockFormats[ov.Format]; ov.Format != "" && !ok {
				add(at+".format", "time, time_hm, date_time or date")
			}
		case OverlayImage:
			if ov.Path == "" {
				add(at+".path", "give the image")
			} else if dir != "" {
				if _, err := os.Stat(resolve(dir, ov.Path)); err != nil {
					add(at+".path", "%v", err)
				}
			}
		default:
			add(at+".type", "text, clock, image or lower_third")
		}
		if ov.X < 0 || ov.X >= 1 || ov.Y < 0 || ov.Y >= 1 {
			add(at, "x and y must be from 0 to below 1 (fractions of the output)")
		}
		if ov.W < 0 || ov.W > 1 {
			add(at+".w", "0 (the default) to 1")
		}
		if ov.Size < 0 || ov.Size > 0.5 {
			add(at+".size", "0 (the default, 0.05) to 0.5 of the picture's height")
		}
		for k, c := range map[string]string{"colour": ov.Colour, "background": ov.Background, "accent": ov.Accent} {
			if _, ok := ParseColour(c); c != "" && !ok {
				add(at+"."+k, "#rgb, #rrggbb or #rrggbbaa")
			}
		}
		if ov.Opacity != nil && (*ov.Opacity < 0 || *ov.Opacity > 1) {
			add(at+".opacity", "0 to 1")
		}
		if ov.FadeS < 0 || ov.FadeS > 10 {
			add(at+".fade_s", "0 to 10 s")
		}
		for k, l := range ov.Layouts {
			if !layouts[l] {
				add(fmt.Sprintf("%s.layouts[%d]", at, k), "no layout %q", l)
			}
		}
	}
	if len(ps) > 0 {
		return ps
	}
	return nil
}

// ParseColour reads #rgb, #rrggbb or #rrggbbaa.
func ParseColour(s string) (color.NRGBA, bool) {
	h := strings.TrimPrefix(s, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) == 6 {
		h += "ff"
	}
	if len(h) != 8 || !strings.HasPrefix(s, "#") {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{uint8(v >> 24), uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

func validEnd(e string) bool {
	return e == "" || e == EndLoop || e == EndHold || e == EndBlack || e == EndReturn
}

func validateRecording(r *Recording, at string, add func(string, string, ...any)) {
	if strings.TrimSpace(r.Path) == "" {
		add(at+".path", "give the file")
	}
	if r.Container != "" && r.Container != "mp4" && r.Container != "mkv" && r.Container != "mov" {
		add(at+".container", "mp4, mkv or mov")
	}
	if r.Codec != "" && r.Codec != "h264" && r.Codec != "h265" {
		add(at+".codec", "h264 or h265")
	}
	if r.BitrateKbps < 0 || r.BitrateKbps > 200000 {
		add(at+".bitrate_kbps", "0 (automatic) to 200000")
	}
	if r.SegmentS < 0 || (r.SegmentS > 0 && r.SegmentS < 2) {
		add(at+".segment_s", "0 (no limit) or at least 2 s")
	}
	if r.SegmentMB < 0 || (r.SegmentMB > 0 && r.SegmentMB < 1) {
		add(at+".segment_mb", "0 (no limit) or at least 1 MB")
	}
	if r.Encoder != "" && !contains(Encoders, r.Encoder) {
		add(at+".encoder", "one of %s", strings.Join(Encoders, ", "))
	}
	if r.AudioKbps < 0 || r.AudioKbps > 512 {
		add(at+".audio_kbps", "0 (160) to 512")
	}
}

// Layout returns a layout by id (nil when there is none).
func (p *Project) Layout(id string) *Layout {
	for i := range p.Layouts {
		if p.Layouts[i].ID == id {
			return &p.Layouts[i]
		}
	}
	return nil
}

// Feed returns a feed by id (nil when there is none).
func (p *Project) Feed(id string) *Feed {
	for i := range p.Feeds {
		if p.Feeds[i].ID == id {
			return &p.Feeds[i]
		}
	}
	return nil
}

// checkURL checks a URL's form and scheme ("" when good).
func checkURL(raw string, schemes []string) string {
	if strings.TrimSpace(raw) == "" {
		return "give the URL"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err.Error()
	}
	if !contains(schemes, strings.ToLower(u.Scheme)) {
		return "the URL must start with " + strings.Join(schemes, "://, ") + "://"
	}
	if u.Host == "" {
		return "the URL has no host or port (" + u.Scheme + "://host:port…)"
	}
	return ""
}

// AudioMode is a feed's audio mode with its default.
func (f *Feed) AudioMode() string {
	if !HasAudio(f.Type) {
		return AudioOff
	}
	if f.Audio == "" {
		if f.Type == FeedAudio {
			return AudioOn
		}
		return AudioFollow
	}
	return f.Audio
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// Resolve makes a path in the project absolute against the project's folder.
func Resolve(dir, p string) string { return resolve(dir, p) }

// Start returns the layout the show starts with.
func (p *Project) Start() *Layout {
	for i := range p.Layouts {
		if p.Layouts[i].ID == p.StartLayout {
			return &p.Layouts[i]
		}
	}
	if len(p.Layouts) > 0 {
		return &p.Layouts[0]
	}
	return nil
}

// Read decodes a project strictly and validates it (dir as for Validate).
func Read(r io.Reader, dir string) (*Project, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var p Project
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("more than one JSON value in the file")
	}
	if err := p.Validate(dir); err != nil {
		return nil, err
	}
	return &p, nil
}

// Load reads a project file (files it names are checked against its folder).
func Load(path string) (*Project, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := Read(f, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Write encodes the project, indented.
func (p *Project) Write(w io.Writer) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Save writes the project to a file.
func (p *Project) Save(path string) error {
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// FeedIDs lists the feeds' ids, sorted.
func (p *Project) FeedIDs() []string {
	var out []string
	for _, f := range p.Feeds {
		out = append(out, f.ID)
	}
	sort.Strings(out)
	return out
}

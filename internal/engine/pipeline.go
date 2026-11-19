// Package engine runs a show on GStreamer. Every part of a show is its own pipeline, joined by
// intervideo channels:
//
//	feed pipelines ──► channel per feed ──► mixer (compositor, placed by the layout) ──► program channel
//	                         │                                                              │
//	                         └──► clean ISO recordings, previews        recordings, previews ◄┘
//
// so a feed that stalls or fails never stops the mix (it is rebuilt by its supervisor), and a
// recording starts, pauses and stops on its own. A channel may have several readers.
//
// The pipelines are described in GStreamer's launch syntax: the builders here are plain string
// functions (tested without GStreamer), and engine.go runs them.
package engine

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/biggaeroza/bga-mix/internal/project"
)

// Every feed is normalised to the output's size, frame rate, raw format and colorimetry before it
// reaches the mixer: the intervideo channel passes frames as they are, so both ends must agree
// exactly. The colorimetry is pinned too (BT.709, the HD standard): left free, sources with
// different colour metadata (a test pattern, a decoded file, an image) made a mixer input
// discard a feed's frames (it showed black), and changed the program's caps mid-stream, which
// made the MP4 muxer rewrite its header over the data after it (an unplayable recording).
const (
	rawFormat   = "I420"
	colorimetry = "bt709"
)

// bitsPerPixel sizes the default recording bitrate: about 0.15 bit per pixel per frame for
// H.264 at good quality (an assumption: 1280×720 at 30 fps gives about 4.1 Mbit/s, within the
// 3.5-6 Mbit/s commonly recommended for 720p30 live encodes). H.265 takes 60 % of it.
const bitsPerPixel = 0.15

// holdTimeout keeps an intervideosrc showing its last frame (about 31 years); its default
// (1 s) turns a feed that stops into black.
const holdTimeout = uint64(1) << 60

// Previews are small JPEGs of the program and each feed, for the UI (the program's larger: it
// is shown large).
const (
	PreviewWidth        = 320
	ProgramPreviewWidth = 640
	PreviewFPS          = 5
)

func caps(o project.Output) string {
	return fmt.Sprintf("video/x-raw,format=%s,width=%d,height=%d,pixel-aspect-ratio=1/1,framerate=%d/1,colorimetry=%s,chroma-site=mpeg2,interlace-mode=progressive",
		rawFormat, o.Width, o.Height, o.FPS, colorimetry)
}

// quote makes a launch-syntax string value.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// FeedChannel and ProgramChannel name a run's intervideo channels, AudioChannel and
// ProgramAudioChannel its interaudio channels.
func FeedChannel(runID, feedID string) string  { return "bgamix-" + runID + "-f-" + feedID }
func ProgramChannel(runID string) string       { return "bgamix-" + runID + "-program" }
func AudioChannel(runID, feedID string) string { return "bgamix-" + runID + "-a-" + feedID }
func ProgramAudioChannel(runID string) string  { return "bgamix-" + runID + "-program-audio" }

// Sound is mixed as 48 kHz stereo 32-bit float, the format every audio branch is brought to.
const audioCaps = "audio/x-raw,format=F32LE,layout=interleaved,rate=48000,channels=2"

// levelInterval is how often the meters are measured (ns).
const levelInterval = 100_000_000

// DefaultAudioKbps is the AAC bitrate of recordings and streams.
const DefaultAudioKbps = 160

// streamGOP is a stream's key frame interval in seconds (services ask for 2 to 4 s).
const streamGOP = 2

// DefaultLatencyMS is the receive buffer of rtsp and srt sources and srt outputs.
const DefaultLatencyMS = 200

// Held reports whether a feed's channel keeps its last frame when the feed stops sending (files,
// playlists and images: paused, ended or between playlist items); a test pattern that stops
// goes black after a second.
func Held(f project.Feed) bool {
	return f.Type == project.FeedFile || f.Type == project.FeedPlaylist || f.Type == project.FeedImage
}

// FeedPipeline describes a feed's pipeline: its source (for a playlist, its item'th file),
// normalised to the output (scaled to fit with borders, the output's frame rate and format),
// into its intervideo channel; with achannel, its sound (48 kHz stereo) into that interaudio
// channel. The audio sink does not wait for sound (async=false): a file may have none.
func FeedPipeline(f project.Feed, item int, o project.Output, dir, channel, achannel string) string {
	norm := "videoconvert ! videoscale add-borders=true ! videorate ! " + caps(o)
	sink := "intervideosink channel=" + quote(channel)
	audio := ""
	if achannel != "" {
		audio = " dec. ! audio/x-raw ! queue ! audioconvert ! audioresample ! " + audioCaps + " ! interaudiosink async=false channel=" + quote(achannel)
	}
	file := func(path string) string {
		// show-preroll-frame=false: the frame decoded while the file is paused before its seek
		// to the in point would otherwise reach the mix (a flash of the file's start)
		// (it is turned on again after that seek, so that seeking a paused file shows the frame)
		sink := "intervideosink name=out show-preroll-frame=false channel=" + quote(channel)
		return fmt.Sprintf("filesrc location=%s ! decodebin name=dec dec. ! video/x-raw ! queue ! %s ! %s%s", quote(project.Resolve(dir, path)), norm, sink, audio)
	}
	switch f.Type {
	case project.FeedTest:
		pat := f.Pattern
		if pat == "" {
			pat = "smpte"
		}
		return fmt.Sprintf("videotestsrc is-live=true pattern=%s ! %s ! %s", pat, norm, sink)
	case project.FeedFile:
		return file(f.Path)
	case project.FeedPlaylist:
		return file(f.Items[item])
	case project.FeedImage:
		return fmt.Sprintf("filesrc location=%s ! decodebin ! imagefreeze is-live=true ! %s ! %s",
			quote(project.Resolve(dir, f.Path)), norm, sink)
	case project.FeedCamera:
		dev := f.Device
		if dev == "" {
			dev = "/dev/video0"
		}
		want := ""
		if f.Width > 0 && f.Height > 0 {
			c := fmt.Sprintf(",width=%d,height=%d", f.Width, f.Height)
			if f.FPS > 0 {
				c += fmt.Sprintf(",framerate=%d/1", f.FPS)
			}
			// raw or MJPEG, whichever the camera gives at that size
			want = fmt.Sprintf(` ! capsfilter caps="video/x-raw%s;image/jpeg%s"`, c, c)
		}
		return fmt.Sprintf("v4l2src device=%s%s ! decodebin ! queue ! %s ! %s", quote(dev), want, norm, sink)
	case project.FeedStream:
		return fmt.Sprintf("uridecodebin name=dec uri=%s dec. ! video/x-raw ! queue ! %s ! %s%s", quote(StreamURI(f)), norm, sink, audio)
	case project.FeedAudio:
		src := "pulsesrc"
		if f.Pattern == "tone" {
			src = "audiotestsrc is-live=true wave=sine freq=440 volume=0.25"
		} else if f.Device != "" {
			src += " device=" + quote(f.Device)
		}
		return fmt.Sprintf("%s ! queue ! audioconvert ! audioresample ! %s ! interaudiosink channel=%s", src, audioCaps, quote(achannel))
	}
	return ""
}

// StreamURI is a stream feed's URI; an SRT URL gets its latency as a parameter (srtsrc reads it
// there; an RTSP source gets it when uridecodebin makes it).
func StreamURI(f project.Feed) string {
	u := f.URL
	if strings.HasPrefix(strings.ToLower(u), "srt://") && !strings.Contains(u, "latency=") {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += fmt.Sprintf("%slatency=%d", sep, latency(f.LatencyMS))
	}
	return u
}

func latency(ms int) int {
	if ms <= 0 {
		return DefaultLatencyMS
	}
	return ms
}

// PadProps are a compositor pad's placement.
type PadProps struct {
	X, Y, W, H, Z int
	Alpha         float64
}

// Place computes each feed's pad placement in a layout: feeds the layout does not show are
// transparent, full size at the bottom. Boxes are drawn in layout order (later on top).
func Place(p *project.Project, l *project.Layout) map[string]PadProps {
	o := p.Output
	out := map[string]PadProps{}
	for _, f := range p.Feeds {
		if project.HasVideo(f.Type) {
			out[f.ID] = PadProps{W: o.Width, H: o.Height, Alpha: 0}
		}
	}
	if l == nil {
		return out
	}
	for i, b := range l.Boxes {
		a := 1.0
		if b.Alpha != nil {
			a = *b.Alpha
		}
		out[b.Feed] = PadProps{
			X: int(math.Round(b.X * float64(o.Width))), Y: int(math.Round(b.Y * float64(o.Height))),
			W: even(b.W * float64(o.Width)), H: even(b.H * float64(o.Height)), Z: i + 1, Alpha: a,
		}
	}
	return out
}

func even(v float64) int { return max(2, int(math.Round(v/2))*2) }

// Lerp is the placement a fraction t (0 to 1) of the way from a to b: position, size and alpha
// move linearly; the stacking order is b's throughout (the incoming boxes fade in on top).
func Lerp(a, b PadProps, t float64) PadProps {
	t = math.Max(0, math.Min(1, t))
	l := func(x, y int) int { return int(math.Round(float64(x) + t*float64(y-x))) }
	return PadProps{X: l(a.X, b.X), Y: l(a.Y, b.Y), W: max(2, l(a.W, b.W)), H: max(2, l(a.H, b.H)), Z: b.Z, Alpha: a.Alpha + t*(b.Alpha-a.Alpha)}
}

// VideoFeeds are the feeds with a picture, in project order: the mixer's inputs sink_0, sink_1 …
func VideoFeeds(p *project.Project) []project.Feed {
	var out []project.Feed
	for _, f := range p.Feeds {
		if project.HasVideo(f.Type) {
			out = append(out, f)
		}
	}
	return out
}

// MixerPipeline describes the mixer: an intervideosrc per feed with a picture (in project
// order) into the compositor, placed by the layout, then the overlays, into the program channel. The compositor
// takes its start time from its first input (otherwise its first frames carry no timestamps).
func MixerPipeline(p *project.Project, l *project.Layout, runID string, ovs []OverlayElement) string {
	o := p.Output
	place := Place(p, l)
	vf := VideoFeeds(p)
	var b strings.Builder
	b.WriteString("compositor name=mix start-time-selection=first background=black")
	for i, f := range vf {
		pp := place[f.ID]
		fmt.Fprintf(&b, " sink_%d::xpos=%d sink_%d::ypos=%d sink_%d::width=%d sink_%d::height=%d sink_%d::zorder=%d sink_%d::alpha=%g",
			i, pp.X, i, pp.Y, i, pp.W, i, pp.H, i, pp.Z, i, pp.Alpha)
	}
	fmt.Fprintf(&b, " ! %s%s ! intervideosink channel=%s", caps(o), overlayChain(ovs), quote(ProgramChannel(runID)))
	for i, f := range vf {
		fmt.Fprintf(&b, " %s ! queue ! mix.sink_%d", channelSource(FeedChannel(runID, f.ID), Held(f), o), i)
	}
	return b.String()
}

// AudioMixerPipeline describes the sound mixer: each feed's channel through a meter (before its
// fader: it shows what comes in, even while faded out) and its fader (volume, starting silent)
// into the mixer, with a silent input so that the mix runs with no sound at all; the program's
// meter; into the program's audio channel. Elements are named lv-<feed>, vol-<feed> and
// lv-program.
func AudioMixerPipeline(feeds []string, runID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "audiomixer name=amix start-time-selection=first ! %s ! level name=lv-program interval=%d ! interaudiosink channel=%s",
		audioCaps, levelInterval, quote(ProgramAudioChannel(runID)))
	fmt.Fprintf(&b, " audiotestsrc is-live=true wave=silence ! %s ! amix.", audioCaps)
	for _, id := range feeds {
		fmt.Fprintf(&b, " interaudiosrc channel=%s ! %s ! level name=lv-%s interval=%d ! volume name=vol-%s volume=0 ! queue ! amix.",
			quote(AudioChannel(runID, id)), audioCaps, id, levelInterval, id)
	}
	return b.String()
}

func channelSource(channel string, held bool, o project.Output) string {
	timeout := ""
	if held {
		timeout = fmt.Sprintf(" timeout=%d", holdTimeout)
	}
	return fmt.Sprintf("intervideosrc channel=%s%s ! %s", quote(channel), timeout, caps(o))
}

// RecordingPath expands a recording's path ({project}, {feed}, {date}, {time}) against the
// project's folder, adding the container's extension when the path has none.
func RecordingPath(r project.Recording, p *project.Project, feed, dir string, at time.Time) string {
	name := strings.NewReplacer("{project}", safeName(p.Name), "{feed}", safeName(feed), "{date}", at.Format("2006-01-02"),
		"{time}", at.Format("15-04-05")).Replace(r.Path)
	if filepath.Ext(name) == "" {
		name += "." + container(r)
	}
	return project.Resolve(dir, name)
}

// FreePath returns path, or path with -2, -3 … before its extension when the file (or its
// first segment) already exists, so that a recording never overwrites an earlier one.
func FreePath(path string, segmented bool) string {
	exists := func(p string) bool {
		if segmented {
			p = strings.Replace(SegmentPattern(p), "%03d", "001", 1)
		}
		_, err := os.Stat(p)
		return err == nil
	}
	if !exists(path) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for n := 2; ; n++ {
		c := fmt.Sprintf("%s-%d%s", base, n, ext)
		if !exists(c) {
			return c
		}
	}
}

// SegmentPattern is a segmented recording's file pattern (splitmuxsink's location): -%03d before
// the extension, counting from 001.
func SegmentPattern(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "-%03d" + ext
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "show"
	}
	return strings.TrimSuffix(b.String(), "-")
}

func container(r project.Recording) string {
	if r.Container == "" {
		return "mp4"
	}
	return r.Container
}

// Bitrate is a recording's video bitrate in kbit/s.
func Bitrate(r project.Recording, o project.Output) int {
	if r.BitrateKbps > 0 {
		return r.BitrateKbps
	}
	k := bitsPerPixel * float64(o.Width*o.Height*o.FPS) / 1000
	if r.Codec == "h265" {
		k *= 0.6
	}
	return int(math.Round(k/100) * 100)
}

// RecordingPipeline describes a recording of a channel (the program, or a feed for an ISO
// recording), with achannel's sound ("": none) in AAC: encoded (by the encoder kind) and muxed
// into path. MP4 and MOV are fragmented (a fragment and a key frame every second) and
// streamable (the header first), and every buffer is written at once (filesink's default 64 kB
// of in-process buffering emptied a killed low-bitrate recording), so a recording cut short by a
// crash still plays up to its last fragment. With segments, splitmuxsink starts a new file at
// the first key frame after each limit.
func RecordingPipeline(r project.Recording, o project.Output, channel string, held bool, achannel, encKind, path string) string {
	enc := encoderChain(encKind, r.Codec, Bitrate(r, o), o.FPS)
	mux, muxProps := "mp4mux", "fragment-duration=1000 streamable=true"
	switch container(r) {
	case "mkv":
		mux, muxProps = "matroskamux", ""
	case "mov":
		mux = "qtmux"
	}
	src := channelSource(channel, held, o) + " ! queue ! " + enc
	audio := ""
	if achannel != "" {
		ak := r.AudioKbps
		if ak <= 0 {
			ak = DefaultAudioKbps
		}
		audio = fmt.Sprintf(" interaudiosrc channel=%s ! %s ! queue ! audioconvert ! voaacenc bitrate=%d ! aacparse ! queue ! ", quote(achannel), audioCaps, ak*1000)
	}
	if r.SegmentS > 0 || r.SegmentMB > 0 {
		var lim string
		if r.SegmentS > 0 {
			lim += fmt.Sprintf(" max-size-time=%d", int64(r.SegmentS*1e9))
		}
		if r.SegmentMB > 0 {
			lim += fmt.Sprintf(" max-size-bytes=%d", int64(r.SegmentMB*1e6))
		}
		mp := ""
		if muxProps != "" {
			mp = fmt.Sprintf(` muxer-properties="properties,%s"`, strings.ReplaceAll(muxProps, " ", ","))
		}
		if audio != "" {
			audio += "mux.audio_%u"
		}
		return fmt.Sprintf(`%s ! splitmuxsink name=mux muxer-factory=%s%s sink-properties="properties,buffer-mode=unbuffered" start-index=1 location=%s%s%s`,
			src, mux, mp, quote(SegmentPattern(path)), lim, audio)
	}
	if muxProps != "" {
		mux += " " + muxProps
	}
	if audio != "" {
		audio += "mux."
	}
	return fmt.Sprintf("%s ! %s name=mux ! filesink name=out buffer-mode=unbuffered location=%s%s", src, mux, quote(path), audio)
}

// StreamTarget is where a stream goes: an RTMP URL with its key, or the SRT URL with its latency.
func StreamTarget(st project.Stream) string {
	u := st.URL
	if streamKind(u) == "srt" {
		if !strings.Contains(u, "latency=") {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			u += fmt.Sprintf("%slatency=%d", sep, latency(st.LatencyMS))
		}
		return u
	}
	if st.Key != "" {
		u = strings.TrimSuffix(u, "/") + "/" + st.Key
	}
	return u
}

// StreamPipeline describes a stream of the program: picture and sound encoded (H.264 or, over
// SRT, H.265; AAC), a key frame every streamGOP seconds, muxed as FLV to RTMP (rtmp2sink) or
// MPEG-TS to SRT (srtsink, not waiting for a receiver).
func StreamPipeline(st project.Stream, o project.Output, runID, encKind string) string {
	r := project.Recording{BitrateKbps: st.VideoKbps, Codec: st.Codec}
	enc := encoderChain(encKind, st.Codec, Bitrate(r, o), o.FPS*streamGOP)
	ak := st.AudioKbps
	if ak <= 0 {
		ak = DefaultAudioKbps
	}
	mux := "flvmux name=mux streamable=true ! rtmp2sink name=out location=" + quote(StreamTarget(st))
	if streamKind(st.URL) == "srt" {
		mux = "mpegtsmux name=mux alignment=7 ! srtsink name=out wait-for-connection=false uri=" + quote(StreamTarget(st))
	}
	return fmt.Sprintf("%s ! queue ! %s ! queue ! %s interaudiosrc channel=%s ! %s ! queue ! audioconvert ! voaacenc bitrate=%d ! aacparse ! queue ! mux.",
		channelSource(ProgramChannel(runID), false, o), enc, mux, quote(ProgramAudioChannel(runID)), audioCaps, ak*1000)
}

// WindowPipeline shows the program in a window on this computer; ListenPipeline plays the
// program's sound on its speakers.
func WindowPipeline(o project.Output, runID string) string {
	return fmt.Sprintf("%s ! queue ! videoconvert ! autovideosink sync=false", channelSource(ProgramChannel(runID), false, o))
}

func ListenPipeline(runID string) string {
	return fmt.Sprintf("interaudiosrc channel=%s ! %s ! queue ! audioconvert ! audioresample ! autoaudiosink", quote(ProgramAudioChannel(runID)), audioCaps)
}

// PreviewPipeline describes a channel's preview: a few JPEGs a second, PreviewWidth wide, into
// an appsink named "jpeg" that keeps only the latest.
func PreviewPipeline(o project.Output, channel string, held bool, width int) string {
	h := even(float64(width) * float64(o.Height) / float64(o.Width))
	return fmt.Sprintf("%s ! videorate drop-only=true ! video/x-raw,framerate=%d/1 ! videoscale ! video/x-raw,width=%d,height=%d ! videoconvert ! jpegenc quality=75 ! appsink name=jpeg max-buffers=1 drop=true sync=false",
		channelSource(channel, held, o), PreviewFPS, width, h)
}

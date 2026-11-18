// Package engine runs a show on GStreamer. Each feed runs in its own pipeline and hands its
// frames to the mixer through an intervideo channel, so a feed that stalls or fails never stops
// the mix. The mixer pipeline composites one pad per feed (placed by the layout) and tees the
// program to the recordings.
//
// The pipelines are described in GStreamer's launch syntax: the builders here are plain string
// functions (tested without GStreamer), and engine.go runs them.
package engine

import (
	"fmt"
	"math"
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

func caps(o project.Output) string {
	return fmt.Sprintf("video/x-raw,format=%s,width=%d,height=%d,pixel-aspect-ratio=1/1,framerate=%d/1,colorimetry=%s,chroma-site=mpeg2,interlace-mode=progressive",
		rawFormat, o.Width, o.Height, o.FPS, colorimetry)
}

// quote makes a launch-syntax string value.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Channel is a feed's intervideo channel in a run.
func Channel(runID, feedID string) string { return "bgamix-" + runID + "-" + feedID }

// FeedPipeline describes a feed's pipeline: its source, normalised to the output (scaled to fit
// with borders, the output's frame rate and format), into its intervideo channel.
func FeedPipeline(f project.Feed, o project.Output, dir, channel string) string {
	norm := "videoconvert ! videoscale add-borders=true ! videorate ! " + caps(o)
	sink := "intervideosink channel=" + quote(channel)
	switch f.Type {
	case project.FeedTest:
		pat := f.Pattern
		if pat == "" {
			pat = "smpte"
		}
		return fmt.Sprintf("videotestsrc is-live=true pattern=%s ! %s ! %s", pat, norm, sink)
	case project.FeedFile:
		// decodebin's audio pad stays unlinked until audio arrives (milestone 4)
		return fmt.Sprintf("filesrc location=%s ! decodebin name=dec dec. ! video/x-raw ! queue ! %s ! %s",
			quote(project.Resolve(dir, f.Path)), norm, sink)
	case project.FeedImage:
		return fmt.Sprintf("filesrc location=%s ! decodebin ! imagefreeze is-live=true ! %s ! %s",
			quote(project.Resolve(dir, f.Path)), norm, sink)
	}
	return ""
}

// PadProps are a compositor pad's placement.
type PadProps struct {
	X, Y, W, H, Z int
	Alpha         float64
}

// Place computes each feed's pad placement in a layout: feeds the layout does not show are
// transparent. Boxes are drawn in layout order (later on top).
func Place(p *project.Project, l *project.Layout) map[string]PadProps {
	o := p.Output
	out := map[string]PadProps{}
	for _, f := range p.Feeds {
		out[f.ID] = PadProps{W: o.Width, H: o.Height, Alpha: 0}
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

// RecordingPath expands a recording's path ({project}, {date}, {time}) against the project's
// folder, adding the container's extension when the path has none.
func RecordingPath(r project.Recording, p *project.Project, dir string, at time.Time) string {
	name := strings.NewReplacer("{project}", safeName(p.Name), "{date}", at.Format("2006-01-02"), "{time}", at.Format("15-04-05")).Replace(r.Path)
	if filepath.Ext(name) == "" {
		name += "." + container(r)
	}
	return project.Resolve(dir, name)
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

// recordingBranch encodes the program and writes it. MP4 and MOV are fragmented (a fragment and a
// key frame every second) and streamable (the header first), and every buffer is written at once,
// so a recording cut short by a crash still plays up to its last fragment.
func recordingBranch(r project.Recording, o project.Output, path string) string {
	k := Bitrate(r, o)
	enc := fmt.Sprintf("x264enc bitrate=%d speed-preset=veryfast tune=zerolatency key-int-max=%d ! h264parse", k, o.FPS)
	if r.Codec == "h265" {
		enc = fmt.Sprintf("x265enc bitrate=%d speed-preset=veryfast tune=zerolatency key-int-max=%d ! h265parse", k, o.FPS)
	}
	mux := "mp4mux fragment-duration=1000 streamable=true"
	switch container(r) {
	case "mkv":
		mux = "matroskamux"
	case "mov":
		mux = "qtmux fragment-duration=1000 streamable=true"
	}
	// unbuffered: filesink's default 64 kB of buffering in the process is lost with the process,
	// which emptied a low-bitrate recording killed after a few seconds
	return fmt.Sprintf("prog. ! queue ! videoconvert ! %s ! %s ! filesink buffer-mode=unbuffered location=%s", enc, mux, quote(path))
}

// MixerPipeline describes the mixer: an intervideosrc per feed (in project order) into the
// compositor, placed by the layout, and the program teed to each recording (and to a sink that
// keeps the mix running when nothing records). The compositor takes its start time from its
// first input: otherwise its first frames reach the MP4 muxer without timestamps.
func MixerPipeline(p *project.Project, l *project.Layout, runID string, recPaths []string) string {
	o := p.Output
	place := Place(p, l)
	var b strings.Builder
	b.WriteString("compositor name=mix start-time-selection=first background=black")
	for i, f := range p.Feeds {
		pp := place[f.ID]
		fmt.Fprintf(&b, " sink_%d::xpos=%d sink_%d::ypos=%d sink_%d::width=%d sink_%d::height=%d sink_%d::zorder=%d sink_%d::alpha=%g",
			i, pp.X, i, pp.Y, i, pp.W, i, pp.H, i, pp.Z, i, pp.Alpha)
	}
	fmt.Fprintf(&b, " ! %s ! tee name=prog", caps(o))
	b.WriteString(" prog. ! queue leaky=downstream max-size-buffers=2 ! fakesink sync=false")
	for i, r := range p.Recordings {
		b.WriteString(" " + recordingBranch(r, o, recPaths[i]))
	}
	for i, f := range p.Feeds {
		timeout := ""
		if f.Type == project.FeedFile && f.End == project.EndHold || f.Type == project.FeedImage {
			timeout = fmt.Sprintf(" timeout=%d", holdTimeout)
		}
		fmt.Fprintf(&b, " intervideosrc channel=%s%s ! %s ! queue ! mix.sink_%d", quote(Channel(runID, f.ID)), timeout, caps(o), i)
	}
	return b.String()
}

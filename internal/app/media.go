package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"
	"github.com/go-gst/go-gst/pkg/gstapp"

	"github.com/biggaeroza/bga-mix/internal/engine"
)

// Thumbnails are 16:9 JPEGs (other shapes letterboxed), taken a tenth of the way in.
const (
	thumbW = 256
	thumbH = 144
)

// Media is what the library knows of a file.
type Media struct {
	Path      string  `json:"path"`
	Kind      string  `json:"kind"` // video, image
	OK        bool    `json:"ok"`   // a frame was decoded
	Error     string  `json:"error,omitempty"`
	DurationS float64 `json:"duration_s,omitempty"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	FPS       float64 `json:"fps,omitempty"`
	Codec     string  `json:"codec,omitempty"`
	Audio     string  `json:"audio,omitempty"` // the first audio stream's codec ("" for none)
	Size      int64   `json:"size"`
	Thumb     []byte  `json:"-"`
}

type mediaKey struct {
	path  string
	size  int64
	mtime time.Time
}

// mediaCache keeps probes until the file changes; one probe of a file at a time.
type mediaCache struct {
	mu   sync.Mutex
	m    map[mediaKey]*Media
	busy map[string]*sync.Mutex
}

func newMediaCache() *mediaCache {
	return &mediaCache{m: map[mediaKey]*Media{}, busy: map[string]*sync.Mutex{}}
}

// Probe reads a media file's format and checks that it decodes, with a thumbnail (cached until
// the file changes).
func (s *Studio) Probe(path string) (*Media, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmtErr(path, errors.New("is a folder"))
	}
	c := s.media
	c.mu.Lock()
	lock := c.busy[path]
	if lock == nil {
		lock = &sync.Mutex{}
		c.busy[path] = lock
	}
	c.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	key := mediaKey{path, fi.Size(), fi.ModTime()}
	c.mu.Lock()
	m := c.m[key]
	c.mu.Unlock()
	if m != nil {
		return m, nil
	}
	m = probe(path)
	m.Size = fi.Size()
	c.mu.Lock()
	c.m[key] = m
	c.mu.Unlock()
	return m, nil
}

// probe opens the file in a paused pipeline: the decoded video's caps give its size and frame
// rate (0 for a still image), the streams' tags (sticky on the pads: a paused pipeline posts no
// tag messages) their codecs, and the preroll frame (a tenth of the way in for clips over 2 s)
// the thumbnail. The sound goes to a sink that does not wait for it (a file may have none). (go-gst's pbutils discoverer is not used: that
// package calls GStreamer type functions while the program loads, before GStreamer is
// initialised, which prints GLib criticals in every bgamix command.)
func probe(path string) *Media {
	engine.Init()
	m := &Media{Path: path, Kind: MediaKind(path)}
	desc := fmt.Sprintf(`filesrc location="%s" ! decodebin name=d d. ! video/x-raw ! identity name=v ! videoconvert ! videoscale add-borders=true ! video/x-raw,width=%d,height=%d,pixel-aspect-ratio=1/1 ! jpegenc quality=80 ! appsink name=s sync=false max-buffers=1 d. ! audio/x-raw ! fakesink name=a async=false`,
		escape(path), thumbW, thumbH)
	el, err := gst.ParseLaunch(desc)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	pl := el.(gst.Pipeline)
	defer pl.SetState(gst.StateNull)
	fail := func(def string) *Media {
		if err := busError(pl); err != nil {
			m.Error = err.Error()
		} else {
			m.Error = def
		}
		return m
	}
	if pl.SetState(gst.StatePaused) == gst.StateChangeFailure {
		return fail("cannot open the file")
	}
	if _, _, ret := pl.GetState(10 * gst.Second); ret != gst.StateChangeSuccess {
		return fail("not a video file or image GStreamer can decode")
	}
	if c := pl.GetByName("v").GetStaticPad("sink").GetCurrentCaps(); c != nil {
		st := c.GetStructure(0)
		if w, ok := st.GetInt("width"); ok {
			m.Width = int(w)
		}
		if hh, ok := st.GetInt("height"); ok {
			m.Height = int(hh)
		}
		if n, d, ok := st.GetFraction("framerate"); ok && n > 0 && d > 0 {
			m.FPS = float64(n) / float64(d)
		}
	}
	if m.FPS == 0 {
		m.Kind = "image"
	} else {
		m.Kind = "video"
		if d, ok := pl.QueryDuration(gst.FormatTime); ok && d > 0 {
			m.DurationS = float64(d) / 1e9
		}
	}
	m.Codec = tag(pl, "v", "video-codec")
	if pad := pl.GetByName("a").GetStaticPad("sink"); pad.GetCurrentCaps() != nil {
		if m.Audio = tag(pl, "a", "audio-codec"); m.Audio == "" {
			m.Audio = "sound"
		}
	}
	sink := pl.GetByName("s").(gstapp.AppSink)
	if m.DurationS > 2 {
		pl.Seek(1.0, gst.FormatTime, gst.SeekFlagFlush|gst.SeekFlagAccurate, gst.SeekTypeSet, int64(m.DurationS/10*1e9), gst.SeekTypeNone, -1)
		pl.GetState(10 * gst.Second)
	}
	smp := sink.TryPullPreroll(10 * gst.Second)
	if smp == nil {
		return fail("no frame decoded")
	}
	mi, ok := smp.GetBuffer().Map(gst.MapRead)
	if !ok {
		return fail("cannot read the frame")
	}
	m.Thumb = append([]byte(nil), mi.Data()...)
	mi.Unmap()
	m.OK = true
	return m
}

// tag reads a string tag from the events that reached an element's sink pad.
func tag(pl gst.Pipeline, element, name string) string {
	pad := pl.GetByName(element).GetStaticPad("sink")
	for i := uint32(0); i < 8; i++ {
		ev := pad.GetStickyEvent(gst.EventTag, i)
		if ev == nil {
			break
		}
		if v, ok := ev.ParseTag().GetString(name); ok && v != "" {
			return v
		}
	}
	return ""
}

func busError(pl gst.Pipeline) error {
	msg := pl.GetBus().TimedPopFiltered(0, gst.MessageError)
	if msg == nil {
		return errors.New("cannot decode")
	}
	_, err := msg.ParseError()
	if err == nil {
		return errors.New("cannot decode")
	}
	return err
}

func escape(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '"' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"
	"github.com/go-gst/go-gst/pkg/gstapp"

	"github.com/biggaeroza/bga-mix/internal/project"
)

var initOnce sync.Once

// Init initialises GStreamer (once per process).
func Init() { initOnce.Do(func() { gst.Init() }) }

// Reconnection back-off of a failed feed: 1 s, doubling to 30 s; a feed that ran for
// stableAfter before failing starts again at 1 s.
const (
	retryFirst  = time.Second
	retryMax    = 30 * time.Second
	stableAfter = 10 * time.Second
)

// Event is something that happened in a show.
type Event struct {
	Time      time.Time `json:"time"`
	Feed      string    `json:"feed,omitempty"`      // the feed it concerns
	Recording string    `json:"recording,omitempty"` // the recording it concerns ("program-1", "iso-cam")
	Kind      string    `json:"kind"`                // started, looped, next, ended, paused, error, warning, restarting, layout, recording, stopped …
	Text      string    `json:"text,omitempty"`
	Fatal     bool      `json:"fatal,omitempty"` // the show cannot go on (a mixer error)
}

// Options are a run's settings that are not part of the show.
type Options struct {
	// Previews keeps small JPEGs of the program and each feed (Preview).
	Previews bool
}

// Show is a running show. Its methods may be called from any goroutine.
type Show struct {
	mu     sync.Mutex
	p      *project.Project
	dir    string
	runID  string
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc

	feeds    []*feedRun
	byID     map[string]*feedRun
	mixer    gst.Pipeline
	mix      gst.Element
	amixer   gst.Pipeline       // the sound mixer
	audW     map[string]float64 // audio-follows-video weights now (0 to 1)
	levels   map[string]Level   // meters: "program" and feeds
	window   gst.Pipeline       // the program window on this computer (nil: closed)
	listen   gst.Pipeline       // the program's sound on the speakers (nil: off)
	layout   string
	previous string
	cur      map[string]PadProps // the placements applied (before blanking)
	transGen int
	transOn  bool
	recs     []*recRun
	previews map[string]*preview // "program" or a feed id
	stopped  bool
	started  time.Time

	subMu   sync.Mutex
	subs    map[int]chan Event
	nextSub int
	events  <-chan Event
}

// ---- events ------------------------------------------------------------------------------

// Subscribe delivers the show's events until cancel is called (buffered: the oldest are dropped
// when the reader falls behind).
func (s *Show) Subscribe() (<-chan Event, func()) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	ch := make(chan Event, 256)
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	return ch, func() {
		s.subMu.Lock()
		delete(s.subs, id)
		s.subMu.Unlock()
	}
}

// Events is the subscription made when the show started.
func (s *Show) Events() <-chan Event { return s.events }

func (s *Show) emit(e Event) {
	e.Time = time.Now()
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- e:
		default:
			select { // drop the oldest
			case <-ch:
			default:
			}
			select {
			case ch <- e:
			default:
			}
		}
	}
}

// ---- pipelines ---------------------------------------------------------------------------

func launch(desc string) (gst.Pipeline, error) {
	el, err := gst.ParseLaunch(desc)
	if err != nil {
		return nil, fmt.Errorf("%v (pipeline: %s)", err, desc)
	}
	p, ok := el.(gst.Pipeline)
	if !ok {
		return nil, errors.New("not a pipeline: " + desc)
	}
	return p, nil
}

// watch delivers a pipeline's bus messages to handle (on its own goroutine) until ctx ends.
func watch(ctx context.Context, pl gst.Pipeline, handle func(*gst.Message)) {
	go func() {
		for msg := range pl.GetBus().Messages(ctx) {
			handle(msg)
		}
	}()
}

func errText(err error, debug string) string {
	if err == nil {
		return debug
	}
	if debug == "" {
		return err.Error()
	}
	return err.Error() + " (" + debug + ")"
}

// ---- start and stop ----------------------------------------------------------------------

// Start starts a show: the feeds (a feed that fails to start is retried; the show goes on), the
// mixer with the start layout, the previews, and the recordings that are not manual. dir is the
// project's folder (for relative paths).
func Start(p *project.Project, dir string, o Options) (*Show, error) {
	Init()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Show{p: p, dir: dir, opts: o, ctx: ctx, cancel: cancel, byID: map[string]*feedRun{}, previews: map[string]*preview{},
		runID: strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano()%1e9, 36), subs: map[int]chan Event{}, started: time.Now()}
	s.events, _ = s.Subscribe()
	s.mu.Lock()
	defer s.mu.Unlock()
	vi := 0
	var withAudio []string
	for _, f := range p.Feeds {
		fr := &feedRun{s: s, idx: -1, f: f, mode: f.AudioMode(), volDB: f.VolumeDB, mute: f.Mute}
		if project.HasVideo(f.Type) {
			fr.idx = vi
			vi++
		}
		// a feed's sound is carried when it may be heard or is recorded on its own
		fr.audio = project.HasAudio(f.Type) && (fr.mode != project.AudioOff || f.ISO != nil || f.Type == project.FeedAudio)
		if fr.audio {
			withAudio = append(withAudio, f.ID)
		}
		s.feeds = append(s.feeds, fr)
		s.byID[f.ID] = fr
		fr.launchOrFail()
	}
	start := p.Start()
	s.layout = start.ID
	s.cur = Place(p, start)
	s.audW = visible(start)
	s.levels = map[string]Level{}
	amixer, err := launch(AudioMixerPipeline(withAudio, s.runID))
	if err != nil {
		s.shutdownLocked()
		return nil, fmt.Errorf("sound mixer: %w", err)
	}
	s.amixer = amixer
	watch(ctx, amixer, func(msg *gst.Message) {
		switch msg.Type() {
		case gst.MessageElement:
			if name := msg.Source().GetName(); strings.HasPrefix(name, "lv-") {
				if lv, ok := parseLevel(msg.GetStructure().String()); ok {
					s.mu.Lock()
					s.levels[strings.TrimPrefix(name, "lv-")] = lv
					s.mu.Unlock()
				}
			}
		case gst.MessageError:
			debug, err := msg.ParseError()
			s.emit(Event{Kind: "error", Text: "sound mixer: " + errText(err, debug)})
		}
	})
	if amixer.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		s.shutdownLocked()
		return nil, errors.New("the sound mixer did not start")
	}
	mixer, err := launch(MixerPipeline(p, start, s.runID))
	if err != nil {
		s.shutdownLocked()
		return nil, fmt.Errorf("mixer: %w", err)
	}
	s.mixer = mixer
	s.mix = mixer.GetByName("mix")
	watch(ctx, mixer, func(msg *gst.Message) {
		switch msg.Type() {
		case gst.MessageError:
			debug, err := msg.ParseError()
			s.emit(Event{Kind: "error", Text: "mixer: " + errText(err, debug), Fatal: true})
		case gst.MessageWarning:
			debug, err := msg.ParseWarning()
			s.emit(Event{Kind: "warning", Text: "mixer: " + errText(err, debug)})
		}
	})
	if mixer.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		s.shutdownLocked()
		return nil, errors.New("the mixer did not start")
	}
	s.applyLocked()
	if o.Previews {
		s.startPreview("program", ProgramChannel(s.runID), false)
		for _, f := range VideoFeeds(p) {
			s.startPreview(f.ID, FeedChannel(s.runID, f.ID), Held(f))
		}
	}
	for i, r := range p.Recordings {
		rr := &recRun{s: s, label: fmt.Sprintf("program-%d", i+1), r: r, channel: ProgramChannel(s.runID)}
		if !r.NoAudio {
			rr.achannel = ProgramAudioChannel(s.runID)
		}
		s.recs = append(s.recs, rr)
	}
	for _, f := range p.Feeds {
		if f.ISO != nil && project.HasVideo(f.Type) {
			rr := &recRun{s: s, label: "iso-" + f.ID, feed: f.ID, r: *f.ISO, channel: FeedChannel(s.runID, f.ID), held: Held(f)}
			if !f.ISO.NoAudio && s.byID[f.ID].audio {
				rr.achannel = AudioChannel(s.runID, f.ID)
			}
			s.recs = append(s.recs, rr)
		}
	}
	for i := range p.Streams {
		st := p.Streams[i]
		s.recs = append(s.recs, &recRun{s: s, label: fmt.Sprintf("stream-%d", i+1), stream: &st,
			r: project.Recording{Manual: st.Manual, Encoder: st.Encoder, Codec: st.Codec}})
	}
	for _, rr := range s.recs {
		if !rr.r.Manual {
			if err := rr.startLocked(); err != nil {
				s.emit(Event{Recording: rr.label, Kind: "error", Text: err.Error()})
			}
		}
	}
	s.emit(Event{Kind: "started", Text: fmt.Sprintf("%d feed(s), layout %q, %d recording(s), %d stream(s)", len(p.Feeds), start.ID, len(s.recs)-len(p.Streams), len(p.Streams))})
	return s, nil
}

// Stop ends the show: the recordings are finished cleanly (up to 10 s each, in parallel), then
// every pipeline is stopped.
func (s *Show) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.transGen++
	recs := append([]*recRun(nil), s.recs...)
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, rr := range recs {
		wg.Add(1)
		go func() { defer wg.Done(); rr.stop() }()
	}
	wg.Wait()
	s.mu.Lock()
	s.shutdownLocked()
	s.mu.Unlock()
	s.emit(Event{Kind: "stopped"})
}

func (s *Show) shutdownLocked() {
	s.stopped = true
	for _, pv := range s.previews {
		pv.pl.SetState(gst.StateNull)
	}
	if s.mixer != nil {
		s.mixer.SetState(gst.StateNull)
	}
	for _, pl := range []gst.Pipeline{s.amixer, s.window, s.listen} {
		if pl != nil {
			pl.SetState(gst.StateNull)
		}
	}
	for _, fr := range s.feeds {
		if fr.retry != nil {
			fr.retry.Stop()
		}
		fr.teardown()
		fr.state = "stopped"
	}
	s.cancel()
}

// ---- layouts -----------------------------------------------------------------------------

// pad is a feed's compositor input.
func (s *Show) pad(i int) gst.Pad {
	if s.mix == nil || i < 0 {
		return nil
	}
	return s.mix.GetStaticPad("sink_" + strconv.Itoa(i))
}

// applyLocked sets every compositor input to its placement, blanked feeds transparent.
func (s *Show) applyLocked() {
	for _, fr := range s.feeds {
		pp := s.cur[fr.f.ID]
		pad := s.pad(fr.idx)
		if pad == nil {
			continue
		}
		a := pp.Alpha
		if fr.blank {
			a = 0
		}
		pad.SetObjectProperty("xpos", int32(pp.X))
		pad.SetObjectProperty("ypos", int32(pp.Y))
		pad.SetObjectProperty("width", int32(pp.W))
		pad.SetObjectProperty("height", int32(pp.H))
		pad.SetObjectProperty("zorder", uint32(pp.Z))
		pad.SetObjectProperty("alpha", a)
	}
	s.applyAudioLocked()
}

// visible gives each feed shown by a layout (a box with some opacity) the weight 1: the
// audio-follows-video weights the layout leads to.
func visible(l *project.Layout) map[string]float64 {
	w := map[string]float64{}
	for _, b := range l.Boxes {
		if b.Alpha == nil || *b.Alpha > 0 {
			w[b.Feed] = 1
		}
	}
	return w
}

// gain is a feed's fader: its level, times its audio-follows-video weight when it follows.
func (s *Show) gain(fr *feedRun) float64 {
	if fr.mode == project.AudioOff {
		return 0
	}
	g := math.Pow(10, fr.volDB/20)
	if fr.mode == project.AudioFollow {
		g *= s.audW[fr.f.ID]
	}
	return g
}

// applyAudioLocked sets every feed's fader and mute.
func (s *Show) applyAudioLocked() {
	if s.amixer == nil {
		return
	}
	for _, fr := range s.feeds {
		if !fr.audio {
			continue
		}
		if v := s.amixer.GetByName("vol-" + fr.f.ID); v != nil {
			v.SetObjectProperty("volume", s.gain(fr))
			v.SetObjectProperty("mute", fr.mute)
		}
	}
}

// AudioCommand changes a feed's sound live: its level (dB, -60 to +12), mute, and mode
// (follow, on, off); nil and "" leave a setting as it is.
func (s *Show) AudioCommand(id string, volumeDB *float64, mute *bool, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fr := s.byID[id]
	if fr == nil {
		return fmt.Errorf("no feed %q", id)
	}
	if !fr.audio {
		return fmt.Errorf("%q carries no sound in this show (a picture-only feed, or its sound was off when the show started)", id)
	}
	if volumeDB != nil {
		if *volumeDB < -60 || *volumeDB > 12 {
			return errors.New("the level is -60 to +12 dB")
		}
		fr.volDB = *volumeDB
	}
	if mute != nil {
		fr.mute = *mute
	}
	switch mode {
	case "":
	case project.AudioFollow, project.AudioOn, project.AudioOff:
		fr.mode = mode
	default:
		return fmt.Errorf("unknown audio mode %q (follow, on, off)", mode)
	}
	s.applyAudioLocked()
	s.emit(Event{Feed: id, Kind: "audio", Text: fmt.Sprintf("%s, %+.1f dB%s", fr.mode, fr.volDB, map[bool]string{true: ", muted"}[fr.mute])})
	return nil
}

// SetLayout switches to a layout: with t nil, by the show's default transition (a cut when it
// has none). A mix moves, resizes and fades every box from where it is now (so a new switch
// during a mix starts from the mix's current picture).
func (s *Show) SetLayout(id string, t *project.Transition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLayoutLocked(id, t)
}

func (s *Show) setLayoutLocked(id string, t *project.Transition) error {
	if s.stopped {
		return errors.New("the show has stopped")
	}
	l := s.p.Layout(id)
	if l == nil {
		return fmt.Errorf("no layout %q", id)
	}
	if t == nil {
		t = s.p.Transition
	}
	kind, dur := project.TransitionCut, 0.0
	if t != nil {
		kind, dur = t.Kind, t.DurationS
		if kind == project.TransitionMix && dur == 0 {
			dur = project.DefaultMixS
		}
	}
	if id != s.layout {
		s.previous = s.layout
	}
	s.layout = id
	target := Place(s.p, l)
	targetW := visible(l)
	s.transGen++
	gen := s.transGen
	if kind == project.TransitionCut || dur <= 0 {
		s.cur = target
		s.audW = targetW
		s.transOn = false
		s.applyLocked()
		s.emit(Event{Kind: "layout", Text: id + " (cut)"})
		return nil
	}
	from := map[string]PadProps{}
	for k, v := range s.cur {
		from[k] = v
	}
	// a box that appears fades in where it lands; a box that goes fades out where it was, in
	// its place in the stack (taking the new layout's order, it would drop behind a full-screen
	// box at once and vanish instead of fading)
	for k := range target {
		a, b := from[k], target[k]
		if a.Alpha == 0 {
			a.X, a.Y, a.W, a.H = b.X, b.Y, b.W, b.H
		}
		if b.Alpha == 0 {
			b.X, b.Y, b.W, b.H, b.Z = a.X, a.Y, a.W, a.H, a.Z
		}
		from[k] = a
		if b.Alpha == 0 {
			from[k+"\x00to"] = b
		}
	}
	fromW := map[string]float64{}
	for k, v := range s.audW {
		fromW[k] = v
	}
	s.transOn = true
	s.emit(Event{Kind: "layout", Text: fmt.Sprintf("%s (mix %.2f s)", id, dur)})
	frames := max(1, int(math.Ceil(dur*float64(s.p.Output.FPS))))
	period := time.Duration(float64(time.Second) / float64(s.p.Output.FPS))
	go func() {
		tick := time.NewTicker(period)
		defer tick.Stop()
		for k := 1; k <= frames; k++ {
			<-tick.C
			s.mu.Lock()
			if s.transGen != gen {
				s.mu.Unlock()
				return
			}
			u := float64(k) / float64(frames)
			e := u * u * (3 - 2*u) // smoothstep: eases in and out
			cur := map[string]PadProps{}
			for f, b := range target {
				to := b
				if v, ok := from[f+"\x00to"]; ok {
					to = v
				}
				cur[f] = Lerp(from[f], to, e)
			}
			w := map[string]float64{}
			for _, fr := range s.feeds {
				w[fr.f.ID] = fromW[fr.f.ID] + e*(targetW[fr.f.ID]-fromW[fr.f.ID])
			}
			if k == frames {
				cur, w = target, targetW
				s.transOn = false
			}
			s.cur, s.audW = cur, w
			s.applyLocked()
			s.mu.Unlock()
		}
	}()
	return nil
}

// ---- feeds -------------------------------------------------------------------------------

type feedRun struct {
	s        *Show
	idx      int     // the compositor input (-1: sound only)
	audio    bool    // its sound is carried to the sound mixer
	mode     string  // follow, on, off
	volDB    float64 // its level
	mute     bool
	f        project.Feed
	pl       gst.Pipeline
	gen      int
	stopW    context.CancelFunc
	state    string // starting, playing, paused, ended, restarting, stopped
	item     int    // a playlist's current item
	blank    bool   // transparent in the mix (ended black, or failed)
	failures int
	lastErr  string
	restarts int
	since    time.Time
	retry    *time.Timer
}

// launchOrFail (re)builds and starts the feed's pipeline; a failure schedules a retry.
func (fr *feedRun) launchOrFail() {
	if err := fr.launch(); err != nil {
		fr.fail(err.Error())
	}
}

func (fr *feedRun) launch() error {
	s := fr.s
	fr.teardown()
	fr.state = "starting"
	ach := ""
	if fr.audio {
		ach = AudioChannel(s.runID, fr.f.ID)
	}
	pl, err := launch(FeedPipeline(fr.f, fr.item, s.p.Output, s.dir, FeedChannel(s.runID, fr.f.ID), ach))
	if err != nil {
		return err
	}
	if fr.f.Type == project.FeedStream {
		// an RTSP source's buffer (its default is 2 s) is set as uridecodebin makes it
		if dec := pl.GetByName("dec"); dec != nil {
			ms := uint32(latency(fr.f.LatencyMS))
			dec.Connect("source-setup", func(_ gst.Element, src gst.Element) {
				if f := src.GetFactory(); f != nil && f.GetName() == "rtspsrc" {
					src.SetObjectProperty("latency", ms)
				}
			})
		}
	}
	fr.gen++
	gen := fr.gen
	ctx, cancel := context.WithCancel(s.ctx)
	fr.pl, fr.stopW = pl, cancel
	watch(ctx, pl, func(msg *gst.Message) { fr.handle(msg, gen) })
	if fr.f.Type == project.FeedFile && (fr.f.InS > 0 || fr.f.OutS > 0 || fr.segmentLoop()) {
		if pl.SetState(gst.StatePaused) == gst.StateChangeFailure {
			return errors.New("cannot open the file")
		}
		if _, _, ret := pl.GetState(5 * gst.Second); ret == gst.StateChangeFailure {
			return errors.New("cannot open the file")
		}
		fr.seekIn()
	}
	if out := pl.GetByName("out"); out != nil {
		out.SetObjectProperty("show-preroll-frame", true)
	}
	if pl.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		return errors.New("did not start")
	}
	fr.state, fr.since = "playing", time.Now()
	if fr.blank {
		fr.blank = false
		s.applyLocked()
	}
	return nil
}

func (fr *feedRun) teardown() {
	if fr.pl != nil {
		fr.gen++ // messages from the old pipeline are ignored
		fr.stopW()
		fr.pl.SetState(gst.StateNull)
		fr.pl = nil
	}
}

// segmentLoop reports whether a file feed loops by segment seeks: its seeks carry the segment
// flag, so that at its end (or out point) it posts "segment done" instead of ending, and the
// next pass is queued without a flush. A flushing seek back to the start, as first done, stalled
// the sound for seconds after each loop (the audio sink, which does not wait for prerolling so
// that files without sound play, kept the pipeline's old clock base); looping this way is also
// seamless.
func (fr *feedRun) segmentLoop() bool { return fr.f.Type == project.FeedFile && fr.loops() }

// seekIn seeks a file feed to its in point, with its out point as the stop.
func (fr *feedRun) seekIn() bool { return fr.seekTo(fr.f.InS) }

func (fr *feedRun) stop() (gst.SeekType, int64) {
	if fr.f.Type == project.FeedFile && fr.f.OutS > 0 {
		return gst.SeekTypeSet, int64(fr.f.OutS * float64(time.Second))
	}
	return gst.SeekTypeNone, -1
}

func (fr *feedRun) seekTo(pos float64) bool {
	stopType, stop := fr.stop()
	flags := gst.SeekFlagFlush | gst.SeekFlagAccurate
	if fr.segmentLoop() {
		flags |= gst.SeekFlagSegment
	}
	return fr.pl.Seek(1.0, gst.FormatTime, flags, gst.SeekTypeSet, int64(pos*float64(time.Second)), stopType, stop)
}

// seekPaused seeks a running file: paused for the seek, then playing again when it was. A
// flushing seek while playing silenced the sound for seconds (see segmentLoop); going through
// PAUSED gives every sink, the audio one too, the pipeline's new clock base.
func (fr *feedRun) seekPaused(pos float64) bool {
	playing := fr.state == "playing"
	if playing {
		fr.pl.SetState(gst.StatePaused)
		fr.pl.GetState(2 * gst.Second)
	}
	ok := fr.seekTo(pos)
	fr.pl.GetState(2 * gst.Second)
	if playing {
		fr.pl.SetState(gst.StatePlaying)
	}
	return ok
}

// loopAgain queues the next pass of a looping file: a segment seek without a flush.
func (fr *feedRun) loopAgain() bool {
	stopType, stop := fr.stop()
	return fr.pl.Seek(1.0, gst.FormatTime, gst.SeekFlagSegment|gst.SeekFlagAccurate, gst.SeekTypeSet, int64(fr.f.InS*float64(time.Second)), stopType, stop)
}

func (fr *feedRun) handle(msg *gst.Message, gen int) {
	s := fr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != fr.gen || s.stopped {
		return
	}
	switch msg.Type() {
	case gst.MessageEOS:
		fr.onEnd()
	case gst.MessageSegmentDone:
		if fr.segmentLoop() && fr.loopAgain() {
			s.emit(Event{Feed: fr.f.ID, Kind: "looped"})
		} else {
			fr.onEnd()
		}
	case gst.MessageError:
		debug, err := msg.ParseError()
		fr.fail(errText(err, debug))
	case gst.MessageWarning:
		debug, err := msg.ParseWarning()
		if strings.Contains(errText(err, debug), "Delayed linking failed") {
			return // the sound branch of a file without sound: nothing is wrong
		}
		s.emit(Event{Feed: fr.f.ID, Kind: "warning", Text: errText(err, debug)})
	}
}

func (fr *feedRun) loops() bool { return fr.f.End == "" || fr.f.End == project.EndLoop }

// onEnd handles a file's end (or its out point) and a playlist item's end.
func (fr *feedRun) onEnd() {
	s := fr.s
	if fr.f.Type == project.FeedStream || fr.f.Type == project.FeedCamera || fr.f.Type == project.FeedAudio {
		fr.fail("the source ended") // a live source that ends has gone away: reconnect
		return
	}
	switch {
	case fr.f.Type == project.FeedPlaylist && fr.item+1 < len(fr.f.Items):
		fr.item++
		s.emit(Event{Feed: fr.f.ID, Kind: "next", Text: fmt.Sprintf("item %d of %d", fr.item+1, len(fr.f.Items))})
		fr.launchOrFail()
		return
	case fr.loops() && fr.f.Type == project.FeedPlaylist:
		fr.item = 0
		s.emit(Event{Feed: fr.f.ID, Kind: "looped"})
		fr.launchOrFail()
		return
	case fr.loops() && fr.f.Type == project.FeedFile:
		if fr.seekIn() {
			s.emit(Event{Feed: fr.f.ID, Kind: "looped"})
			return
		}
	}
	fr.state = "ended"
	switch fr.f.End {
	case project.EndBlack:
		fr.blank = true
		s.applyLocked()
		s.emit(Event{Feed: fr.f.ID, Kind: "ended", Text: "showing black"})
	case project.EndReturn:
		s.emit(Event{Feed: fr.f.ID, Kind: "ended", Text: "returning to the previous layout"})
		if s.previous != "" {
			s.setLayoutLocked(s.previous, nil)
		}
	default:
		s.emit(Event{Feed: fr.f.ID, Kind: "ended", Text: "holding the last frame"})
	}
}

// fail reports a feed's failure, blanks it, and retries with back-off.
func (fr *feedRun) fail(text string) {
	s := fr.s
	fr.teardown()
	fr.lastErr = text
	if !fr.since.IsZero() && time.Since(fr.since) > stableAfter {
		fr.failures = 0
	}
	fr.failures++
	delay := retryFirst << min(fr.failures-1, 5)
	if delay > retryMax {
		delay = retryMax
	}
	fr.state = "restarting"
	if !fr.blank {
		fr.blank = true
		s.applyLocked()
	}
	s.emit(Event{Feed: fr.f.ID, Kind: "error", Text: text})
	s.emit(Event{Feed: fr.f.ID, Kind: "restarting", Text: fmt.Sprintf("in %.0f s", delay.Seconds())})
	if fr.retry != nil {
		fr.retry.Stop()
	}
	fr.retry = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stopped || fr.state != "restarting" {
			return
		}
		fr.restarts++
		fr.launchOrFail()
		if fr.state == "playing" {
			s.emit(Event{Feed: fr.f.ID, Kind: "started", Text: "reconnected"})
		}
	})
}

// FeedCommand is a transport command: play, pause, restart, seek (to Seconds into the file),
// next, previous.
func (s *Show) FeedCommand(id, cmd string, seconds float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fr := s.byID[id]
	if fr == nil {
		return fmt.Errorf("no feed %q", id)
	}
	if s.stopped {
		return errors.New("the show has stopped")
	}
	isFile := fr.f.Type == project.FeedFile || fr.f.Type == project.FeedPlaylist
	switch cmd {
	case "play":
		if fr.state == "ended" || fr.pl == nil {
			return fr.restartLocked()
		}
		fr.pl.SetState(gst.StatePlaying)
		fr.state = "playing"
		if fr.blank {
			fr.blank = false
			s.applyLocked()
		}
	case "pause":
		if !isFile {
			return errors.New("only files and playlists pause")
		}
		if fr.pl == nil {
			return errors.New("the feed is not running")
		}
		fr.pl.SetState(gst.StatePaused)
		fr.state = "paused"
	case "restart":
		if err := fr.restartLocked(); err != nil {
			return err
		}
	case "seek":
		if !isFile || fr.pl == nil {
			return errors.New("only a running file or playlist seeks")
		}
		pos := seconds
		if fr.f.Type == project.FeedFile {
			pos = math.Max(fr.f.InS, pos)
			if fr.f.OutS > 0 {
				pos = math.Min(fr.f.OutS, pos)
			}
		}
		if !fr.seekPaused(math.Max(0, pos)) {
			return errors.New("the seek failed")
		}
		if fr.state == "ended" {
			fr.pl.SetState(gst.StatePlaying)
			fr.state = "playing"
			if fr.blank {
				fr.blank = false
				s.applyLocked()
			}
		}
	case "next", "previous":
		if fr.f.Type != project.FeedPlaylist {
			return errors.New("only playlists have items")
		}
		n := len(fr.f.Items)
		if cmd == "next" {
			fr.item = (fr.item + 1) % n
		} else {
			fr.item = (fr.item - 1 + n) % n
		}
		s.emit(Event{Feed: id, Kind: "next", Text: fmt.Sprintf("item %d of %d", fr.item+1, n)})
		fr.launchOrFail()
	default:
		return fmt.Errorf("unknown command %q (play, pause, restart, seek, next, previous)", cmd)
	}
	s.emit(Event{Feed: id, Kind: cmd})
	return nil
}

func (fr *feedRun) restartLocked() error {
	if fr.retry != nil {
		fr.retry.Stop()
	}
	fr.item = 0
	if fr.f.Type == project.FeedFile && fr.pl != nil {
		if !fr.seekPaused(fr.f.InS) {
			return errors.New("the seek failed")
		}
		fr.pl.SetState(gst.StatePlaying)
		fr.state = "playing"
		if fr.blank {
			fr.blank = false
			fr.s.applyLocked()
		}
		return nil
	}
	fr.launchOrFail()
	return nil
}

// ---- recordings --------------------------------------------------------------------------

type recRun struct {
	s        *Show
	label    string
	feed     string          // an ISO recording's feed
	stream   *project.Stream // a stream (else a recording)
	r        project.Recording
	channel  string
	achannel string // the sound recorded ("": none)
	held     bool
	encoder  string // the encoder kind in use
	retry    *time.Timer
	failures int
	user     bool // stopped by the operator (a stream is not reconnected)
	pl       gst.Pipeline
	stopW    context.CancelFunc
	eos      chan struct{}
	state    string // idle, recording, paused, stopping, failed
	path     string
	files    []string
	started  time.Time
	pausedT  time.Duration
	pausedA  time.Time
	lastErr  string
}

func (rr *recRun) startLocked() error {
	s := rr.s
	if rr.state == "recording" || rr.state == "paused" || rr.state == "stopping" {
		return errors.New("already recording")
	}
	kind, note := ChooseEncoder(rr.r.Encoder, rr.r.Codec)
	if note != "" {
		s.emit(Event{Recording: rr.label, Kind: "warning", Text: note})
	}
	rr.encoder, rr.user = kind, false
	var desc, path string
	if rr.stream != nil {
		desc, path = StreamPipeline(*rr.stream, s.p.Output, s.runID, kind), StreamDisplay(*rr.stream)
	} else {
		seg := rr.r.SegmentS > 0 || rr.r.SegmentMB > 0
		path = FreePath(RecordingPath(rr.r, s.p, rr.feed, s.dir, time.Now()), seg)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		desc = RecordingPipeline(rr.r, s.p.Output, rr.channel, rr.held, rr.achannel, kind, path)
	}
	pl, err := launch(desc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	eos := make(chan struct{})
	rr.pl, rr.stopW, rr.eos, rr.path = pl, cancel, eos, path
	watch(ctx, pl, func(msg *gst.Message) {
		switch msg.Type() {
		case gst.MessageEOS:
			close(eos)
		case gst.MessageError:
			debug, err := msg.ParseError()
			s.mu.Lock()
			if rr.pl == pl {
				rr.lastErr = errText(err, debug)
				rr.state = "failed"
				pl.SetState(gst.StateNull)
				if rr.stream != nil && !s.stopped {
					rr.reconnectLocked()
				}
			}
			s.mu.Unlock()
			s.emit(Event{Recording: rr.label, Kind: "error", Text: errText(err, debug)})
		case gst.MessageWarning: // srtsink reports a lost receiver this way, and reconnects
			debug, err := msg.ParseWarning()
			s.mu.Lock()
			if rr.pl == pl {
				rr.lastErr = errText(err, debug)
			}
			s.mu.Unlock()
			s.emit(Event{Recording: rr.label, Kind: "warning", Text: errText(err, debug)})
		}
	})
	if pl.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		cancel()
		pl.SetState(gst.StateNull)
		return errors.New("did not start")
	}
	rr.state, rr.started, rr.pausedT, rr.lastErr = "recording", time.Now(), 0, ""
	if rr.stream == nil {
		rr.files = append(rr.files, path)
	}
	s.emit(Event{Recording: rr.label, Kind: "recording", Text: path + " (" + kind + " encoder)"})
	return nil
}

// reconnectLocked starts a failed stream again after a back-off (1 s doubling to 30 s; reset
// after 10 s of good streaming).
func (rr *recRun) reconnectLocked() {
	s := rr.s
	if !rr.started.IsZero() && time.Since(rr.started) > stableAfter {
		rr.failures = 0
	}
	rr.failures++
	delay := min(retryFirst<<min(rr.failures-1, 5), retryMax)
	rr.state = "reconnecting"
	s.emit(Event{Recording: rr.label, Kind: "restarting", Text: fmt.Sprintf("in %.0f s", delay.Seconds())})
	if rr.retry != nil {
		rr.retry.Stop()
	}
	rr.retry = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stopped || rr.user || rr.state != "reconnecting" {
			return
		}
		rr.stopW()
		rr.state = "idle"
		if err := rr.startLocked(); err != nil {
			rr.lastErr = err.Error()
			rr.reconnectLocked()
		}
	})
}

// StreamDisplay is a stream's URL without its key (for the status and the log).
func StreamDisplay(st project.Stream) string {
	if st.Key != "" {
		return strings.TrimSuffix(st.URL, "/") + "/•••"
	}
	return st.URL
}

// stop finishes the recording cleanly (end-of-stream through the muxer, up to 10 s).
func (rr *recRun) stop() {
	s := rr.s
	s.mu.Lock()
	rr.user = true
	if rr.retry != nil {
		rr.retry.Stop()
	}
	if rr.state == "reconnecting" {
		rr.state = "idle"
		s.mu.Unlock()
		s.emit(Event{Recording: rr.label, Kind: "stopped"})
		return
	}
	if rr.state != "recording" && rr.state != "paused" {
		s.mu.Unlock()
		return
	}
	pl, eos, path := rr.pl, rr.eos, rr.path
	if rr.state == "paused" {
		pl.SetState(gst.StatePlaying)
	}
	rr.state = "stopping"
	s.mu.Unlock()
	if rr.stream == nil {
		// end-of-stream finishes the file; it is sent from a goroutine because a sink that is
		// stuck (a full disk, a network sink retrying) can block it
		go pl.SendEvent(gst.NewEventEOS())
		select {
		case <-eos:
		case <-time.After(10 * time.Second):
			s.emit(Event{Recording: rr.label, Kind: "warning", Text: "did not finish within 10 s: stopped anyway"})
		}
	} // a stream needs no finishing: srtsink, while reconnecting, never takes end-of-stream
	pl.SetState(gst.StateNull)
	s.mu.Lock()
	rr.stopW()
	rr.state = "idle"
	s.mu.Unlock()
	s.emit(Event{Recording: rr.label, Kind: "stopped", Text: path})
}

// RecordingCommand starts, stops, pauses or resumes a recording or stream (by its label:
// "program-1" …, "iso-<feed>", "stream-1" …). A paused recording leaves the paused time out of
// the file; streams do not pause.
func (s *Show) RecordingCommand(label, cmd string) error {
	s.mu.Lock()
	var rr *recRun
	for _, r := range s.recs {
		if r.label == label {
			rr = r
		}
	}
	if rr == nil {
		s.mu.Unlock()
		return fmt.Errorf("no recording %q", label)
	}
	if s.stopped {
		s.mu.Unlock()
		return errors.New("the show has stopped")
	}
	switch cmd {
	case "start":
		defer s.mu.Unlock()
		return rr.startLocked()
	case "stop":
		s.mu.Unlock()
		rr.stop()
		return nil
	case "pause":
		defer s.mu.Unlock()
		if rr.stream != nil {
			return errors.New("a stream does not pause (stop it, or cut to a holding layout)")
		}
		if rr.state != "recording" {
			return errors.New("not recording")
		}
		rr.pl.SetState(gst.StatePaused)
		rr.state, rr.pausedA = "paused", time.Now()
	case "resume":
		defer s.mu.Unlock()
		if rr.state != "paused" {
			return errors.New("not paused")
		}
		rr.pl.SetState(gst.StatePlaying)
		rr.state = "recording"
		rr.pausedT += time.Since(rr.pausedA)
	default:
		s.mu.Unlock()
		return fmt.Errorf("unknown command %q (start, stop, pause, resume)", cmd)
	}
	s.emit(Event{Recording: label, Kind: cmd})
	return nil
}

// ---- previews ----------------------------------------------------------------------------

type preview struct {
	pl   gst.Pipeline
	mu   sync.Mutex
	jpeg []byte
	at   time.Time
}

func (s *Show) startPreview(key, channel string, held bool) {
	pl, err := launch(PreviewPipeline(s.p.Output, channel, held))
	if err != nil {
		s.emit(Event{Kind: "warning", Text: "preview " + key + ": " + err.Error()})
		return
	}
	pv := &preview{pl: pl}
	sink := pl.GetByName("jpeg").(gstapp.AppSink)
	sink.SetCallbacks(&gstapp.AppSinkCallbacks{NewSample: func(as gstapp.AppSink) gst.FlowReturn {
		smp := as.PullSample()
		if smp == nil {
			return gst.FlowOK
		}
		buf := smp.GetBuffer()
		mi, ok := buf.Map(gst.MapRead)
		if !ok {
			return gst.FlowOK
		}
		b := append([]byte(nil), mi.Data()...)
		mi.Unmap()
		pv.mu.Lock()
		pv.jpeg, pv.at = b, time.Now()
		pv.mu.Unlock()
		return gst.FlowOK
	}})
	pl.SetState(gst.StatePlaying)
	s.previews[key] = pv
}

// Preview returns the latest JPEG of the program ("program") or a feed (its id).
func (s *Show) Preview(key string) ([]byte, time.Time, bool) {
	s.mu.Lock()
	pv := s.previews[key]
	s.mu.Unlock()
	if pv == nil {
		return nil, time.Time{}, false
	}
	pv.mu.Lock()
	defer pv.mu.Unlock()
	return pv.jpeg, pv.at, pv.jpeg != nil
}

// ---- status ------------------------------------------------------------------------------

// FeedStatus is a feed's state.
type FeedStatus struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	State     string  `json:"state"`
	Blank     bool    `json:"blank,omitempty"`
	PositionS float64 `json:"position_s,omitempty"`
	DurationS float64 `json:"duration_s,omitempty"`
	Item      int     `json:"item,omitempty"` // a playlist's current item (from 1)
	Items     int     `json:"items,omitempty"`
	Error     string  `json:"error,omitempty"`
	Restarts  int     `json:"restarts,omitempty"`
	// sound (feeds carrying it)
	HasAudio bool    `json:"has_audio,omitempty"`
	Audio    string  `json:"audio,omitempty"` // follow, on, off
	VolumeDB float64 `json:"volume_db"`
	Mute     bool    `json:"mute,omitempty"`
	Heard    float64 `json:"heard,omitempty"` // its gain into the mix now (0: not heard)
	Level    *Level  `json:"level,omitempty"`
}

// StreamStatus is a stream's state.
type StreamStatus struct {
	Label    string  `json:"label"`
	Name     string  `json:"name,omitempty"`
	URL      string  `json:"url"` // without the key
	State    string  `json:"state"`
	ElapsedS float64 `json:"elapsed_s,omitempty"`
	Encoder  string  `json:"encoder,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// RecordingStatus is a recording's state.
type RecordingStatus struct {
	Label    string   `json:"label"`
	Feed     string   `json:"feed,omitempty"`
	State    string   `json:"state"`
	Path     string   `json:"path,omitempty"`
	Files    []string `json:"files,omitempty"`
	ElapsedS float64  `json:"elapsed_s,omitempty"` // recorded time (pauses left out)
	Bytes    int64    `json:"bytes,omitempty"`     // the current file's size
	Error    string   `json:"error,omitempty"`
	Encoder  string   `json:"encoder,omitempty"`
}

// LayoutInfo names a layout.
type LayoutInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Status is the show's state.
type Status struct {
	Name          string            `json:"name"`
	Output        project.Output    `json:"output"`
	Layout        string            `json:"layout"`
	Previous      string            `json:"previous,omitempty"`
	Transitioning bool              `json:"transitioning,omitempty"`
	Layouts       []LayoutInfo      `json:"layouts"`
	Feeds         []FeedStatus      `json:"feeds"`
	Recordings    []RecordingStatus `json:"recordings"`
	UptimeS       float64           `json:"uptime_s"`
	Stopped       bool              `json:"stopped,omitempty"`
	Previews      bool              `json:"previews,omitempty"`
	Streams       []StreamStatus    `json:"streams"`
	ProgramLevel  *Level            `json:"program_level,omitempty"`
	Window        bool              `json:"window,omitempty"`
	Listen        bool              `json:"listen,omitempty"`
}

// Status reports the show's state.
func (s *Show) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Name: s.p.Name, Output: s.p.Output, Layout: s.layout, Previous: s.previous, Transitioning: s.transOn,
		UptimeS: time.Since(s.started).Seconds(), Stopped: s.stopped, Previews: s.opts.Previews}
	for _, l := range s.p.Layouts {
		st.Layouts = append(st.Layouts, LayoutInfo{l.ID, l.Name})
	}
	for _, fr := range s.feeds {
		fs := FeedStatus{ID: fr.f.ID, Name: fr.f.Name, Type: fr.f.Type, State: fr.state, Blank: fr.blank, Restarts: fr.restarts}
		if fr.state == "restarting" {
			fs.Error = fr.lastErr
		}
		if fr.f.Type == project.FeedPlaylist {
			fs.Item, fs.Items = fr.item+1, len(fr.f.Items)
		}
		if fr.pl != nil && (fr.f.Type == project.FeedFile || fr.f.Type == project.FeedPlaylist) {
			if pos, ok := fr.pl.QueryPosition(gst.FormatTime); ok && pos >= 0 {
				fs.PositionS = float64(pos) / 1e9
			}
			if d, ok := fr.pl.QueryDuration(gst.FormatTime); ok && d > 0 {
				fs.DurationS = float64(d) / 1e9
			}
		}
		if fr.audio {
			fs.HasAudio, fs.Audio, fs.VolumeDB, fs.Mute, fs.Heard = true, fr.mode, fr.volDB, fr.mute, s.gain(fr)
			if fr.mute {
				fs.Heard = 0
			}
			if lv, ok := s.levels[fr.f.ID]; ok && time.Since(lv.At) < time.Second {
				fs.Level = &lv
			}
		}
		st.Feeds = append(st.Feeds, fs)
	}
	st.Streams = []StreamStatus{}
	if lv, ok := s.levels["program"]; ok && time.Since(lv.At) < time.Second {
		st.ProgramLevel = &lv
	}
	st.Window, st.Listen = s.window != nil, s.listen != nil
	for _, rr := range s.recs {
		if rr.stream != nil {
			ss := StreamStatus{Label: rr.label, Name: rr.stream.Name, URL: StreamDisplay(*rr.stream), State: rr.state, Error: rr.lastErr, Encoder: rr.encoder}
			if ss.State == "" {
				ss.State = "idle"
			}
			if rr.state == "recording" {
				ss.State, ss.ElapsedS = "live", time.Since(rr.started).Seconds()
			}
			st.Streams = append(st.Streams, ss)
			continue
		}
		rs := RecordingStatus{Label: rr.label, Feed: rr.feed, State: rr.state, Files: rr.files, Error: rr.lastErr, Encoder: rr.encoder}
		if rs.State == "" {
			rs.State = "idle"
		}
		if rr.state == "recording" || rr.state == "paused" || rr.state == "stopping" {
			rs.Path = rr.path
			el := time.Since(rr.started) - rr.pausedT
			if rr.state == "paused" {
				el -= time.Since(rr.pausedA)
			}
			rs.ElapsedS = el.Seconds()
			if fi, err := os.Stat(rr.path); err == nil {
				rs.Bytes = fi.Size()
			}
		}
		st.Recordings = append(st.Recordings, rs)
	}
	return st
}

// Project is the show being run.
func (s *Show) Project() *project.Project { return s.p }

// Recordings lists the files written so far (all recordings).
func (s *Show) Recordings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, rr := range s.recs {
		out = append(out, rr.files...)
	}
	return out
}

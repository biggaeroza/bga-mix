package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
	for i, f := range p.Feeds {
		fr := &feedRun{s: s, idx: i, f: f}
		s.feeds = append(s.feeds, fr)
		s.byID[f.ID] = fr
		fr.launchOrFail()
	}
	start := p.Start()
	s.layout = start.ID
	s.cur = Place(p, start)
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
		for _, f := range p.Feeds {
			s.startPreview(f.ID, FeedChannel(s.runID, f.ID), Held(f))
		}
	}
	for i, r := range p.Recordings {
		s.recs = append(s.recs, &recRun{s: s, label: fmt.Sprintf("program-%d", i+1), r: r, channel: ProgramChannel(s.runID)})
	}
	for _, f := range p.Feeds {
		if f.ISO != nil {
			s.recs = append(s.recs, &recRun{s: s, label: "iso-" + f.ID, feed: f.ID, r: *f.ISO, channel: FeedChannel(s.runID, f.ID), held: Held(f)})
		}
	}
	for _, rr := range s.recs {
		if !rr.r.Manual {
			if err := rr.startLocked(); err != nil {
				s.emit(Event{Recording: rr.label, Kind: "error", Text: err.Error()})
			}
		}
	}
	s.emit(Event{Kind: "started", Text: fmt.Sprintf("%d feed(s), layout %q, %d recording(s)", len(p.Feeds), start.ID, len(s.recs))})
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
	if s.mix == nil {
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
	s.transGen++
	gen := s.transGen
	if kind == project.TransitionCut || dur <= 0 {
		s.cur = target
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
			if k == frames {
				cur = target
				s.transOn = false
			}
			s.cur = cur
			s.applyLocked()
			s.mu.Unlock()
		}
	}()
	return nil
}

// ---- feeds -------------------------------------------------------------------------------

type feedRun struct {
	s        *Show
	idx      int
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
	pl, err := launch(FeedPipeline(fr.f, fr.item, s.p.Output, s.dir, FeedChannel(s.runID, fr.f.ID)))
	if err != nil {
		return err
	}
	fr.gen++
	gen := fr.gen
	ctx, cancel := context.WithCancel(s.ctx)
	fr.pl, fr.stopW = pl, cancel
	watch(ctx, pl, func(msg *gst.Message) { fr.handle(msg, gen) })
	if fr.f.Type == project.FeedFile && (fr.f.InS > 0 || fr.f.OutS > 0) {
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

// seekIn seeks a file feed to its in point, with its out point as the stop.
func (fr *feedRun) seekIn() bool { return fr.seekTo(fr.f.InS) }

func (fr *feedRun) seekTo(pos float64) bool {
	stopType, stop := gst.SeekTypeNone, int64(-1)
	if fr.f.Type == project.FeedFile && fr.f.OutS > 0 {
		stopType, stop = gst.SeekTypeSet, int64(fr.f.OutS*float64(time.Second))
	}
	return fr.pl.Seek(1.0, gst.FormatTime, gst.SeekFlagFlush|gst.SeekFlagAccurate, gst.SeekTypeSet, int64(pos*float64(time.Second)), stopType, stop)
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
	case gst.MessageError:
		debug, err := msg.ParseError()
		fr.fail(errText(err, debug))
	case gst.MessageWarning:
		debug, err := msg.ParseWarning()
		s.emit(Event{Feed: fr.f.ID, Kind: "warning", Text: errText(err, debug)})
	}
}

func (fr *feedRun) loops() bool { return fr.f.End == "" || fr.f.End == project.EndLoop }

// onEnd handles a file's end (or its out point) and a playlist item's end.
func (fr *feedRun) onEnd() {
	s := fr.s
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
		return fr.restartLocked()
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
		if !fr.seekTo(math.Max(0, pos)) {
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
		if !fr.seekIn() {
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
	s       *Show
	label   string
	feed    string // an ISO recording's feed
	r       project.Recording
	channel string
	held    bool
	pl      gst.Pipeline
	stopW   context.CancelFunc
	eos     chan struct{}
	state   string // idle, recording, paused, stopping, failed
	path    string
	files   []string
	started time.Time
	pausedT time.Duration
	pausedA time.Time
	lastErr string
}

func (rr *recRun) startLocked() error {
	s := rr.s
	if rr.state == "recording" || rr.state == "paused" || rr.state == "stopping" {
		return errors.New("already recording")
	}
	seg := rr.r.SegmentS > 0 || rr.r.SegmentMB > 0
	path := FreePath(RecordingPath(rr.r, s.p, rr.feed, s.dir, time.Now()), seg)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	pl, err := launch(RecordingPipeline(rr.r, s.p.Output, rr.channel, rr.held, path))
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
			}
			s.mu.Unlock()
			s.emit(Event{Recording: rr.label, Kind: "error", Text: errText(err, debug)})
		}
	})
	if pl.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		cancel()
		pl.SetState(gst.StateNull)
		return errors.New("the recording did not start")
	}
	rr.state, rr.started, rr.pausedT, rr.lastErr = "recording", time.Now(), 0, ""
	rr.files = append(rr.files, path)
	s.emit(Event{Recording: rr.label, Kind: "recording", Text: path})
	return nil
}

// stop finishes the recording cleanly (end-of-stream through the muxer, up to 10 s).
func (rr *recRun) stop() {
	s := rr.s
	s.mu.Lock()
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
	pl.SendEvent(gst.NewEventEOS())
	select {
	case <-eos:
	case <-time.After(10 * time.Second):
		s.emit(Event{Recording: rr.label, Kind: "warning", Text: "did not finish within 10 s: stopped anyway"})
	}
	s.mu.Lock()
	pl.SetState(gst.StateNull)
	rr.stopW()
	rr.state = "idle"
	s.mu.Unlock()
	s.emit(Event{Recording: rr.label, Kind: "stopped", Text: path})
}

// RecordingCommand starts, stops, pauses or resumes a recording (by its label: "program-1" …,
// "iso-<feed>"). A paused recording leaves the paused time out of the file.
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
		st.Feeds = append(st.Feeds, fs)
	}
	for _, rr := range s.recs {
		rs := RecordingStatus{Label: rr.label, Feed: rr.feed, State: rr.state, Files: rr.files, Error: rr.lastErr}
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

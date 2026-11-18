package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"

	"github.com/biggaeroza/bga-mix/internal/project"
)

var initOnce sync.Once

// Init initialises GStreamer (once per process).
func Init() { initOnce.Do(func() { gst.Init() }) }

// Event is something that happened in a run.
type Event struct {
	Time  time.Time
	Feed  string // "" for the mixer
	Kind  string // started, looped, ended, error, warning, stopped
	Text  string
	Fatal bool // the run cannot go on (a mixer error)
}

// Show is a running show.
type Show struct {
	p      *project.Project
	dir    string
	runID  string
	feeds  map[string]*feedRun
	mixer  gst.Pipeline
	events chan Event
	ctx    context.Context
	cancel context.CancelFunc
	eos    chan struct{} // closed when the mixer reaches end-of-stream
	wg     sync.WaitGroup

	// Recordings are the files being written.
	Recordings []string
}

type feedRun struct {
	f        project.Feed
	pipeline gst.Pipeline
}

// Events delivers the run's events (buffered; the oldest are dropped when nobody reads).
func (s *Show) Events() <-chan Event { return s.events }

func (s *Show) emit(e Event) {
	e.Time = time.Now()
	select {
	case s.events <- e:
	default:
		select { // drop the oldest
		case <-s.events:
		default:
		}
		select {
		case s.events <- e:
		default:
		}
	}
}

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

// Start starts a show: the feeds, then the mixer with the start layout and the recordings. dir
// is the project's folder (for relative paths).
func Start(p *project.Project, dir string) (*Show, error) {
	Init()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Show{p: p, dir: dir, runID: strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano()%1e6, 10),
		feeds: map[string]*feedRun{}, events: make(chan Event, 256), ctx: ctx, cancel: cancel, eos: make(chan struct{})}
	fail := func(err error) (*Show, error) { s.Stop(); return nil, err }
	for _, f := range p.Feeds {
		desc := FeedPipeline(f, p.Output, dir, Channel(s.runID, f.ID))
		pl, err := launch(desc)
		if err != nil {
			return fail(fmt.Errorf("feed %s: %w", f.ID, err))
		}
		fr := &feedRun{f: f, pipeline: pl}
		s.feeds[f.ID] = fr
		s.watchFeed(fr)
		if err := s.startFeed(fr); err != nil {
			s.emit(Event{Feed: f.ID, Kind: "error", Text: err.Error()})
		}
	}
	now := time.Now()
	for _, r := range p.Recordings {
		path := RecordingPath(r, p, dir, now)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fail(err)
		}
		s.Recordings = append(s.Recordings, path)
	}
	mixer, err := launch(MixerPipeline(p, p.Start(), s.runID, s.Recordings))
	if err != nil {
		return fail(fmt.Errorf("mixer: %w", err))
	}
	s.mixer = mixer
	s.watchMixer()
	if mixer.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		return fail(errors.New("the mixer did not start"))
	}
	s.emit(Event{Kind: "started", Text: fmt.Sprintf("%d feed(s), layout %q, %d recording(s)", len(p.Feeds), p.Start().ID, len(p.Recordings))})
	return s, nil
}

// startFeed plays a feed; a file feed with an in or out point is first paused and seeked.
func (s *Show) startFeed(fr *feedRun) error {
	f := fr.f
	if f.Type == project.FeedFile && (f.InS > 0 || f.OutS > 0) {
		if fr.pipeline.SetState(gst.StatePaused) == gst.StateChangeFailure {
			return errors.New("cannot open the file")
		}
		if _, _, ret := fr.pipeline.GetState(5 * gst.Second); ret == gst.StateChangeFailure {
			return errors.New("cannot open the file")
		}
		s.seekIn(fr)
	}
	if fr.pipeline.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		return errors.New("did not start")
	}
	s.emit(Event{Feed: f.ID, Kind: "started"})
	return nil
}

// seekIn seeks a file feed to its in point, with its out point as the stop.
func (s *Show) seekIn(fr *feedRun) bool {
	stopType, stop := gst.SeekTypeNone, int64(-1)
	if fr.f.OutS > 0 {
		stopType, stop = gst.SeekTypeSet, int64(fr.f.OutS*float64(time.Second))
	}
	return fr.pipeline.Seek(1.0, gst.FormatTime, gst.SeekFlagFlush|gst.SeekFlagAccurate, gst.SeekTypeSet, int64(fr.f.InS*float64(time.Second)), stopType, stop)
}

func (s *Show) watchFeed(fr *feedRun) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for msg := range fr.pipeline.GetBus().Messages(s.ctx) {
			switch msg.Type() {
			case gst.MessageEOS:
				if fr.f.Type == project.FeedFile && (fr.f.End == "" || fr.f.End == project.EndLoop) {
					if s.seekIn(fr) {
						s.emit(Event{Feed: fr.f.ID, Kind: "looped"})
						continue
					}
				}
				s.emit(Event{Feed: fr.f.ID, Kind: "ended", Text: map[bool]string{true: "holding the last frame", false: "showing black"}[fr.f.End == project.EndHold]})
			case gst.MessageError:
				debug, err := msg.ParseError()
				// a failed feed leaves its slot black or held; the mix goes on (reconnection:
				// milestone 2)
				s.emit(Event{Feed: fr.f.ID, Kind: "error", Text: errText(err, debug)})
			case gst.MessageWarning:
				debug, err := msg.ParseWarning()
				s.emit(Event{Feed: fr.f.ID, Kind: "warning", Text: errText(err, debug)})
			}
		}
	}()
}

func (s *Show) watchMixer() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for msg := range s.mixer.GetBus().Messages(s.ctx) {
			switch msg.Type() {
			case gst.MessageEOS:
				close(s.eos)
				return
			case gst.MessageError:
				debug, err := msg.ParseError()
				s.emit(Event{Kind: "error", Text: errText(err, debug), Fatal: true})
			case gst.MessageWarning:
				debug, err := msg.ParseWarning()
				s.emit(Event{Kind: "warning", Text: errText(err, debug)})
			}
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

// Stop ends the show: end-of-stream is sent into the program's tee, after the compositor, so the
// recordings are finished cleanly (up to 10 s) and their last frame is complete (sent to the
// pipeline, it reached the feeds' inputs one by one, and the compositor made a last frame with
// some feeds already gone); then every pipeline is stopped.
func (s *Show) Stop() {
	if s.mixer != nil {
		sent := false
		if tee := s.mixer.GetByName("prog"); tee != nil {
			if pad := tee.GetStaticPad("sink"); pad != nil {
				sent = pad.SendEvent(gst.NewEventEOS())
			}
		}
		if !sent {
			s.mixer.SendEvent(gst.NewEventEOS())
		}
		select {
		case <-s.eos:
		case <-time.After(10 * time.Second):
			s.emit(Event{Kind: "warning", Text: "the recordings did not finish within 10 s: stopped anyway"})
		}
		s.mixer.SetState(gst.StateNull)
	}
	for _, fr := range s.feeds {
		fr.pipeline.SetState(gst.StateNull)
	}
	s.cancel()
	s.emit(Event{Kind: "stopped"})
}

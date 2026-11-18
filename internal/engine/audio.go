package engine

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"
)

// Level is a meter reading in dBFS (the louder of the two channels; -100 for silence).
type Level struct {
	PeakDB float64   `json:"peak_db"`
	RMSDB  float64   `json:"rms_db"`
	At     time.Time `json:"-"`
}

// floorDB is the meters' floor.
const floorDB = -100

// The level element posts its readings as arrays of GValues, which the Go bindings do not
// convert: the message's text is read instead ("rms=(GValueArray)< -9.03, -9.03 >").
var levelField = regexp.MustCompile(`(rms|peak)=\(GValueArray\)< ?([^>]*) ?>`)

func parseLevel(text string) (Level, bool) {
	lv := Level{PeakDB: floorDB, RMSDB: floorDB, At: time.Now()}
	found := 0
	for _, m := range levelField.FindAllStringSubmatch(text, -1) {
		best := math.Inf(-1)
		for _, v := range strings.Split(m[2], ",") {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > best {
				best = f
			}
		}
		if math.IsInf(best, -1) || math.IsNaN(best) || best < floorDB {
			best = floorDB
		}
		if m[1] == "rms" {
			lv.RMSDB = best
		} else {
			lv.PeakDB = best
		}
		found++
	}
	return lv, found == 2
}

// Levels are the meters: "program" and each feed carrying sound (readings older than a second
// read as silence).
func (s *Show) Levels() map[string]Level {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Level{}
	for k, v := range s.levels {
		if time.Since(v.At) > time.Second {
			v = Level{PeakDB: floorDB, RMSDB: floorDB}
		}
		out[k] = v
	}
	return out
}

// Monitor opens or closes the program window ("window") or the program's sound on this
// computer's speakers ("listen").
func (s *Show) Monitor(kind string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("the show has stopped")
	}
	slot := &s.window
	desc := WindowPipeline(s.p.Output, s.runID)
	switch kind {
	case "window":
	case "listen":
		slot, desc = &s.listen, ListenPipeline(s.runID)
	default:
		return errors.New("window or listen")
	}
	if *slot != nil {
		(*slot).SetState(gst.StateNull)
		*slot = nil
	}
	if on {
		pl, err := launch(desc)
		if err != nil {
			return err
		}
		watch(s.ctx, pl, func(msg *gst.Message) {
			if msg.Type() == gst.MessageError {
				debug, err := msg.ParseError()
				s.emit(Event{Kind: "warning", Text: kind + ": " + errText(err, debug)})
			}
		})
		if pl.SetState(gst.StatePlaying) == gst.StateChangeFailure {
			pl.SetState(gst.StateNull)
			return errors.New("could not open the " + map[string]string{"window": "program window", "listen": "speakers"}[kind])
		}
		*slot = pl
	}
	s.emit(Event{Kind: kind, Text: map[bool]string{true: "on", false: "off"}[on]})
	return nil
}

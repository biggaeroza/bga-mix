package server

import (
	"errors"

	"github.com/go-gst/go-gst/pkg/gst"

	"github.com/biggaeroza/bga-mix/internal/engine"
)

// runGst runs a pipeline to its end.
func runGst(desc string) error {
	engine.Init()
	el, err := gst.ParseLaunch(desc)
	if err != nil {
		return err
	}
	pl := el.(gst.Pipeline)
	defer pl.SetState(gst.StateNull)
	pl.SetState(gst.StatePlaying)
	msg := pl.GetBus().TimedPopFiltered(10*gst.Second, gst.MessageEOS|gst.MessageError)
	if msg == nil || msg.Type() == gst.MessageError {
		return errors.New("the pipeline failed")
	}
	return nil
}

package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-gst/go-gst/pkg/gst"
)

// Video encoders. Hardware encoders are tried in this order for "auto"; each is test-encoded
// once (a few frames) before it is used, so a driver that is installed but does not work falls
// back to software (x264, x265). Their properties follow each element's documentation; an
// encoder whose properties are wrong fails its test and is not used.
type encSpec struct {
	kind       string // va, nvenc, qsv, vaapi, software
	h264, h265 string // GStreamer element names
	props      func(kbps, gop int) string
	hardware   bool
}

var encoders = []encSpec{
	{"nvenc", "nvh264enc", "nvh265enc", func(k, g int) string { return fmt.Sprintf("bitrate=%d gop-size=%d rc-mode=cbr", k, g) }, true},
	{"va", "vah264enc", "vah265enc", func(k, g int) string { return fmt.Sprintf("bitrate=%d key-int-max=%d rate-control=cbr", k, g) }, true},
	{"qsv", "qsvh264enc", "qsvh265enc", func(k, g int) string { return fmt.Sprintf("bitrate=%d gop-size=%d rate-control=cbr", k, g) }, true},
	{"vaapi", "vaapih264enc", "vaapih265enc", func(k, g int) string { return fmt.Sprintf("bitrate=%d keyframe-period=%d rate-control=cbr", k, g) }, true},
	{"software", "x264enc", "x265enc", func(k, g int) string {
		return fmt.Sprintf("bitrate=%d speed-preset=veryfast tune=zerolatency key-int-max=%d", k, g)
	}, false},
}

func encoderSpec(kind string) encSpec {
	for _, e := range encoders {
		if e.kind == kind {
			return e
		}
	}
	return encoders[len(encoders)-1]
}

// encoderChain is the element chain that encodes raw video: hardware encoders get NV12 (the
// format they all take), x264/x265 I420; then the parser.
func encoderChain(kind, codec string, kbps, gop int) string {
	e := encoderSpec(kind)
	name, parse := e.h264, "h264parse"
	if codec == "h265" {
		name, parse = e.h265, "h265parse"
	}
	in := "videoconvert"
	if e.hardware {
		in = "videoconvert ! video/x-raw,format=NV12"
	}
	return fmt.Sprintf("%s ! %s %s ! %s config-interval=-1", in, name, e.props(kbps, gop), parse)
}

var (
	encMu     sync.Mutex
	encWorks  = map[string]bool{} // kind/codec → test result
	encTested = map[string]bool{}
)

// EncoderWorks test-encodes a few frames with an encoder (once per process).
func EncoderWorks(kind, codec string) bool {
	key := kind + "/" + codec
	encMu.Lock()
	defer encMu.Unlock()
	if encTested[key] {
		return encWorks[key]
	}
	encTested[key] = true
	e := encoderSpec(kind)
	name := e.h264
	if codec == "h265" {
		name = e.h265
	}
	Init()
	if gst.ElementFactoryFind(name) == nil {
		return false
	}
	el, err := gst.ParseLaunch("videotestsrc num-buffers=10 ! video/x-raw,width=320,height=240,framerate=25/1 ! " +
		encoderChain(kind, codec, 1000, 25) + " ! fakesink")
	if err != nil {
		return false
	}
	pl := el.(gst.Pipeline)
	defer pl.SetState(gst.StateNull)
	if pl.SetState(gst.StatePlaying) == gst.StateChangeFailure {
		return false
	}
	msg := pl.GetBus().TimedPopFiltered(gst.ClockTime(10*time.Second), gst.MessageEOS|gst.MessageError)
	ok := msg != nil && msg.Type() == gst.MessageEOS
	encWorks[key] = ok
	return ok
}

// ChooseEncoder picks the encoder for a preference ("", auto, software, va, vaapi, nvenc, qsv):
// a named hardware encoder that does not work falls back to software, with a note.
func ChooseEncoder(pref, codec string) (kind, note string) {
	if codec == "" {
		codec = "h264"
	}
	switch pref {
	case "software":
		return "software", ""
	case "", "auto":
		for _, e := range encoders {
			if e.hardware && EncoderWorks(e.kind, codec) {
				return e.kind, ""
			}
		}
		return "software", ""
	}
	if EncoderWorks(pref, codec) {
		return pref, ""
	}
	return "software", fmt.Sprintf("the %s encoder (%s) does not work here: using software encoding", pref, codec)
}

// EncoderInfo is an encoder and whether it works on this machine.
type EncoderInfo struct {
	Kind     string `json:"kind"`
	Element  string `json:"element"`
	Hardware bool   `json:"hardware"`
	Works    bool   `json:"works"`
}

// Encoders lists the H.264 encoders and whether each works here (the hardware ones are
// test-encoded once).
func Encoders() []EncoderInfo {
	var out []EncoderInfo
	for _, e := range encoders {
		out = append(out, EncoderInfo{Kind: e.kind, Element: e.h264, Hardware: e.hardware, Works: EncoderWorks(e.kind, "h264")})
	}
	return out
}

// streamKind is "rtmp" or "srt" by a stream URL's scheme.
func streamKind(url string) string {
	if strings.HasPrefix(strings.ToLower(url), "srt://") {
		return "srt"
	}
	return "rtmp"
}

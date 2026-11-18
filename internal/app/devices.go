package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gst/go-gst/pkg/gst"

	"github.com/biggaeroza/bga-mix/internal/engine"
)

// Device is a camera or a sound input a feed can use.
type Device struct {
	Device string `json:"device"` // what goes in the feed's device field
	Name   string `json:"name"`
}

// Devices lists the cameras (Video4Linux capture devices) and the sound inputs (PulseAudio or
// PipeWire sources, the speakers' monitors included).
func Devices() (cameras, audio []Device) {
	cameras, audio = []Device{}, []Device{}
	// cameras: /dev/video* that capture (a camera gives two or more nodes; the extra ones are
	// metadata, and say so in their name or index)
	paths, _ := filepath.Glob("/dev/video*")
	sort.Strings(paths)
	for _, p := range paths {
		base := filepath.Base(p)
		name, _ := os.ReadFile("/sys/class/video4linux/" + base + "/name")
		idx, _ := os.ReadFile("/sys/class/video4linux/" + base + "/index")
		if strings.TrimSpace(string(idx)) != "" && strings.TrimSpace(string(idx)) != "0" {
			continue // not the device's capture node
		}
		n := strings.TrimSpace(string(name))
		if n == "" {
			n = base
		}
		cameras = append(cameras, Device{Device: p, Name: n + " (" + p + ")"})
	}
	engine.Init()
	os.Setenv("LIBCAMERA_LOG_LEVELS", "*:ERROR") // the device monitor wakes libcamera, which talks
	m := gst.NewDeviceMonitor()
	m.AddFilter("Audio/Source", nil)
	seen := map[string]bool{}
	for _, d := range m.GetDevices() {
		props := d.GetProperties()
		if props == nil || !strings.HasPrefix(props.String(), "pulse-proplist") {
			continue // the same inputs appear through PipeWire and ALSA too
		}
		node := props.GetString("node.name")
		if node == "" {
			continue
		}
		if props.GetString("media.class") == "Audio/Sink" { // a monitor of an output
			node += ".monitor"
		}
		if seen[node] {
			continue
		}
		seen[node] = true
		audio = append(audio, Device{Device: node, Name: d.GetDisplayName()})
	}
	return cameras, audio
}

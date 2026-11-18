# BGAMix

A live video mixer you set up and run from a GUI instead of scripts. BGAMix takes cameras,
network streams, video files and images as feeds, places them in layouts (full screen,
picture-in-picture, side by side, grids, your own), switches between layouts live with cuts and
transitions, and records and streams the result. It runs on a desktop, a server or a small box,
from its desktop window, a web browser, or remotely through its control API.

> **Status:** milestone 4 (sources, sound and streaming) done, waiting for review: cameras, network
> streams and sound inputs; a sound mixer with meters; streaming to RTMP services and over SRT;
> hardware encoders. See [docs/PLAN.md](docs/PLAN.md), [docs/milestone-4.md](docs/milestone-4.md),
> [docs/milestone-3.md](docs/milestone-3.md) (the GUI) and [docs/api.md](docs/api.md).

## Try it

Linux with GStreamer 1.20 or newer (developed on 1.28) and its development packages:

```
sudo apt install libgstreamer1.0-dev libgstreamer-plugins-base1.0-dev libgstreamer-plugins-bad1.0-dev \
  gstreamer1.0-plugins-good gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly gstreamer1.0-libav \
  gstreamer1.0-pulseaudio
# hardware encoding on Intel graphics (optional; found and tested by itself):
sudo apt install intel-media-va-driver-non-free
go build -o bgamix ./cmd/bgamix
```

The GUI, in a web browser or a desktop window:

```
./bgamix serve examples/test-pip.bgamix          # opens the GUI in your browser
# the desktop window (also needs libgtk-3-dev and libwebkit2gtk-4.1-dev):
go build -tags desktop,production,webkit2_41 -o bgamix-gui ./cmd/bgamix-gui
./bgamix-gui examples/test-pip.bgamix
```

Headless, from the command line:

```
./bgamix validate examples/test-pip.bgamix
./bgamix run examples/test-pip.bgamix            # records examples/recordings/…mp4; Ctrl-C stops
curl -X POST localhost:8740/api/layout -d '{"layout": "side"}'    # in another terminal: switch live
go test ./...                                     # decodes its own recordings and checks the pixels
```

A show is a `.bgamix` file (JSON): the output picture, the feeds (test patterns, video files, still
images, playlists, cameras, network streams, sound inputs), layouts that place them (boxes as
fractions of the picture), recordings and streams. See
`examples/` and [docs/milestone-1.md](docs/milestone-1.md).

## How it works

BGAMix is written in Go and uses [GStreamer](https://gstreamer.freedesktop.org/) as its media
engine. Each feed runs in its own pipeline, so a camera that drops out or a stream that stalls
never stops the mix; BGAMix reconnects it. The mixer composites the feeds, and layouts and
transitions change the picture live without restarting anything.

## Inspired by Snowmix

BGAMix is inspired by [Snowmix](https://snowmix.sourceforge.net/) by Peter Maersk-Moller, a
live video mixer driven by scripts, which showed what such a tool can do. BGAMix is a separate
program written from scratch: it contains no Snowmix code, scripts, configuration, documentation
or media, and it has its own project format, layout model and control interface. It aims at
people who would rather click than script. If Snowmix's scripting suits you, use Snowmix.

## Licence

MIT (see [LICENSE](LICENSE)). BGAMix uses GStreamer (LGPL-2.1, dynamically linked system
libraries) through MIT-licensed Go bindings; every dependency is listed with its licence in
[THIRD-PARTY.md](THIRD-PARTY.md).

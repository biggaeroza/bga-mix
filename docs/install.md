# Installing BGAMix

BGAMix runs on Linux. It uses GStreamer, the media framework that comes with most Linux
distributions, for decoding, mixing, encoding and streaming.

## 1. GStreamer

GStreamer 1.20 or newer, with its good, bad and ugly plugin sets, libav and PulseAudio/PipeWire
support.

**Debian, Ubuntu, Mint, Pop!_OS:**

```
sudo apt install gstreamer1.0-tools gstreamer1.0-plugins-base gstreamer1.0-plugins-good \
  gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly gstreamer1.0-libav gstreamer1.0-pulseaudio
```

**Fedora** (the ugly plugins and libav come from RPM Fusion):

```
sudo dnf install gstreamer1 gstreamer1-plugins-base gstreamer1-plugins-good gstreamer1-plugins-bad-free \
  gstreamer1-plugins-bad-freeworld gstreamer1-plugins-ugly gstreamer1-libav
```

**Arch, Manjaro:**

```
sudo pacman -S gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad gst-plugins-ugly gst-libav
```

The desktop window also needs GTK 3 and WebKitGTK 4.1 (`libwebkit2gtk-4.1-0` on Debian and
Ubuntu, `webkit2gtk4.1` on Fedora, `webkit2gtk-4.1` on Arch). Most desktops have them already.

### Hardware encoding (optional)

BGAMix finds a working hardware encoder by itself and otherwise encodes in software (x264), which
is fine for one 1080p30 output on a recent computer. To use the graphics chip:

- **Intel** (and **AMD**): the VA-API driver, e.g. `sudo apt install intel-media-va-driver-non-free`
  (Intel, 2015 and later) or `mesa-va-drivers` (AMD).
- **NVIDIA:** the proprietary driver; GStreamer's `nvcodec` plugin is in the "bad" set.

Outputs → Encoder shows which encoders work on your computer.

## 2. BGAMix

**From a release:** unpack the archive and run its installer, which puts `bgamix` and
`bgamix-gui` in `~/.local/bin` and BGAMix in your applications menu:

```
tar xzf bgamix-<version>-linux-amd64.tar.gz
cd bgamix-<version>-linux-amd64
./install.sh
```

Check the download against `SHA256SUMS` if you like: `sha256sum -c SHA256SUMS --ignore-missing`.

**From the source** (Go 1.26 or newer, and the development packages):

```
sudo apt install golang libgstreamer1.0-dev libgstreamer-plugins-base1.0-dev \
  libgstreamer-plugins-bad1.0-dev libgtk-3-dev libwebkit2gtk-4.1-dev
git clone https://github.com/biggaeroza/bga-mix && cd bga-mix
go build -o bgamix ./cmd/bgamix
go build -tags desktop,production,webkit2_41 -o bgamix-gui ./cmd/bgamix-gui
```

Release archives are made with `go run ./cmd/release -version <x.y.z>`.

## 3. Start

- `bgamix-gui`: the desktop window (or BGAMix in your applications menu).
- `bgamix serve`: the same GUI in your web browser (on this computer only).
- `bgamix run show.bgamix`: a saved show, without a GUI (a server, a box in a rack), controlled
  through the [control API](api.md).

Then follow the [user guide](guide.md).

## Troubleshooting

- **"no element …" when a show starts:** a GStreamer plugin is missing. `gst-inspect-1.0 <element>`
  tells you whether it is there; install the plugin set above that has it.
- **A camera does not appear:** check that it is in `ls /dev/video*` and that you may use it (on
  some systems your user must be in the `video` group). Another program using the camera keeps
  it busy.
- **No sound inputs listed:** BGAMix reads them from PulseAudio or PipeWire; check that
  `pactl list short sources` lists them.
- **The desktop window stays blank:** some graphics drivers trouble WebKitGTK; try
  `WEBKIT_DISABLE_COMPOSITING_MODE=1 bgamix-gui`, or use `bgamix serve` and a browser.

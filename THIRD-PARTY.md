# Third-party software

BGAMix's own code is MIT-licensed (LICENSE). It uses:

| Component | Licence | How |
|---|---|---|
| [GStreamer](https://gstreamer.freedesktop.org/) and its plugins | LGPL-2.1 or later | dynamically linked system libraries; not distributed with BGAMix's source |
| [go-gst](https://github.com/go-gst/go-gst) v0.0.3 and go-glib v0.0.4 (Go bindings for GStreamer and GLib, generated from GStreamer's GIR files) | MIT | Go modules, linked into the program (cgo). Its handwritten v1.x releases were retracted by the project (LGPL-licensed, "not licensed open enough"); BGAMix uses the current generated, MIT-licensed line |
| Go standard library | BSD-3-Clause | |
| [gorilla/websocket](https://github.com/gorilla/websocket) v1.5.3 | BSD-2-Clause | Go module: the control API's WebSocket |
| [Wails](https://wails.io) v2.16.0 and its dependencies (go.mod: labstack/echo, leaanthony/*, samber/lo, godbus, google/uuid … all MIT, BSD or Apache-2.0) | MIT | Go modules: the desktop window only (`cmd/bgamix-gui`, built with `-tags desktop`) |
| WebKitGTK, GTK 3 | LGPL-2.1 or later | the desktop window's web view on Linux: dynamically linked system libraries |

Each component is added here, with its licence, when it is first used.

**Snowmix** (snowmix.sourceforge.net, Peter Maersk-Moller) inspired BGAMix, but no part of it is
included or was used to write BGAMix.

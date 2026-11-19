#!/bin/sh
# Installs BGAMix for this user: the programs in ~/.local/bin, the desktop entry and icon.
# Run it from the unpacked release folder. GStreamer must be installed (see docs/install.md).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
bin=${PREFIX:-$HOME/.local}/bin
share=${XDG_DATA_HOME:-$HOME/.local/share}
mkdir -p "$bin" "$share/applications" "$share/icons/hicolor/scalable/apps"
for p in bgamix bgamix-gui; do
	[ -f "$here/bin/$p" ] && install -m 755 "$here/bin/$p" "$bin/$p"
done
install -m 644 "$here/packaging/bgamix.svg" "$share/icons/hicolor/scalable/apps/bgamix.svg"
install -m 644 "$here/packaging/bgamix.desktop" "$share/applications/bgamix.desktop"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$share/applications" || true
echo "BGAMix installed in $bin (make sure it is on your PATH)."
command -v gst-inspect-1.0 >/dev/null 2>&1 || echo "Note: GStreamer does not seem to be installed: see docs/install.md."

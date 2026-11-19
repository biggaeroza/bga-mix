// The Feeds tab: the show's sources (test patterns, video files, images, playlists) and their
// settings, including clean ISO recordings.

import { api, h, url, duration, basename, join, dirname, relative, uniqueID, colour, toast, put } from "./api.js";

const TYPES = { test: "Test pattern", file: "Video file", image: "Still image", playlist: "Playlist",
  camera: "Camera", stream: "Network stream", audio: "Sound input" };
const HAS_AUDIO = new Set(["file", "playlist", "stream", "audio"]);
export const hasVideo = (f) => f.type !== "audio";
const AUDIO_MODES = { follow: "Follows the picture (heard while on air)", on: "Always heard", off: "Off" };

// devices: {cameras, audio_inputs}, looked up again when older than a few seconds, so that a
// camera plugged in while BGAMix is open appears
let devices = null, devicesAt = 0;
async function getDevices(refresh) {
  if (!devices || refresh || Date.now() - devicesAt > 4000) {
    try { devices = await api("devices"); } catch { devices = { cameras: [], audio_inputs: [] }; }
    devicesAt = Date.now();
  }
  return devices;
}

// connectedBox lists the cameras and sound inputs plugged in that no feed uses yet, each with
// an Add button.
function connectedBox(ctx) {
  const box = h("div.connected", {}, h("div.dim", {}, "Looking for cameras and sound inputs…"));
  const fill = async (refresh) => {
    const d = await getDevices(refresh);
    const p = ctx.st.project;
    const used = (type, dev) => p.feeds.some((f) => f.type === type && (f.device || (type === "camera" ? "/dev/video0" : "")) === dev);
    const cams = d.cameras.filter((c) => !used("camera", c.device));
    const mics = d.audio_inputs.filter((a) => !used("audio", a.device) && !a.device.endsWith(".monitor"));
    const name = (n) => n.replace(/ \(\/dev\/video\d+\)$/, "");
    put(box,
      h("div.connected-head", {}, h("strong", {}, "Connected"),
        h("button.icon", { title: "Look again", onclick: () => fill(true) }, "⟳")),
      cams.map((c) => h("div.connected-row", {}, h("span.grow", { title: c.device }, "📷\uFE0E ", name(c.name)),
        h("button", { title: "Add it as a feed, with a full-screen layout of its own", onclick: () => {
          const id = addFeed(ctx, { id: "cam", name: name(c.name), type: "camera", device: c.device });
          ctx.edit((p) => p.layouts.push({ id: uniqueID(id, p.layouts.map((l) => l.id)), name: name(c.name), boxes: [{ feed: id, x: 0, y: 0, w: 1, h: 1 }] }));
          toast(`Added ${name(c.name)} and a layout showing it full screen`);
        } }, "Add"))),
      mics.map((a) => h("div.connected-row", {}, h("span.grow", { title: a.device }, "🎙\uFE0E ", a.name),
        h("button", { onclick: () => addFeed(ctx, { id: "mic", name: a.name, type: "audio", device: a.device }) }, "Add"))),
      !cams.length && !mics.length ? h("div.dim", {}, d.cameras.length || d.audio_inputs.length
        ? "Every camera and sound input connected is in use." : "No camera found. Plug one in, then ⟳.") : null);
  };
  fill(false);
  return box;
}
const ENDS = { loop: "Loop", hold: "Hold the last frame", black: "Go black", return: "Return to the previous layout" };

// mediaPath is a feed's file as the server sees it (relative paths are against the show file).
export function mediaPath(st, p) { return join(dirname(st.path), p); }

// thumb is a file's thumbnail (or a placeholder when it has none).
export function thumb(st, p, cls = "thumb") {
  const img = h("img." + cls, { alt: "", loading: "lazy", src: url("media/thumb", { path: mediaPath(st, p) }) });
  img.addEventListener("error", () => img.replaceWith(h("div." + cls + ".none", {}, "no picture")));
  return img;
}

// info writes a file's format into el (probed by the server).
export async function info(st, p, el) {
  el.textContent = "checking…";
  try {
    const m = await api("media?path=" + encodeURIComponent(mediaPath(st, p)));
    put(el, m.ok ? h("span.ok", {}, "✓ decodes") : h("span.bad", {}, "✗ " + (m.error || "does not decode")),
      m.width ? ` · ${m.width}×${m.height}` : "", m.fps ? ` · ${+m.fps.toFixed(2)} fps` : "",
      m.duration_s ? ` · ${duration(m.duration_s)}` : "", m.codec ? ` · ${m.codec}` : "", m.audio ? ` · sound: ${m.audio}` : "");
  } catch (e) { put(el, h("span.bad", {}, "✗ " + e.message)); }
}

export function addFeed(ctx, f) {
  const p = ctx.st.project;
  f.id = uniqueID(f.id || f.name || f.type, p.feeds.map((x) => x.id));
  ctx.edit((p) => p.feeds.push(f));
  ctx.st.sel.feed = f.id;
  return f.id;
}

// renameFeed changes a feed's id and every layout box that shows it.
function renameFeed(p, from, to) {
  for (const l of p.layouts) for (const b of l.boxes) if (b.feed === from) b.feed = to;
  for (const f of p.feeds) if (f.id === from) f.id = to;
}

export function render(el, ctx) {
  const { st } = ctx;
  const p = st.project;
  if (!p.feeds.some((f) => f.id === st.sel.feed)) st.sel.feed = p.feeds[0]?.id;
  const f = p.feeds.find((x) => x.id === st.sel.feed);
  const add = (type) => {
    const nf = { id: type, name: TYPES[type], type };
    if (type === "test") nf.pattern = "smpte";
    if (type === "playlist") nf.items = [];
    if (type === "stream") nf.url = "srt://0.0.0.0:9000?mode=listener";
    if (type === "audio") nf.pattern = "tone";
    addFeed(ctx, nf);
  };
  el.append(h("div.split", {},
    h("aside.list", {},
      h("div.list-head", {}, h("strong", {}, "Feeds"),
        h("select", { "aria-label": "Add a feed", onchange: (e) => { if (e.target.value) add(e.target.value); e.target.value = ""; } },
          h("option", { value: "" }, "+ Add…"), Object.entries(TYPES).map(([k, v]) => h("option", { value: k }, v)))),
      p.feeds.length ? p.feeds.map((x, i) => h("button.item", { class: x.id === st.sel.feed ? "active" : "", onclick: () => { st.sel.feed = x.id; ctx.draw(); } },
        h("span.swatch", { style: { background: colour(x.id) } }),
        h("span.grow", {}, h("div", {}, x.name || x.id), h("div.dim", {}, TYPES[x.type] || x.type, x.path ? " · " + basename(x.path) : "", x.items ? ` · ${x.items.length} file(s)` : "",
          x.url ? " · " + x.url.replace(/^(\w+:\/\/[^/?]*).*/, "$1") : "", x.device ? " · " + basename(x.device) : "")),
        h("span.updown", {},
          h("button.icon", { title: "Up", disabled: i === 0, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.feeds.splice(i - 1, 0, p.feeds.splice(i, 1)[0])); } }, "▲"),
          h("button.icon", { title: "Down", disabled: i === p.feeds.length - 1, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.feeds.splice(i + 1, 0, p.feeds.splice(i, 1)[0])); } }, "▼")))) :
        h("p.dim.pad", {}, "No feeds yet: add one, or add files from the Media tab."),
      connectedBox(ctx)),
    h("section.editor", {}, f ? editor(ctx, f) : h("p.dim", {}, "Choose a feed."))));
}

function field(label, input, hint) {
  return h("label.field", {}, h("span.label", {}, label), input, hint ? h("span.hint", {}, hint) : null);
}

function editor(ctx, f) {
  const { st } = ctx;
  const p = st.project;
  const set = (k, v, redraw = false) => ctx.edit(() => { if (v === "" || v === undefined || v === null) delete f[k]; else f[k] = v; }, redraw);
  const num = (k, v) => set(k, v === "" ? undefined : Math.max(0, +v));
  const browse = async (kind, done) => {
    const path = await ctx.pick(kind, { start: dirname(st.path) });
    if (path) done(relative(st.path, path));
  };
  const usedIn = p.layouts.filter((l) => l.boxes.some((b) => b.feed === f.id)).map((l) => l.name || l.id);
  const idIn = h("input", { type: "text", value: f.id, pattern: "[A-Za-z0-9][A-Za-z0-9_-]*", onchange: (e) => {
    const to = e.target.value.trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$/.test(to)) { toast("An id is letters, digits, - and _", "bad"); e.target.value = f.id; return; }
    if (p.feeds.some((x) => x !== f && x.id === to)) { toast("Another feed has that id", "bad"); e.target.value = f.id; return; }
    ctx.edit((p) => renameFeed(p, f.id, to)); st.sel.feed = to; ctx.draw();
  } });
  const parts = [
    h("div.editor-head", {}, h("h2", {}, f.name || f.id),
      h("button.danger", { onclick: () => {
        if (usedIn.length && !confirm(`${f.name || f.id} is in ${usedIn.join(", ")}. Delete it and its boxes?`)) return;
        ctx.edit((p) => { p.feeds = p.feeds.filter((x) => x !== f); for (const l of p.layouts) l.boxes = l.boxes.filter((b) => b.feed !== f.id); });
      } }, "Delete feed")),
    h("div.grid2", {},
      field("Name", h("input", { type: "text", value: f.name || "", oninput: (e) => set("name", e.target.value) })),
      field("Id", idIn, "used by layouts and the control API"),
      field("Type", h("select", { onchange: (e) => {
        const t = e.target.value;
        ctx.edit(() => {
          f.type = t;
          for (const k of ["pattern", "path", "in_s", "out_s", "end", "items", "device", "width", "height", "fps", "url", "latency_ms", "audio", "volume_db", "mute"]) delete f[k];
          if (t === "test") f.pattern = "smpte";
          if (t === "playlist") f.items = [];
          if (t === "stream") f.url = "srt://0.0.0.0:9000?mode=listener";
          if (t === "audio") f.pattern = "tone";
          if (t === "audio") { delete f.iso; for (const l of p.layouts) l.boxes = l.boxes.filter((b) => b.feed !== f.id); }
        });
      } }, Object.entries(TYPES).map(([k, v]) => h("option", { value: k, selected: k === f.type }, v))))),
  ];
  if (f.type === "test") {
    parts.push(h("div.grid2", {}, field("Pattern", h("select", { onchange: (e) => set("pattern", e.target.value) },
      (st.meta.patterns || []).map((x) => h("option", { value: x, selected: x === (f.pattern || "smpte") }, x))))));
  }
  if (f.type === "file" || f.type === "image") {
    const infoEl = h("div.info", {});
    const kind = f.type === "image" ? "image" : "media";
    const pathIn = h("input.grow", { type: "text", value: f.path || "", placeholder: "a file (relative to the show file, or absolute)",
      onchange: (e) => { set("path", e.target.value.trim(), true); } });
    parts.push(h("div.media-row", {}, f.path ? thumb(st, f.path) : h("div.thumb.none", {}, "no file"),
      h("div.grow", {},
        field("File", h("div.row", {}, pathIn, h("button", { onclick: () => browse(kind, (v) => set("path", v, true)) }, "Browse…"))),
        infoEl)));
    if (f.path) info(st, f.path, infoEl);
  }
  if (f.type === "file") {
    parts.push(h("div.grid3", {},
      field("In point (s)", h("input", { type: "number", min: 0, step: 0.1, value: f.in_s ?? "", placeholder: "0", onchange: (e) => num("in_s", e.target.value) })),
      field("Out point (s)", h("input", { type: "number", min: 0, step: 0.1, value: f.out_s ?? "", placeholder: "the end", onchange: (e) => num("out_s", e.target.value) })),
      field("At the end", endSelect(f, set))));
  }
  if (f.type === "playlist") {
    const items = f.items || (f.items = []);
    parts.push(h("div.field", {}, h("span.label", {}, "Files, played in turn"),
      h("ol.items", {}, items.map((it, i) => h("li", {},
        thumb(st, it, "mini"), h("span.grow", { title: it }, basename(it)),
        h("button.icon", { title: "Up", disabled: i === 0, onclick: () => ctx.edit(() => items.splice(i - 1, 0, items.splice(i, 1)[0])) }, "▲"),
        h("button.icon", { title: "Down", disabled: i === items.length - 1, onclick: () => ctx.edit(() => items.splice(i + 1, 0, items.splice(i, 1)[0])) }, "▼"),
        h("button.icon", { title: "Remove", onclick: () => ctx.edit(() => items.splice(i, 1)) }, "×")))),
      h("div.row", {}, h("button", { onclick: () => browse("media", (v) => ctx.edit(() => items.push(v))) }, "Add a file…"),
        h("span.dim", {}, "or add files from the Media tab"))),
      h("div.grid3", {}, field("At the end", endSelect(f, set))));
  }
  if (f.type === "camera") {
    const sel = h("select", { onchange: (e) => set("device", e.target.value) }, h("option", { value: f.device || "" }, f.device || "/dev/video0 (the first camera)"));
    getDevices().then((d) => put(sel, h("option", { value: "", selected: !f.device }, "/dev/video0 (the first camera)"),
      d.cameras.map((c) => h("option", { value: c.device, selected: c.device === f.device }, c.name)),
      f.device && !d.cameras.some((c) => c.device === f.device) ? h("option", { value: f.device, selected: true }, f.device + " (not connected)") : null));
    parts.push(h("div.grid2", {},
      field("Camera", h("div.row", {}, sel, h("button", { title: "Look for cameras again", onclick: async () => { await getDevices(true); ctx.draw(); } }, "Refresh"))),
      field("Picture asked of it", h("div.row", {},
        h("input.num", { type: "number", min: 0, step: 2, value: f.width || "", placeholder: "width", onchange: (e) => set("width", +e.target.value || undefined), "aria-label": "Width" }), "×",
        h("input.num", { type: "number", min: 0, step: 2, value: f.height || "", placeholder: "height", onchange: (e) => set("height", +e.target.value || undefined), "aria-label": "Height" }), "at",
        h("input.num", { type: "number", min: 0, value: f.fps || "", placeholder: "fps", onchange: (e) => set("fps", +e.target.value || undefined), "aria-label": "Frame rate" })),
        "empty: the camera's own; raw or MJPEG, whichever it gives")));
  }
  if (f.type === "stream") {
    parts.push(h("div.grid2", {},
      field("URL", h("input", { type: "text", value: f.url || "", placeholder: "srt://0.0.0.0:9000?mode=listener", oninput: (e) => set("url", e.target.value.trim()) }),
        "srt://host:port (?mode=listener to wait for the sender), rtsp://camera/stream, udp://0.0.0.0:5000 (MPEG-TS), rtmp://…, http(s)://… (HLS)"),
      field("Buffer (ms)", h("input.num", { type: "number", min: 0, max: 10000, step: 20, value: f.latency_ms || "", placeholder: "200", onchange: (e) => set("latency_ms", +e.target.value || undefined) }),
        "SRT and RTSP: more rides out a poor network, less is closer to live")),
      h("p.dim", {}, "A stream that drops or ends is reconnected (after 1 s, 2 s, 4 s … up to 30 s); its box is left transparent meanwhile."));
  }
  if (f.type === "audio") {
    const sel = h("select", { onchange: (e) => {
      const v = e.target.value;
      ctx.edit(() => { if (v === "\u0000tone") { f.pattern = "tone"; delete f.device; } else { delete f.pattern; if (v) f.device = v; else delete f.device; } });
    } }, h("option", {}, "…"));
    getDevices().then((d) => put(sel,
      h("option", { value: "\u0000tone", selected: f.pattern === "tone" }, "Test tone (440 Hz)"),
      h("option", { value: "", selected: f.pattern !== "tone" && !f.device }, "The system's input"),
      d.audio_inputs.map((a) => h("option", { value: a.device, selected: a.device === f.device }, a.name)),
      f.device && !d.audio_inputs.some((a) => a.device === f.device) ? h("option", { value: f.device, selected: true }, f.device + " (not found)") : null));
    parts.push(h("div.grid2", {}, field("Input", h("div.row", {}, sel, h("button", { onclick: async () => { await getDevices(true); ctx.draw(); } }, "Refresh")),
      "a microphone, a line input, or the monitor of an output")),
      h("p.dim", {}, "Sound only: it has no picture, so it is not placed in layouts. It is heard always unless set otherwise below."));
  }
  if (HAS_AUDIO.has(f.type)) {
    const def = f.type === "audio" ? "on" : "follow";
    parts.push(h("fieldset", {}, h("legend", {}, "Sound"), h("div.grid3", {},
      field("Heard", h("select", { onchange: (e) => set("audio", e.target.value === def ? undefined : e.target.value) },
        Object.entries(AUDIO_MODES).map(([k, v]) => h("option", { value: k, selected: (f.audio || def) === k }, v)))),
      field("Level (dB)", h("input.num", { type: "number", min: -60, max: 12, step: 0.5, value: f.volume_db ?? "", placeholder: "0",
        onchange: (e) => set("volume_db", e.target.value === "" || +e.target.value === 0 ? undefined : Math.max(-60, Math.min(12, +e.target.value))) }), "-60 to +12; changed live in the switcher"),
      h("label.field.check", {}, h("input", { type: "checkbox", checked: !!f.mute, onchange: (e) => set("mute", e.target.checked || undefined) }), " Start muted"))));
  }
  // ISO
  const iso = f.iso;
  if (hasVideo(f)) parts.push(h("fieldset", {}, h("legend", {}, h("label", {}, h("input", { type: "checkbox", checked: !!iso, onchange: (e) => ctx.edit(() => {
    if (e.target.checked) f.iso = { path: "recordings/{project}-{feed}-{date}-{time}" }; else delete f.iso;
  }) }), " Record this feed on its own (ISO, clean)")),
  iso ? h("div.grid3", {},
    field("File", h("input", { type: "text", value: iso.path, oninput: (e) => ctx.edit(() => { iso.path = e.target.value; }, false) }), "{project} {feed} {date} {time}"),
    field("Container", h("select", { onchange: (e) => ctx.edit(() => { iso.container = e.target.value; }, false) },
      ["mp4", "mkv", "mov"].map((c) => h("option", { selected: (iso.container || "mp4") === c }, c)))),
    field("Start", h("select", { onchange: (e) => ctx.edit(() => { if (e.target.value === "1") iso.manual = true; else delete iso.manual; }, false) },
      h("option", { value: "", selected: !iso.manual }, "with the show"), h("option", { value: "1", selected: !!iso.manual }, "by hand")))) : null));
  if (hasVideo(f)) parts.push(h("p.dim", {}, usedIn.length ? "Shown in: " + usedIn.join(", ") : "Not in any layout yet (Layouts tab)."));
  return parts;
}

function endSelect(f, set) {
  return h("select", { onchange: (e) => set("end", e.target.value === "loop" ? undefined : e.target.value) },
    Object.entries(ENDS).map(([k, v]) => h("option", { value: k, selected: (f.end || "loop") === k }, v)));
}

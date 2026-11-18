// The Feeds tab: the show's sources (test patterns, video files, images, playlists) and their
// settings, including clean ISO recordings.

import { api, h, url, duration, basename, join, dirname, relative, uniqueID, colour, toast, put } from "./api.js";

const TYPES = { test: "Test pattern", file: "Video file", image: "Still image", playlist: "Playlist" };
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
    addFeed(ctx, nf);
  };
  el.append(h("div.split", {},
    h("aside.list", {},
      h("div.list-head", {}, h("strong", {}, "Feeds"),
        h("select", { "aria-label": "Add a feed", onchange: (e) => { if (e.target.value) add(e.target.value); e.target.value = ""; } },
          h("option", { value: "" }, "+ Add…"), Object.entries(TYPES).map(([k, v]) => h("option", { value: k }, v)))),
      p.feeds.length ? p.feeds.map((x, i) => h("button.item", { class: x.id === st.sel.feed ? "active" : "", onclick: () => { st.sel.feed = x.id; ctx.draw(); } },
        h("span.swatch", { style: { background: colour(x.id) } }),
        h("span.grow", {}, h("div", {}, x.name || x.id), h("div.dim", {}, TYPES[x.type] || x.type, x.path ? " · " + basename(x.path) : "", x.items ? ` · ${x.items.length} file(s)` : "")),
        h("span.updown", {},
          h("button.icon", { title: "Up", disabled: i === 0, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.feeds.splice(i - 1, 0, p.feeds.splice(i, 1)[0])); } }, "▲"),
          h("button.icon", { title: "Down", disabled: i === p.feeds.length - 1, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.feeds.splice(i + 1, 0, p.feeds.splice(i, 1)[0])); } }, "▼")))) :
        h("p.dim.pad", {}, "No feeds yet: add one, or add files from the Media tab.")),
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
          for (const k of ["pattern", "path", "in_s", "out_s", "end", "items"]) delete f[k];
          if (t === "test") f.pattern = "smpte";
          if (t === "playlist") f.items = [];
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
  // ISO
  const iso = f.iso;
  parts.push(h("fieldset", {}, h("legend", {}, h("label", {}, h("input", { type: "checkbox", checked: !!iso, onchange: (e) => ctx.edit(() => {
    if (e.target.checked) f.iso = { path: "recordings/{project}-{feed}-{date}-{time}" }; else delete f.iso;
  }) }), " Record this feed on its own (ISO, clean)")),
  iso ? h("div.grid3", {},
    field("File", h("input", { type: "text", value: iso.path, oninput: (e) => ctx.edit(() => { iso.path = e.target.value; }, false) }), "{project} {feed} {date} {time}"),
    field("Container", h("select", { onchange: (e) => ctx.edit(() => { iso.container = e.target.value; }, false) },
      ["mp4", "mkv", "mov"].map((c) => h("option", { selected: (iso.container || "mp4") === c }, c)))),
    field("Start", h("select", { onchange: (e) => ctx.edit(() => { if (e.target.value === "1") iso.manual = true; else delete iso.manual; }, false) },
      h("option", { value: "", selected: !iso.manual }, "with the show"), h("option", { value: "1", selected: !!iso.manual }, "by hand")))) : null));
  parts.push(h("p.dim", {}, usedIn.length ? "Shown in: " + usedIn.join(", ") : "Not in any layout yet (Layouts tab)."));
  return parts;
}

function endSelect(f, set) {
  return h("select", { onchange: (e) => set("end", e.target.value === "loop" ? undefined : e.target.value) },
    Object.entries(ENDS).map(([k, v]) => h("option", { value: k, selected: (f.end || "loop") === k }, v)));
}

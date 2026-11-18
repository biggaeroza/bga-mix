// The Media tab: the library of video files and images in a folder, each probed (format,
// duration, whether it decodes) with a thumbnail; files become feeds or playlist items.

import { api, h, duration, bytes, basename, dirname, relative, toast, put } from "./api.js";
import { thumb, addFeed } from "./feeds.js";

let queue = [], running = 0;
// probe one file after another (two at a time): a folder of long files would otherwise start
// many decoders at once
function probeLater(path, done) {
  queue.push([path, done]);
  pump();
}
function pump() {
  while (running < 2 && queue.length) {
    const [path, done] = queue.shift();
    running++;
    api("media?path=" + encodeURIComponent(path)).then(done, (e) => done({ ok: false, error: e.message }))
      .finally(() => { running--; pump(); });
  }
}

export function render(el, ctx) {
  const { st } = ctx;
  queue = [];
  if (st.sel.mediaDir === undefined) st.sel.mediaDir = dirname(st.path) || st.meta.home || "";
  const grid = h("div.cards", {});
  const head = h("div.toolbar", {});
  el.append(head, grid);
  const playlists = () => st.project.feeds.filter((f) => f.type === "playlist");
  const load = async (dir) => {
    try {
      const l = await api("files?dir=" + encodeURIComponent(dir));
      st.sel.mediaDir = l.dir;
      const dirs = l.entries.filter((e) => e.dir), files = l.entries.filter((e) => e.kind === "video" || e.kind === "image");
      put(head, 
        h("strong", {}, "Folder"), h("code.path", { title: l.dir }, l.dir),
        l.parent ? h("button", { onclick: () => load(l.parent) }, "↰ Up") : null,
        h("button", { onclick: async () => { const d = await ctx.pick("folder", { start: l.dir }); if (d) load(d); } }, "Choose folder…"),
        st.path ? h("button", { onclick: () => load(dirname(st.path)) }, "Show's folder") : null,
        h("span.grow", {}),
        h("span.dim", {}, `${files.length} file(s)`),
        dirs.length ? h("div.chips", {}, dirs.map((d) => h("button.chip", { onclick: () => load(d.path) }, "📁 " + d.name))) : null);
      put(grid, ...files.map((e) => card(ctx, e, playlists)));
      if (!files.length) grid.append(h("p.dim.pad", {}, "No video files or images in this folder."));
    } catch (err) { put(head, h("span.bad", {}, err.message), h("button", { onclick: () => load(st.meta.home) }, "Home folder")); }
  };
  load(st.sel.mediaDir);
}

function card(ctx, e, playlists) {
  const { st } = ctx;
  const meta = h("div.dim", {}, "checking…");
  const status = h("span.status", {}, "…");
  const rel = () => relative(st.path, e.path);
  const used = st.project.feeds.filter((f) => f.path && (f.path === rel() || f.path === e.path) || (f.items || []).some((it) => it === rel() || it === e.path));
  const actions = h("div.row.wrap", {});
  const addAs = (type) => {
    const id = addFeed(ctx, { id: basename(e.path), name: basename(e.path).replace(/\.[^.]+$/, ""), type, path: rel() });
    toast(`Added the feed “${id}”`);
  };
  const fill = (m) => {
    put(status, m.ok ? h("span.ok", { title: "decodes" }, "✓") : h("span.bad", { title: m.error || "does not decode" }, "✗"));
    put(meta, m.ok ? [m.kind === "image" ? "image" : duration(m.duration_s), m.width ? ` · ${m.width}×${m.height}` : "",
      m.fps ? ` · ${+m.fps.toFixed(2)} fps` : "", h("br"), m.codec || "", m.audio ? " · sound" : "", " · ", bytes(m.size)]
      : h("span.bad", {}, m.error || "does not decode"));
    put(actions, 
      m.kind === "image" ? h("button", { disabled: !m.ok, onclick: () => addAs("image") }, "Add as image feed")
        : h("button", { disabled: !m.ok, onclick: () => addAs("file") }, "Add as video feed"),
      m.kind !== "image" && playlists().length ? h("select", { disabled: !m.ok, "aria-label": "Add to a playlist", onchange: (ev) => {
        const f = st.project.feeds.find((x) => x.id === ev.target.value);
        ev.target.value = "";
        if (!f) return;
        ctx.edit(() => { (f.items ||= []).push(rel()); }, false);
        toast(`Added to the playlist “${f.name || f.id}”`);
      } }, h("option", { value: "" }, "Add to playlist…"), playlists().map((f) => h("option", { value: f.id }, f.name || f.id))) : null);
  };
  probeLater(e.path, fill);
  return h("div.card", {},
    h("div.card-pic", {}, thumb(st, e.path), status),
    h("div.card-body", {}, h("div.name", { title: e.path }, e.name), meta,
      used.length ? h("div.dim", {}, "in: " + used.map((f) => f.name || f.id).join(", ")) : null, actions));
}

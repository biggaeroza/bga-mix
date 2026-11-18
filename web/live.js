// The Live tab: the switcher. The program and every feed as live previews; the layouts as
// buttons (keys 1-9) taken with a cut or a mix; transport for files and playlists; the
// recordings. Built once per show and updated in place from the studio's status (polled).

import { api, h, url, duration, bytes, basename, colour, toast, put } from "./api.js";
import { mini } from "./layouts.js";

let built = null; // {el, parts…} while the tab shows a running show
let keyHandler = null;
const loops = new Set();

export function unmount() {
  for (const l of loops) l.stop = true;
  loops.clear();
  if (keyHandler) window.removeEventListener("keydown", keyHandler);
  keyHandler = null;
  built = null;
}

// preview is an <img> that loads the next JPEG when the last one arrived, at the preview rate.
function preview(key, fps, cls) {
  const img = h("img." + cls, { alt: key === "program" ? "Program" : key });
  const loop = { stop: false };
  loops.add(loop);
  const next = () => {
    if (loop.stop || !img.isConnected && img.dataset.started) return;
    img.dataset.started = "1";
    img.src = url("preview/" + encodeURIComponent(key), { t: Date.now() });
  };
  img.addEventListener("load", () => { img.classList.remove("waiting"); setTimeout(next, 1000 / fps); });
  img.addEventListener("error", () => { img.classList.add("waiting"); setTimeout(next, 500); });
  setTimeout(next, 0);
  return img;
}

export function render(el, ctx) {
  unmount();
  const { st } = ctx;
  if (!st.studio.running) return idle(el, ctx);
  const s = st.studio.status;
  const fps = st.meta.preview_fps || 5;
  const p = st.project;
  const trans = st.sel.take || { mode: "default", duration_s: p.transition?.duration_s || 0.5 };
  st.sel.take = trans;
  const transition = () => trans.mode === "cut" ? { kind: "cut" } : trans.mode === "mix" ? { kind: "mix", duration_s: trans.duration_s } : null;
  const take = (id) => ctx.take(id, transition());

  const progInfo = h("div.prog-info", {});
  const layoutBtns = s.layouts.map((l, i) => {
    const pl = p.layouts.find((x) => x.id === l.id);
    return h("button.take", { "data-id": l.id, onclick: () => take(l.id), title: `Take ${l.name || l.id}${i < 9 ? ` (key ${i + 1})` : ""}` },
      pl ? mini(p, pl, "mini-take") : h("div.mini-take"), h("span", {}, i < 9 ? h("kbd", {}, i + 1) : null, " ", l.name || l.id));
  });
  const mode = h("div.seg", {}, [["default", `Default (${p.transition?.kind === "mix" ? "mix" : "cut"})`], ["cut", "Cut"], ["mix", "Mix"]].map(([m, n]) =>
    h("button", { class: trans.mode === m ? "active" : "", onclick: (e) => { trans.mode = m; for (const b of mode.children) b.classList.toggle("active", b === e.currentTarget); } }, n)));
  const dur = h("input.num", { type: "number", min: 0.05, max: 60, step: 0.1, value: trans.duration_s, "aria-label": "Mix duration", onchange: (e) => { trans.duration_s = Math.min(60, Math.max(0.05, +e.target.value || 0.5)); } });

  const feedCards = s.feeds.map((f) => feedCard(ctx, f, fps));
  const recRows = h("div.recs", {});
  el.append(h("div.live", {},
    h("section.program", {},
      h("div.prog-frame", { style: { aspectRatio: `${s.output.width} / ${s.output.height}` } }, preview("program", fps, "prog"), h("span.onair-tag", {}, "PROGRAM")),
      progInfo),
    h("aside.side", {},
      h("section.panel", {}, h("h3", {}, "Transition"), h("div.row.wrap", {}, mode, h("label.row", {}, "mix ", dur, " s"))),
      h("section.panel", {}, h("h3", {}, "Recordings"), recRows)),
    h("section.layouts-row", {}, h("h3", {}, "Layouts"), h("div.takes", {}, layoutBtns)),
    h("section.feeds-row", {}, h("h3", {}, "Feeds"), h("div.feed-cards", {}, feedCards.map((c) => c.el)))));

  built = { progInfo, layoutBtns, feedCards, recRows, recKey: "" };
  keyHandler = (e) => {
    if (st.tab !== "live" || e.ctrlKey || e.metaKey || e.altKey || /INPUT|SELECT|TEXTAREA/.test(document.activeElement?.tagName)) return;
    const n = +e.key;
    if (n >= 1 && n <= 9 && s.layouts[n - 1]) { e.preventDefault(); take(s.layouts[n - 1].id); }
  };
  window.addEventListener("keydown", keyHandler);
  update(ctx);
}

function idle(el, ctx) {
  const { st } = ctx;
  const p = st.project;
  const recs = (p.recordings || []).length + p.feeds.filter((f) => f.iso).length;
  el.append(h("div.idle-page", {},
    h("h2", {}, "The show is not running"),
    h("p", {}, `${p.feeds.length} feed(s), ${p.layouts.length} layout(s), ${recs} recording(s) · ${p.output.width}×${p.output.height} at ${p.output.fps} fps`),
    st.problems.length ? [h("p.bad", {}, "Fix these first:"), h("ul.problems", {}, st.problems.map((x) => h("li", {}, x)))] : null,
    h("button.go.big", { disabled: st.problems.length > 0, onclick: () => ctx.start() }, "▶ Start show"),
    h("p.dim", {}, "Starting runs every feed, the mixer with the start layout, the previews here and the recordings that start with the show. The show runs the project as it is when started; it does not need saving first.")));
}

function feedCard(ctx, f, fps) {
  const isFile = f.type === "file" || f.type === "playlist";
  const state = h("span.state", {});
  const err = h("div.err", {});
  const pos = h("span.pos", {});
  let seeking = false;
  const seek = h("input.seek", { type: "range", min: 0, max: 1, step: 0.01, value: 0, "aria-label": "Position",
    onpointerdown: () => { seeking = true; }, onchange: (e) => { seeking = false; cmd("seek", +e.target.value); } });
  const cmd = async (c, seconds) => {
    try { await api(`feeds/${encodeURIComponent(f.id)}/${c}`, seconds === undefined ? {} : { seconds }); }
    catch (e) { toast(e.message, "bad"); }
  };
  const btn = (c, label, title) => h("button.icon", { title, onclick: () => cmd(c) }, label);
  const el = h("div.feed-card", { style: { "--c": colour(f.id) } },
    h("div.feed-pic", {}, preview(f.id, fps, "feed"), h("span.feed-name", {}, f.name || f.id)),
    h("div.feed-bar", {}, state,
      isFile ? h("span.transport", {},
        f.type === "playlist" ? btn("previous", "⏮\uFE0E", "Previous item") : null,
        btn("play", "▶", "Play"), btn("pause", "⏸\uFE0E", "Pause"), btn("restart", "⟲", "From the start"),
        f.type === "playlist" ? btn("next", "⏭\uFE0E", "Next item") : null) : btn("restart", "⟲", "Restart the feed")),
    isFile ? h("div.seek-row", {}, seek, pos) : null,
    err);
  return { id: f.id, el, update(fs) {
    state.className = "state " + fs.state;
    state.textContent = fs.state + (fs.items ? ` ${fs.item}/${fs.items}` : "") + (fs.restarts ? ` · ${fs.restarts} restart(s)` : "");
    err.textContent = fs.error || "";
    if (isFile) {
      if (fs.duration_s > 0) { seek.max = fs.duration_s; if (!seeking) seek.value = fs.position_s || 0; }
      pos.textContent = `${duration(fs.position_s)} / ${duration(fs.duration_s)}`;
    }
  } };
}

export function update(ctx) {
  const { st } = ctx;
  if (!built || !st.studio.running) return;
  const s = st.studio.status;
  const cur = s.layouts.find((l) => l.id === s.layout);
  put(built.progInfo, 
    h("span", {}, "On air: ", h("strong", {}, cur?.name || s.layout)),
    s.transitioning ? h("span.badge.warn", {}, "mixing") : null,
    s.previous ? h("span.dim", {}, " · before: " + (s.layouts.find((l) => l.id === s.previous)?.name || s.previous)) : null,
    h("span.grow", {}),
    h("span.dim", {}, `${s.output.width}×${s.output.height} · ${s.output.fps} fps · up ${duration(s.uptime_s)}`));
  for (const b of built.layoutBtns) b.classList.toggle("onair", b.dataset.id === s.layout);
  for (const c of built.feedCards) { const fs = s.feeds.find((x) => x.id === c.id); if (fs) c.update(fs); }
  // recordings: rebuilt when their states change, else their figures updated
  const key = s.recordings.map((r) => r.label + r.state).join();
  if (key !== built.recKey) {
    built.recKey = key;
    put(built.recRows, ...(s.recordings.length ? s.recordings.map((r) => recRow(r)) : [h("p.dim", {}, "None in this show.")]));
  }
  for (const r of s.recordings) {
    const row = built.recRows.querySelector(`[data-label="${r.label}"] .figs`);
    if (row) row.textContent = r.state === "recording" || r.state === "paused" ? `${duration(r.elapsed_s)} · ${bytes(r.bytes)}` : r.files?.length ? `${r.files.length} file(s)` : "";
  }
}

function recRow(r) {
  const cmd = async (c) => {
    if (c === "stop" && !confirm(`Stop the recording ${r.label}?`)) return;
    try { await api(`recordings/${encodeURIComponent(r.label)}/${c}`, {}); } catch (e) { toast(e.message, "bad"); }
  };
  const on = r.state === "recording", paused = r.state === "paused";
  return h("div.rec-row", { "data-label": r.label },
    h("span.recdot", { class: on ? "on" : paused ? "paused" : "" }),
    h("div.grow", {}, h("div", {}, r.feed ? `ISO ${r.feed}` : r.label, " ", h("span.state", { class: r.state }, r.state)),
      h("div.dim.path", { title: r.path || (r.files || []).join("\n") }, basename(r.path || (r.files || []).slice(-1)[0] || "")),
      h("div.figs.dim", {}), r.error ? h("div.err", {}, r.error) : null),
    h("div.row", {},
      on || paused ? null : h("button.rec", { onclick: () => cmd("start") }, "● Record"),
      on ? h("button", { onclick: () => cmd("pause") }, "Pause") : null,
      paused ? h("button", { onclick: () => cmd("resume") }, "Resume") : null,
      on || paused ? h("button", { onclick: () => cmd("stop") }, "■ Stop") : null));
}

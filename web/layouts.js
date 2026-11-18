// The Layouts tab: layouts (scenes) and their boxes, dragged and resized on a canvas the shape
// of the output. Boxes snap to the edges, centre lines and other boxes (hold Alt not to snap);
// arrow keys nudge the chosen box (Shift: ten times more), Delete removes it.

import { h, url, colour, uniqueID, toast, put } from "./api.js";
import { thumb, hasVideo } from "./feeds.js";

const SNAP = 0.012;         // fraction of the picture within which edges snap
const MIN = 0.02;           // the smallest box

const TEMPLATES = {
  full: { name: "Full screen", boxes: (f) => f.slice(0, 1).map((id) => box(id, 0, 0, 1, 1)) },  // f: the feeds with a picture
  pip: { name: "Picture in picture", boxes: (f) => [box(f[0], 0, 0, 1, 1), box(f[1] ?? f[0], 0.66, 0.04, 0.3, 0.3)].filter((b) => b.feed) },
  side: { name: "Side by side", boxes: (f) => [box(f[0], 0, 0.25, 0.5, 0.5), box(f[1] ?? f[0], 0.5, 0.25, 0.5, 0.5)].filter((b) => b.feed) },
  grid: { name: "Grid 2×2", boxes: (f) => f.slice(0, 4).map((id, i) => box(id, (i % 2) * 0.5, Math.floor(i / 2) * 0.5, 0.5, 0.5)) },
  empty: { name: "Empty", boxes: () => [] },
};
function box(feed, x, y, w, hh) { return { feed, x, y, w, h: hh }; }
const r4 = (v) => Math.round(v * 10000) / 10000;

let keyHandler = null, liveTimer = null;
export function unmount() {
  if (keyHandler) window.removeEventListener("keydown", keyHandler);
  keyHandler = null;
  clearInterval(liveTimer);
}

export function render(el, ctx) {
  unmount();
  const { st } = ctx;
  const p = st.project;
  if (!p.layouts.some((l) => l.id === st.sel.layout)) st.sel.layout = p.layouts[0]?.id;
  const l = p.layouts.find((x) => x.id === st.sel.layout);
  const startID = p.start_layout || p.layouts[0]?.id;
  const addLayout = (t) => {
    const tpl = TEMPLATES[t];
    const id = uniqueID(t, p.layouts.map((x) => x.id));
    ctx.edit((p) => p.layouts.push({ id, name: tpl.name, boxes: tpl.boxes(p.feeds.filter(hasVideo).map((f) => f.id)) }));
    st.sel.layout = id; st.sel.box = 0; ctx.draw();
  };
  el.append(h("div.split", {},
    h("aside.list", {},
      h("div.list-head", {}, h("strong", {}, "Layouts"),
        h("select", { "aria-label": "Add a layout", onchange: (e) => { if (e.target.value) addLayout(e.target.value); e.target.value = ""; } },
          h("option", { value: "" }, "+ Add…"), Object.entries(TEMPLATES).map(([k, v]) => h("option", { value: k }, v.name)))),
      p.layouts.map((x, i) => h("button.item", { class: x.id === st.sel.layout ? "active" : "", onclick: () => { st.sel.layout = x.id; st.sel.box = undefined; ctx.draw(); } },
        mini(p, x),
        h("span.grow", {}, h("div", {}, x.name || x.id, x.id === startID ? h("span.badge", { title: "The show starts with this layout" }, "start") : null),
          h("div.dim", {}, `${i < 9 ? `key ${i + 1} · ` : ""}${x.boxes.length} box(es)`)),
        h("span.updown", {},
          h("button.icon", { title: "Up", disabled: i === 0, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.layouts.splice(i - 1, 0, p.layouts.splice(i, 1)[0])); } }, "▲"),
          h("button.icon", { title: "Down", disabled: i === p.layouts.length - 1, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.layouts.splice(i + 1, 0, p.layouts.splice(i, 1)[0])); } }, "▼")))),
      transitionBox(ctx)),
    h("section.editor", {}, l ? editor(ctx, l, startID) : h("p.dim", {}, "Add a layout."))));
}

// mini is a small drawing of a layout.
export function mini(p, l, cls = "mini-layout") {
  return h("div." + cls, { style: { aspectRatio: `${p.output.width} / ${p.output.height}` } },
    l.boxes.map((b, i) => h("span", { style: { left: b.x * 100 + "%", top: b.y * 100 + "%", width: b.w * 100 + "%", height: b.h * 100 + "%", zIndex: i + 1, background: colour(b.feed), opacity: b.alpha ?? 1 } })));
}

function transitionBox(ctx) {
  const p = ctx.st.project;
  const t = p.transition || { kind: "cut" };
  return h("fieldset.transition", {}, h("legend", {}, "Default transition"),
    h("div.row", {},
      h("select", { "aria-label": "Transition", onchange: (e) => ctx.edit((p) => {
        p.transition = e.target.value === "mix" ? { kind: "mix", duration_s: t.duration_s || 0.5 } : { kind: "cut" };
      }) }, h("option", { value: "cut", selected: t.kind !== "mix" }, "Cut"), h("option", { value: "mix", selected: t.kind === "mix" }, "Mix")),
      t.kind === "mix" ? [h("input.num", { type: "number", min: 0.05, max: 60, step: 0.1, value: t.duration_s || 0.5, "aria-label": "Mix duration",
        onchange: (e) => ctx.edit((p) => { p.transition.duration_s = Math.min(60, Math.max(0.05, +e.target.value || 0.5)); }, false) }), " s"] : null),
    h("p.dim", {}, "Used when switching live unless the switcher says otherwise."));
}

function editor(ctx, l, startID) {
  const { st } = ctx;
  const p = st.project;
  const o = p.output;
  const feedName = (id) => p.feeds.find((f) => f.id === id)?.name || id;
  if (st.sel.box !== undefined && !l.boxes[st.sel.box]) st.sel.box = undefined;
  // as wide as there is room for, but never taller than 58 % of the window
  const stage = h("div.stage", { style: { aspectRatio: `${o.width} / ${o.height}`, width: `min(100%, calc(58vh * ${o.width} / ${o.height}))` }, tabindex: 0, "aria-label": "Layout canvas" });
  const boxEls = [];
  const place = (i) => {
    const b = l.boxes[i], el = boxEls[i];
    Object.assign(el.style, { left: b.x * 100 + "%", top: b.y * 100 + "%", width: b.w * 100 + "%", height: b.h * 100 + "%", zIndex: i + 1, opacity: b.alpha ?? 1 });
    el.querySelector(".geo").textContent = `${Math.round(b.x * o.width)},${Math.round(b.y * o.height)} · ${Math.round(b.w * o.width)}×${Math.round(b.h * o.height)}`;
  };
  const select = (i) => {
    st.sel.box = i;
    boxEls.forEach((el, k) => el.classList.toggle("sel", k === i));
    table.querySelectorAll("tr[data-i]").forEach((tr) => tr.classList.toggle("sel", +tr.dataset.i === i));
  };
  const lines = h("div.guides", {});
  // snapping: v moves to the nearest candidate line within SNAP (and the line is shown)
  const snap = (v, cands, guides, axis) => {
    let best = null;
    for (const c of cands) if (Math.abs(c - v) < SNAP && (best === null || Math.abs(c - v) < Math.abs(best - v))) best = c;
    if (best !== null) guides.push([axis, best]);
    return best ?? v;
  };
  // a moved box snaps by its near edge or its far edge, whichever is closer to a line
  const snapPos = (v, size, s) => {
    const a = s(v), b = s(v + size) - size;
    if (a === v) return b;
    if (b === v) return a;
    return Math.abs(a - v) <= Math.abs(b - v) ? a : b;
  };
  const showGuides = (gs) => put(lines, ...gs.map(([axis, v]) => h("span", { class: axis, style: axis === "v" ? { left: v * 100 + "%" } : { top: v * 100 + "%" } })));

  l.boxes.forEach((b, i) => {
    const f = p.feeds.find((x) => x.id === b.feed);
    const pic = !f ? null : livePic(ctx, f) || (f.path && (f.type === "file" || f.type === "image") ? thumb(st, f.path, "fill") : null);
    const el = h("div.box", { style: { "--c": colour(b.feed) } },
      pic, h("span.tag", {}, `${i + 1} · ${feedName(b.feed)}`, f ? "" : " (no such feed)"), h("span.geo", {}),
      ["nw", "ne", "sw", "se"].map((c) => h("span.handle." + c, { "data-c": c })));
    el.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      stage.focus();
      select(i);
      const rect = stage.getBoundingClientRect();
      const c = e.target.dataset?.c; // a corner, or a move
      const s0 = { ...b }, px = e.clientX, py = e.clientY;
      const aspect = (b.w * o.width) / (b.h * o.height);
      try { el.setPointerCapture(e.pointerId); } catch { /* a synthetic or ended pointer */ }
      const move = (ev) => {
        const dx = (ev.clientX - px) / rect.width, dy = (ev.clientY - py) / rect.height;
        const gs = [], free = ev.altKey;
        const sx = (v) => free ? v : snap(v, [0, 0.5, 1, ...l.boxes.flatMap((q, k) => k === i ? [] : [q.x, q.x + q.w, q.x + q.w / 2])], gs, "v");
        const sy = (v) => free ? v : snap(v, [0, 0.5, 1, ...l.boxes.flatMap((q, k) => k === i ? [] : [q.y, q.y + q.h, q.y + q.h / 2])], gs, "h");
        if (!c) {
          const x = snapPos(s0.x + dx, s0.w, sx), y = snapPos(s0.y + dy, s0.h, sy);
          b.x = r4(Math.min(1 - s0.w, Math.max(0, x)));
          b.y = r4(Math.min(1 - s0.h, Math.max(0, y)));
        } else {
          let x0 = s0.x, y0 = s0.y, x1 = s0.x + s0.w, y1 = s0.y + s0.h;
          if (c.includes("w")) x0 = sx(Math.min(x1 - MIN, Math.max(0, x0 + dx)));
          if (c.includes("e")) x1 = sx(Math.max(x0 + MIN, Math.min(1, x1 + dx)));
          if (c.includes("n")) y0 = sy(Math.min(y1 - MIN, Math.max(0, y0 + dy)));
          if (c.includes("s")) y1 = sy(Math.max(y0 + MIN, Math.min(1, y1 + dy)));
          if (keep.checked && !ev.shiftKey) { // keep the box's shape: the height follows the width
            const hh = ((x1 - x0) * o.width) / aspect / o.height;
            if (c.includes("n")) y0 = y1 - hh; else y1 = y0 + hh;
            if (y0 < 0 || y1 > 1) return;
          }
          Object.assign(b, { x: r4(x0), y: r4(y0), w: r4(x1 - x0), h: r4(y1 - y0) });
        }
        showGuides(gs);
        place(i);
      };
      const up = () => {
        el.removeEventListener("pointermove", move);
        el.removeEventListener("pointerup", up);
        showGuides([]);
        if (b.x !== s0.x || b.y !== s0.y || b.w !== s0.w || b.h !== s0.h) ctx.edit(() => {}, true);
      };
      el.addEventListener("pointermove", move);
      el.addEventListener("pointerup", up);
    });
    boxEls.push(el);
    stage.append(el);
    place(i);
  });
  stage.append(lines);
  stage.addEventListener("pointerdown", (e) => { if (e.target === stage) select(undefined); });

  keyHandler = (e) => {
    if (st.tab !== "layouts" || st.sel.box === undefined || /INPUT|SELECT|TEXTAREA/.test(document.activeElement?.tagName)) return;
    const b = l.boxes[st.sel.box];
    const d = e.shiftKey ? 0.1 : 0.01;
    const mv = { ArrowLeft: [-d, 0], ArrowRight: [d, 0], ArrowUp: [0, -d], ArrowDown: [0, d] }[e.key];
    if (mv) {
      e.preventDefault();
      b.x = r4(Math.min(1 - b.w, Math.max(0, b.x + mv[0])));
      b.y = r4(Math.min(1 - b.h, Math.max(0, b.y + mv[1])));
      place(st.sel.box);
      ctx.edit(() => {}, false);
      syncRow(st.sel.box);
    } else if (e.key === "Delete" || e.key === "Backspace") {
      e.preventDefault();
      const i = st.sel.box;
      st.sel.box = undefined;
      ctx.edit(() => l.boxes.splice(i, 1));
    }
  };
  window.addEventListener("keydown", keyHandler);

  // the table of boxes
  const pct = (v) => +(v * 100).toFixed(2);
  const syncRow = (i) => {
    const tr = table.querySelector(`tr[data-i="${i}"]`);
    if (!tr) return;
    for (const k of ["x", "y", "w", "h"]) tr.querySelector(`[data-k=${k}]`).value = pct(l.boxes[i][k]);
  };
  const table = h("table.boxes", {}, h("thead", {}, h("tr", {}, ["#", "Feed", "x %", "y %", "width %", "height %", "opacity %", ""].map((t) => h("th", {}, t)))),
    h("tbody", {}, l.boxes.map((b, i) => h("tr", { "data-i": i, class: i === st.sel.box ? "sel" : "", onclick: () => select(i) },
      h("td", {}, h("span.swatch", { style: { background: colour(b.feed) } }), i + 1),
      h("td", {}, h("select", { onchange: (e) => ctx.edit(() => { b.feed = e.target.value; }) },
        p.feeds.filter(hasVideo).map((f) => h("option", { value: f.id, selected: f.id === b.feed }, f.name || f.id)),
        p.feeds.some((f) => f.id === b.feed) ? null : h("option", { selected: true, value: b.feed }, b.feed + " (missing)"))),
      ["x", "y", "w", "h"].map((k) => h("td", {}, h("input.num", { type: "number", step: 0.5, min: 0, max: 100, "data-k": k, value: pct(b[k]),
        onchange: (e) => {
          const v = Math.min(1, Math.max(k === "w" || k === "h" ? MIN : 0, (+e.target.value || 0) / 100));
          b[k] = r4(v);
          if (b.x + b.w > 1) b[k === "x" ? "x" : "w"] = r4(k === "x" ? 1 - b.w : 1 - b.x);
          if (b.y + b.h > 1) b[k === "y" ? "y" : "h"] = r4(k === "y" ? 1 - b.h : 1 - b.y);
          place(i); syncRow(i); ctx.edit(() => {}, false);
        } }))),
      h("td", {}, h("input.num", { type: "number", min: 0, max: 100, step: 5, value: Math.round((b.alpha ?? 1) * 100), onchange: (e) => ctx.edit(() => {
        const a = Math.min(100, Math.max(0, +e.target.value)) / 100;
        if (a === 1) delete b.alpha; else b.alpha = a;
        place(i);
      }, false) })),
      h("td.nowrap", {},
        h("button.icon", { title: "Bring forward", disabled: i === l.boxes.length - 1, onclick: () => { st.sel.box = i + 1; ctx.edit(() => l.boxes.splice(i + 1, 0, l.boxes.splice(i, 1)[0])); } }, "▲"),
        h("button.icon", { title: "Send backward", disabled: i === 0, onclick: () => { st.sel.box = i - 1; ctx.edit(() => l.boxes.splice(i - 1, 0, l.boxes.splice(i, 1)[0])); } }, "▼"),
        h("button.icon", { title: "Full screen", onclick: () => ctx.edit(() => Object.assign(b, { x: 0, y: 0, w: 1, h: 1 })) }, "⛶"),
        h("button.icon", { title: "Remove", onclick: () => { st.sel.box = undefined; ctx.edit(() => l.boxes.splice(i, 1)); } }, "×"))))));

  const keep = h("input", { type: "checkbox", checked: st.sel.keepShape ?? true, onchange: (e) => { st.sel.keepShape = e.target.checked; } });
  const idIn = h("input", { type: "text", value: l.id, onchange: (e) => {
    const to = e.target.value.trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$/.test(to) || p.layouts.some((x) => x !== l && x.id === to)) { toast("An id is letters, digits, - and _, and unique", "bad"); e.target.value = l.id; return; }
    ctx.edit((p) => { if (p.start_layout === l.id) p.start_layout = to; l.id = to; });
    st.sel.layout = to; ctx.draw();
  } });
  const pictured = p.feeds.filter(hasVideo);
  const unused = pictured.filter((f) => !l.boxes.some((b) => b.feed === f.id));
  const running = st.studio.running;
  return [
    h("div.editor-head", {},
      h("h2", {}, l.name || l.id),
      running ? h("button.go", { title: "Switch the running show to this layout", onclick: () => ctx.take(l.id) }, "Take live") : null,
      h("button", { onclick: () => { const id = uniqueID(l.id, p.layouts.map((x) => x.id)); ctx.edit((p) => p.layouts.push({ ...structuredClone(l), id, name: (l.name || l.id) + " copy" })); st.sel.layout = id; ctx.draw(); } }, "Duplicate"),
      h("button.danger", { disabled: p.layouts.length < 2, title: p.layouts.length < 2 ? "A show needs a layout" : "", onclick: () => {
        if (!confirm(`Delete the layout ${l.name || l.id}?`)) return;
        ctx.edit((p) => { p.layouts = p.layouts.filter((x) => x !== l); if (p.start_layout === l.id) delete p.start_layout; });
      } }, "Delete")),
    h("div.grid3", {},
      h("label.field", {}, h("span.label", {}, "Name"), h("input", { type: "text", value: l.name || "", oninput: (e) => ctx.edit(() => { l.name = e.target.value; }, false) })),
      h("label.field", {}, h("span.label", {}, "Id"), idIn),
      h("label.field.check", {}, h("input", { type: "checkbox", checked: l.id === startID, disabled: l.id === startID, onchange: () => ctx.edit((p) => { p.start_layout = l.id; }) }), " The show starts with this layout")),
    h("div.stage-wrap", {}, stage),
    h("div.toolbar", {},
      h("select", { "aria-label": "Add a box", disabled: !p.feeds.length, onchange: (e) => {
        if (!e.target.value) return;
        const n = l.boxes.length;
        ctx.edit(() => l.boxes.push(box(e.target.value, 0.1 + (n % 5) * 0.05, 0.1 + (n % 5) * 0.05, 0.4, 0.4)));
        st.sel.box = n; ctx.draw();
      } }, h("option", { value: "" }, "+ Add a box for…"), unused.map((f) => h("option", { value: f.id }, f.name || f.id)),
        unused.length < pictured.length ? h("optgroup", { label: "again" }, pictured.filter((f) => !unused.includes(f)).map((f) => h("option", { value: f.id }, f.name || f.id))) : null),
      h("label", {}, keep, " keep the box's shape (Shift: free)"),
      h("span.dim", {}, "Drag to move, corners to resize; Alt: no snapping; arrows nudge; Delete removes.")),
    table,
  ];
}

// livePic is a running feed's preview, refreshed every second in the editor.
function livePic(ctx, f) {
  if (!ctx.st.studio.running || !ctx.st.studio.status?.feeds?.some((x) => x.id === f.id)) return null;
  const img = h("img.fill", { alt: "", src: url("preview/" + f.id, { t: Date.now() }) });
  img.addEventListener("error", () => { img.style.visibility = "hidden"; });
  img.addEventListener("load", () => { img.style.visibility = ""; });
  clearInterval(liveTimer);
  liveTimer = setInterval(() => {
    for (const im of document.querySelectorAll(".stage img.live")) im.src = im.src.replace(/t=\d+/, "t=" + Date.now());
  }, 1000);
  img.classList.add("live");
  return img;
}

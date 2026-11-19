// The Overlays tab: text, clocks, images (logos) and lower thirds drawn over the program. Each
// is previewed exactly as BGAMix draws it, on a stage the output's shape; drag it to move it.

import { h, put, token, uniqueID, toast, dirname, relative } from "./api.js";

const TYPES = { lower_third: "Lower third", text: "Text", clock: "Clock", image: "Image (logo)" };
const CLOCKS = { time: "13:45:07", time_hm: "13:45", date_time: "2026-11-19 13:45:07", date: "19 November 2026" };

let dragging = null;
export function unmount() { dragging = null; }

// render draws an overlay on the server and returns {url, x, y, w, h} (pixels on the output).
async function draw(st, ov) {
  const r = await fetch("/api/overlay/render", { method: "POST", headers: { "Content-Type": "application/json", "X-BGAMix-Token": token },
    body: JSON.stringify({ path: st.path, output: st.project.output, overlay: ov }) });
  if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || r.statusText);
  const blob = await r.blob();
  const url = URL.createObjectURL(blob);
  const img = await new Promise((res, rej) => { const i = new Image(); i.onload = () => res(i); i.onerror = rej; i.src = url; });
  return { url, x: +r.headers.get("X-Overlay-X"), y: +r.headers.get("X-Overlay-Y"), w: img.naturalWidth, h: img.naturalHeight };
}

export function render(el, ctx) {
  const { st } = ctx;
  const p = st.project;
  p.overlays ||= [];
  if (!p.overlays.some((o) => o.id === st.sel.overlay)) st.sel.overlay = p.overlays[0]?.id;
  const ov = p.overlays.find((o) => o.id === st.sel.overlay);
  const add = (type) => {
    const o = { lower_third: { type, text: "Name Surname", subtitle: "Title", x: 0.05, y: 0.76, hidden: true },
      text: { type, text: "Text", x: 0.05, y: 0.05, background: "#000000a0" },
      clock: { type, x: 0.82, y: 0.04, background: "#00000080" },
      image: { type, path: "", x: 0.84, y: 0.04, w: 0.12 } }[type];
    o.id = uniqueID(type === "lower_third" ? "lower-third" : type, p.overlays.map((x) => x.id));
    o.name = TYPES[type];
    ctx.edit((p) => (p.overlays ||= []).push(o));
    st.sel.overlay = o.id;
    ctx.draw();
  };
  el.append(h("div.split", {},
    h("aside.list", {},
      h("div.list-head", {}, h("strong", {}, "Overlays"),
        h("select", { "aria-label": "Add an overlay", onchange: (e) => { if (e.target.value) add(e.target.value); e.target.value = ""; } },
          h("option", { value: "" }, "+ Add…"), Object.entries(TYPES).map(([k, v]) => h("option", { value: k }, v)))),
      p.overlays.length ? p.overlays.map((o, i) => h("button.item", { class: o.id === st.sel.overlay ? "active" : "", onclick: () => { st.sel.overlay = o.id; ctx.draw(); } },
        h("span.grow", {}, h("div", {}, o.name || o.id), h("div.dim", {}, TYPES[o.type] || o.type,
          o.text ? " · " + o.text : "", o.hidden ? " · starts hidden" : "", o.layouts?.length ? ` · ${o.layouts.length} layout(s)` : "")),
        h("span.updown", {},
          h("button.icon", { title: "Down (behind)", disabled: i === 0, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.overlays.splice(i - 1, 0, p.overlays.splice(i, 1)[0])); } }, "▲"),
          h("button.icon", { title: "Up (in front)", disabled: i === p.overlays.length - 1, onclick: (e) => { e.stopPropagation(); ctx.edit((p) => p.overlays.splice(i + 1, 0, p.overlays.splice(i, 1)[0])); } }, "▼"))))
        : h("p.dim.pad", {}, "No overlays. Add a lower third to introduce people, a logo, a clock or a line of text. They are drawn over every feed.")),
    h("section.editor", {}, ov ? editor(ctx, ov) : h("p.dim", {}, "Choose an overlay."))));
}

function field(label, input, hint) {
  return h("label.field", {}, h("span.label", {}, label), input, hint ? h("span.hint", {}, hint) : null);
}

function editor(ctx, ov) {
  const { st } = ctx;
  const p = st.project;
  const o = p.output;
  const stage = h("div.stage.ov-stage", { style: { aspectRatio: `${o.width} / ${o.height}`, width: `min(100%, calc(52vh * ${o.width} / ${o.height}))` } });
  const pic = h("img.ov-pic", { alt: "", draggable: "false" });
  const msg = h("div.err", {});
  stage.append(pic);
  let geo = null;
  const place = () => {
    if (!geo) return;
    Object.assign(pic.style, { left: (ov.x * 100) + "%", top: (ov.y * 100) + "%", width: (geo.w / o.width * 100) + "%", height: (geo.h / o.height * 100) + "%" });
  };
  let seq = 0;
  const refresh = async () => {
    const my = ++seq;
    try {
      const g = await draw(st, ov);
      if (my !== seq) return;
      if (geo) URL.revokeObjectURL(geo.url);
      geo = g;
      pic.src = g.url;
      msg.textContent = "";
      place();
    } catch (e) { msg.textContent = e.message; }
  };
  let t;
  const later = () => { clearTimeout(t); t = setTimeout(refresh, 150); };
  const set = (k, v, redraw = false) => { ctx.edit(() => { if (v === "" || v === undefined || v === null || v === false) delete ov[k]; else ov[k] = v; }, redraw); if (!redraw) later(); };
  const r4 = (v) => Math.round(v * 10000) / 10000;
  pic.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    const rect = stage.getBoundingClientRect(), x0 = ov.x, y0 = ov.y, px = e.clientX, py = e.clientY;
    try { pic.setPointerCapture(e.pointerId); } catch {}
    dragging = true;
    const move = (ev) => {
      const maxX = geo ? 1 - geo.w / o.width : 0.99, maxY = geo ? 1 - geo.h / o.height : 0.99;
      ov.x = r4(Math.max(0, Math.min(Math.max(0, maxX), x0 + (ev.clientX - px) / rect.width)));
      ov.y = r4(Math.max(0, Math.min(Math.max(0, maxY), y0 + (ev.clientY - py) / rect.height)));
      place();
      xIn.value = +(ov.x * 100).toFixed(1); yIn.value = +(ov.y * 100).toFixed(1);
    };
    const up = () => { pic.removeEventListener("pointermove", move); pic.removeEventListener("pointerup", up); dragging = null; ctx.edit(() => {}, false); };
    pic.addEventListener("pointermove", move);
    pic.addEventListener("pointerup", up);
  });
  const xIn = h("input.num", { type: "number", min: 0, max: 99, step: 0.5, value: +(ov.x * 100).toFixed(1), onchange: (e) => { set("x", r4(Math.min(0.99, Math.max(0, +e.target.value / 100)))); ov.x = ov.x || 0; } });
  const yIn = h("input.num", { type: "number", min: 0, max: 99, step: 0.5, value: +(ov.y * 100).toFixed(1), onchange: (e) => { set("y", r4(Math.min(0.99, Math.max(0, +e.target.value / 100)))); ov.y = ov.y || 0; } });
  const colourIn = (k, def, label) => {
    const v = ov[k] || def;
    const hex = v.slice(0, 7), a = v.length === 9 ? parseInt(v.slice(7), 16) : 255;
    const c = h("input", { type: "color", value: hex });
    const al = h("input.num", { type: "number", min: 0, max: 100, step: 5, value: Math.round(a / 2.55), title: "Opacity %" });
    const upd = () => set(k, c.value + Math.round(Math.min(100, Math.max(0, +al.value)) * 2.55).toString(16).padStart(2, "0"));
    c.addEventListener("input", upd); al.addEventListener("change", upd);
    return field(label, h("div.row", {}, c, al, "%", ov[k] ? h("button.icon", { title: "Default", onclick: () => set(k, undefined, true) }, "×") : null));
  };
  const idIn = h("input", { type: "text", value: ov.id, onchange: (e) => {
    const to = e.target.value.trim();
    if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$/.test(to) || p.overlays.some((x) => x !== ov && x.id === to)) { toast("An id is letters, digits, - and _, and unique", "bad"); e.target.value = ov.id; return; }
    ctx.edit(() => { ov.id = to; }); st.sel.overlay = to; ctx.draw();
  } });
  const parts = [
    h("div.editor-head", {}, h("h2", {}, ov.name || ov.id),
      st.studio.running ? h("span.dim", {}, "Changes here apply at the next start; show, hide and change words live in the switcher.") : null,
      h("button.danger", { onclick: () => ctx.edit((p) => { p.overlays = p.overlays.filter((x) => x !== ov); if (!p.overlays.length) delete p.overlays; }) }, "Delete")),
    h("div.grid3", {},
      field("Name", h("input", { type: "text", value: ov.name || "", oninput: (e) => ctx.edit(() => { ov.name = e.target.value; }, false) })),
      field("Id", idIn, "for the control API"),
      field("Type", h("input", { type: "text", value: TYPES[ov.type] || ov.type, disabled: true }))),
  ];
  if (ov.type === "lower_third" || ov.type === "text") {
    parts.push(h("div.grid2", {},
      field(ov.type === "text" ? "Text" : "Name (first line)", h("input", { type: "text", value: ov.text || "", oninput: (e) => set("text", e.target.value) })),
      ov.type === "lower_third" ? field("Title (second line)", h("input", { type: "text", value: ov.subtitle || "", oninput: (e) => set("subtitle", e.target.value) })) : null));
  }
  if (ov.type === "clock") {
    parts.push(h("div.grid3", {}, field("Shows", h("select", { onchange: (e) => set("format", e.target.value === "time" ? "" : e.target.value) },
      Object.entries(CLOCKS).map(([k, v]) => h("option", { value: k, selected: (ov.format || "time") === k }, v))), "the mixing computer's time")));
  }
  if (ov.type === "image") {
    parts.push(h("div.grid2", {}, field("Image", h("div.row", {},
      h("input.grow", { type: "text", value: ov.path || "", placeholder: "PNG (transparency kept), JPEG, GIF, WebP …", onchange: (e) => set("path", e.target.value.trim(), true) }),
      h("button", { onclick: async () => { const f = await ctx.pick("image", { start: dirname(st.path) }); if (f) set("path", relative(st.path, f), true); } }, "Browse…")))));
  }
  parts.push(h("div.stage-wrap", {}, stage), msg,
    h("div.grid3", {},
      field("Position (% of the picture)", h("div.row", {}, "x", xIn, "y", yIn), "or drag it on the picture"),
      ov.type === "image" || ov.type === "lower_third" ? field("Width (% of the picture)", h("input.num", { type: "number", min: 1, max: 100, step: 1,
        value: Math.round((ov.w || (ov.type === "image" ? 0.15 : 0.45)) * 100), onchange: (e) => set("w", Math.min(1, Math.max(0.01, +e.target.value / 100))) })) : null,
      ov.type !== "image" ? field("Text size (% of the picture's height)", h("input.num", { type: "number", min: 1, max: 50, step: 0.5,
        value: +((ov.size || 0.05) * 100).toFixed(1), onchange: (e) => set("size", Math.min(0.5, Math.max(0.01, +e.target.value / 100))) })) : null),
    ov.type !== "image" ? h("div.grid3", {},
      colourIn("colour", "#ffffff", "Text colour"),
      colourIn("background", ov.type === "lower_third" ? "#000000c0" : "#00000000", ov.type === "lower_third" ? "Bar" : "Box behind (0 %: none)"),
      ov.type === "lower_third" ? colourIn("accent", "#e53935", "Accent strip") : null) : null,
    h("div.grid3", {},
      field("Opacity (%)", h("input.num", { type: "number", min: 0, max: 100, step: 5, value: Math.round((ov.opacity ?? 1) * 100),
        onchange: (e) => { const a = Math.min(100, Math.max(0, +e.target.value)) / 100; set("opacity", a === 1 ? undefined : a); } })),
      field("Fade in and out (s)", h("input.num", { type: "number", min: 0.05, max: 10, step: 0.1, value: ov.fade_s || 0.3, onchange: (e) => set("fade_s", Math.min(10, Math.max(0.05, +e.target.value)) === 0.3 ? undefined : Math.min(10, Math.max(0.05, +e.target.value))) })),
      h("label.field.check", {}, h("input", { type: "checkbox", checked: !!ov.hidden, onchange: (e) => set("hidden", e.target.checked || undefined) }), " Starts hidden (shown from the switcher)")),
    h("fieldset", {}, h("legend", {}, "Shown on"),
      h("div.row.wrap", {},
        h("label", {}, h("input", { type: "checkbox", checked: !ov.layouts?.length, onchange: (e) => ctx.edit(() => { if (e.target.checked) delete ov.layouts; else ov.layouts = p.layouts.map((l) => l.id); }) }), " every layout"),
        ov.layouts?.length ? p.layouts.map((l) => h("label", {}, h("input", { type: "checkbox", checked: ov.layouts.includes(l.id), onchange: (e) => ctx.edit(() => {
          ov.layouts = e.target.checked ? [...ov.layouts, l.id] : ov.layouts.filter((x) => x !== l.id);
          if (!ov.layouts.length) delete ov.layouts;
        }) }), " " + (l.name || l.id))) : null),
      h("p.dim", {}, "It shows while it is switched on and one of these layouts is on air, and fades with the switch.")));
  refresh();
  return parts;
}

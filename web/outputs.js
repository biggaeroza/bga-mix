// The Outputs tab: the output picture (size and frame rate), the show's name and notes, and the
// program recordings.

import { h } from "./api.js";

const SIZES = [[3840, 2160, "2160p (4K UHD)"], [2560, 1440, "1440p"], [1920, 1080, "1080p (Full HD)"], [1280, 720, "720p (HD)"],
  [1024, 576, "576p (16:9)"], [854, 480, "480p (16:9)"], [640, 360, "360p"], [1080, 1920, "1080×1920 (vertical)"], [720, 1280, "720×1280 (vertical)"]];
const RATES = [24, 25, 30, 48, 50, 60];

// the same estimate as the engine's default bitrate (engine.Bitrate): 0.15 bit per pixel per
// frame for H.264, 60 % of it for H.265
function autoKbps(o, codec) {
  const k = 0.15 * o.width * o.height * o.fps / 1000 * (codec === "h265" ? 0.6 : 1);
  return Math.round(k / 100) * 100;
}

export function render(el, ctx) {
  const p = ctx.st.project;
  const o = p.output;
  const preset = SIZES.find(([w, hh]) => w === o.width && hh === o.height);
  const custom = (k, v) => ctx.edit(() => { o[k] = Math.max(2, Math.round((+v || 2) / 2) * 2); });
  el.append(h("div.page", {},
    h("section.panel", {}, h("h2", {}, "Show"),
      h("div.grid2", {},
        h("label.field", {}, h("span.label", {}, "Name"), h("input", { type: "text", value: p.name || "", oninput: (e) => ctx.edit(() => { p.name = e.target.value; }, false), onchange: () => ctx.draw() }),
          h("span.hint", {}, "{project} in recording names")),
        h("label.field", {}, h("span.label", {}, "Notes"), h("textarea", { rows: 2, oninput: (e) => ctx.edit(() => { if (e.target.value) p.notes = e.target.value; else delete p.notes; }, false) }, p.notes || "")))),
    h("section.panel", {}, h("h2", {}, "Output picture"),
      h("div.grid3", {},
        h("label.field", {}, h("span.label", {}, "Size"), h("select", { onchange: (e) => {
          if (e.target.value === "custom") { ctx.edit(() => {}); return; }
          const [w, hh] = e.target.value.split("x").map(Number);
          ctx.edit(() => { o.width = w; o.height = hh; });
        } }, SIZES.map(([w, hh, n]) => h("option", { value: `${w}x${hh}`, selected: preset && preset[0] === w && preset[1] === hh }, n)),
          h("option", { value: "custom", selected: !preset }, "Custom"))),
        h("label.field", {}, h("span.label", {}, "Width × height"), h("div.row", {},
          h("input.num", { type: "number", min: 16, max: 7680, step: 2, value: o.width, onchange: (e) => custom("width", e.target.value), "aria-label": "Width" }), "×",
          h("input.num", { type: "number", min: 16, max: 4320, step: 2, value: o.height, onchange: (e) => custom("height", e.target.value), "aria-label": "Height" }))),
        h("label.field", {}, h("span.label", {}, "Frame rate"), h("select", { onchange: (e) => ctx.edit(() => { o.fps = +e.target.value; }) },
          [...new Set([...RATES, o.fps])].sort((a, b) => a - b).map((r) => h("option", { value: r, selected: r === o.fps }, r + " fps"))))),
      h("p.dim", {}, "Every feed is scaled to fit this picture (keeping its shape, with borders) and brought to this frame rate. Layout boxes are fractions of it, so they follow a change of size.")),
    h("section.panel", {}, h("div.editor-head", {}, h("h2", {}, "Recordings of the program"),
      h("button", { onclick: () => ctx.edit(() => (p.recordings ||= []).push({ path: "recordings/{project}-{date}-{time}" })) }, "+ Add a recording")),
      (p.recordings || []).length ? (p.recordings || []).map((r, i) => recording(ctx, r, i))
        : h("p.dim", {}, "No recordings: the show is only mixed (and previewed). Feeds can also be recorded clean on their own (Feeds tab, ISO)."),
      h("p.dim", {}, "MP4 and MOV are written so that a recording cut short by a crash or power cut still plays up to its last second. A file never overwrites an earlier one (-2, -3 … are added). Paths are relative to the show file."))));
}

function recording(ctx, r, i) {
  const p = ctx.st.project;
  const set = (k, v, redraw = false) => ctx.edit(() => { if (v === "" || v === undefined || v === false || v === 0) delete r[k]; else r[k] = v; }, redraw);
  return h("fieldset.rec", {}, h("legend", {}, `program-${i + 1}`),
    h("div.grid3", {},
      h("label.field.span2", {}, h("span.label", {}, "File"), h("input", { type: "text", value: r.path, oninput: (e) => ctx.edit(() => { r.path = e.target.value; }, false) }),
        h("span.hint", {}, "{project} {date} {time}; the extension follows the container")),
      h("label.field", {}, h("span.label", {}, "Start"), h("select", { onchange: (e) => set("manual", e.target.value === "1") },
        h("option", { value: "", selected: !r.manual }, "with the show"), h("option", { value: "1", selected: !!r.manual }, "by hand (switcher)"))),
      h("label.field", {}, h("span.label", {}, "Container"), h("select", { onchange: (e) => set("container", e.target.value === "mp4" ? "" : e.target.value) },
        [["mp4", "MP4 (crash-safe)"], ["mkv", "Matroska (MKV)"], ["mov", "QuickTime (MOV, crash-safe)"]].map(([v, n]) => h("option", { value: v, selected: (r.container || "mp4") === v }, n)))),
      h("label.field", {}, h("span.label", {}, "Video codec"), h("select", { onchange: (e) => set("codec", e.target.value === "h264" ? "" : e.target.value, true) },
        [["h264", "H.264 (plays everywhere)"], ["h265", "H.265 (smaller, slower)"]].map(([v, n]) => h("option", { value: v, selected: (r.codec || "h264") === v }, n)))),
      h("label.field", {}, h("span.label", {}, "Bitrate (kbit/s)"), h("input.num", { type: "number", min: 0, max: 200000, step: 100, value: r.bitrate_kbps || "", placeholder: `auto: ${autoKbps(p.output, r.codec)}`,
        onchange: (e) => set("bitrate_kbps", Math.max(0, Math.round(+e.target.value || 0))) })),
      h("label.field", {}, h("span.label", {}, "New file every (s)"), h("input.num", { type: "number", min: 0, step: 60, value: r.segment_s || "", placeholder: "no limit",
        onchange: (e) => set("segment_s", Math.max(0, +e.target.value || 0)) })),
      h("label.field", {}, h("span.label", {}, "or every (MB)"), h("input.num", { type: "number", min: 0, step: 100, value: r.segment_mb || "", placeholder: "no limit",
        onchange: (e) => set("segment_mb", Math.max(0, +e.target.value || 0)) })),
      h("div.field.end", {}, h("button.danger", { onclick: () => ctx.edit(() => { p.recordings.splice(i, 1); if (!p.recordings.length) delete p.recordings; }) }, "Remove"))));
}

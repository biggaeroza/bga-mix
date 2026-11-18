// Choosing files: the desktop's native dialogs when there are any, otherwise an in-page browser
// of the computer's folders (served by /api/files).

import { api, h, modal, bytes, join, put } from "./api.js";

let native = false;
export function setNative(v) { native = v; }

const last = { dir: "" }; // the folder the in-page browser opened last

// pick asks for a file ("media", "image", "show") or a folder ("folder"); null when cancelled.
// With save, the in-page browser asks for a file name in the chosen folder.
export async function pick(kind, { start = "", save = false, suggested = "" } = {}) {
  if (native && !save) {
    const r = await api("browse", { kind });
    return r.cancelled ? null : r.path;
  }
  return browse(kind, start || last.dir, save, suggested);
}

function browse(kind, start, save, suggested) {
  return new Promise((resolve) => {
    let done = false;
    const finish = (v) => { if (!done) { done = true; resolve(v); close(); } };
    const pathIn = h("input.grow", { type: "text", placeholder: "folder", "aria-label": "Folder" });
    const nameIn = save ? h("input.grow", { type: "text", value: suggested, "aria-label": "File name" }) : null;
    const list = h("div.picker-list", {});
    const msg = h("div.dim", {});
    let dir = "";
    const wanted = (e) => e.dir || (kind === "media" ? e.kind === "video" || e.kind === "image"
      : kind === "image" ? e.kind === "image" : kind === "show" ? e.kind === "show" : false);
    const load = async (d) => {
      try {
        const l = await api("files?dir=" + encodeURIComponent(d));
        dir = l.dir; last.dir = dir; pathIn.value = dir; msg.textContent = "";
        put(list, 
          l.parent ? h("button.entry", { onclick: () => load(l.parent) }, h("span.ico", {}, "↰"), "..") : null,
          ...l.entries.filter(wanted).map((e) => h("button.entry", {
            class: e.dir ? "dir" : "",
            ondblclick: () => { if (!e.dir && !save && kind !== "folder") finish(e.path); },
            onclick: () => {
              if (e.dir) load(e.path);
              else if (save) nameIn.value = e.name;
              else finish(e.path);
            },
          }, h("span.ico", {}, e.dir ? "📁" : e.kind === "image" ? "🖼" : e.kind === "show" ? "🎛" : "🎞"), h("span.grow", {}, e.name), e.dir ? "" : h("span.dim", {}, bytes(e.size)))),
        );
        if (!list.children.length) list.append(h("div.dim.pad", {}, "Nothing to choose here."));
      } catch (err) { msg.textContent = err.message; }
    };
    pathIn.addEventListener("keydown", (e) => { if (e.key === "Enter") load(pathIn.value); });
    const title = save ? "Save the show" : kind === "folder" ? "Choose a folder" : kind === "show" ? "Open a show" : kind === "image" ? "Choose an image" : "Choose a media file";
    const close = modal(title, h("div.picker", {}, h("div.row", {}, pathIn, h("button", { onclick: () => load(pathIn.value) }, "Go")), list,
      save ? h("label.row", {}, "File name ", nameIn) : null, msg), (closeIt) => [
      h("button", { onclick: () => finish(null) }, "Cancel"),
      save ? h("button.primary", { onclick: () => { const n = nameIn.value.trim(); if (n) finish(join(dir, n)); } }, "Save")
        : kind === "folder" ? h("button.primary", { onclick: () => finish(dir) }, "Choose this folder") : null,
    ]);
    // a click on the backdrop or × closes without an answer
    const obs = new MutationObserver(() => { if (!document.body.contains(list)) { obs.disconnect(); finish(null); } });
    obs.observe(document.body, { childList: true });
    load(start);
  });
}

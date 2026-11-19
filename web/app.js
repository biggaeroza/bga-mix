// BGAMix's GUI: the shell (project state, the top bar, tabs, polling the studio). The tabs are
// modules with render(container, ctx); the Live tab is mounted once and updated in place.

import { api, h, toast, basename, modal, put } from "./api.js";
import { pick, setNative } from "./pick.js";
import * as feeds from "./feeds.js";
import * as media from "./media.js";
import * as layouts from "./layouts.js";
import * as overlays from "./overlays.js";
import * as outputs from "./outputs.js";
import * as live from "./live.js";
import * as log from "./log.js";

const TABS = [
  { id: "live", label: "Live", mod: live },
  { id: "feeds", label: "Feeds", mod: feeds },
  { id: "media", label: "Media", mod: media },
  { id: "layouts", label: "Layouts", mod: layouts },
  { id: "overlays", label: "Overlays", mod: overlays },
  { id: "outputs", label: "Outputs", mod: outputs },
  { id: "log", label: "Log", mod: log },
];

const st = {
  meta: {},
  project: null,
  path: "",          // the show file ("" until saved)
  saved: "",         // the project as last saved or opened (JSON), for the unsaved mark
  started: "",       // the project as the running show was started (JSON)
  problems: [],
  studio: { running: false, events: [], seq: 0 },
  events: [],        // the log, oldest first
  seq: 0,
  tab: "live",
  sel: {},           // per-tab selection (feed, layout, box …)
};

let checkTimer;
const ctx = {
  st,
  // edit changes the project; with rerender the tab is drawn again (structure changed),
  // without it (a field typed into) only the marks are updated.
  edit(fn, rerender = true) {
    fn(st.project);
    marks();
    if (rerender) draw();
    clearTimeout(checkTimer);
    checkTimer = setTimeout(check, 400);
  },
  draw: () => draw(),
  go: (tab) => { st.tab = tab; draw(); },
  start: () => start(),
  // take switches the running show to a layout (transition: null for the show's default)
  async take(id, transition = null) {
    try {
      const body = { layout: id };
      if (transition) body.transition = transition;
      await api("layout", body);
      await poll();
    } catch (e) { toast(e.message, "bad"); }
  },
  stop: () => stop(),
  pick,
};

function json(p) { return JSON.stringify(p); }
const dirty = () => st.project && json(st.project) !== st.saved;
const stale = () => st.studio.running && st.started && json(st.project) !== st.started;

async function check() {
  if (!st.project) return;
  try {
    const r = await api("project/check", { path: st.path, project: st.project });
    st.problems = r.problems;
  } catch (e) { st.problems = [e.message]; }
  marks();
}

function load(project, path, problems = []) {
  st.project = project;
  st.path = path || "";
  st.saved = json(project);
  st.problems = problems;
  st.sel = {};
  try { if (path) localStorage.setItem("bgamix.last", path); } catch {}
  draw();
  check();
}

async function newShow() {
  if (dirty() && !confirm("Discard the unsaved changes?")) return;
  load((await api("project/new", {})).project, "");
}

async function openShow(path) {
  if (dirty() && !confirm("Discard the unsaved changes?")) return;
  try {
    if (!path) {
      if (st.meta.native_dialogs) path = "";
      else { path = await pick("show"); if (!path) return; }
    }
    const r = await api("project/open", { path });
    if (r.cancelled) return;
    load(r.project, r.path, r.problems || []);
    toast("Opened " + basename(r.path));
  } catch (e) { toast(e.message, "bad"); }
}

async function save(ask) {
  try {
    let path = st.path;
    if ((!path || ask) && !st.meta.native_dialogs) {
      path = await pick("show", { save: true, suggested: basename(st.path) || (st.project.name || "show") + ".bgamix" });
      if (!path) return;
      ask = false;
    }
    const r = await api("project/save", { path, ask: !!ask, project: st.project });
    if (r.cancelled) return;
    st.path = r.path;
    st.saved = json(st.project);
    st.problems = r.problems || [];
    try { localStorage.setItem("bgamix.last", r.path); } catch {}
    toast("Saved " + basename(r.path));
    draw();
  } catch (e) { toast(e.message, "bad"); }
}

async function start() {
  try {
    await api("show/start", { path: st.path, project: st.project });
    st.started = json(st.project);
    toast("The show is running");
    st.tab = "live";
    await poll();
    draw();
  } catch (e) {
    if (e.problems) {
      st.problems = e.problems;
      showProblems("The show cannot start");
    } else toast(e.message, "bad");
    marks();
  }
}

async function stop() {
  if (!confirm("Stop the show? Recordings are finished and closed.")) return;
  try {
    await api("show/stop", {});
    toast("Stopped");
    await poll();
    draw();
  } catch (e) { toast(e.message, "bad"); }
}

function showProblems(title = "Problems") {
  modal(title, st.problems.length ? h("ul.problems", {}, st.problems.map((p) => h("li", {}, p))) : h("p", {}, "None."),
    (close) => [h("button", { onclick: close }, "Close")]);
}

// ---- the top bar and tabs ----------------------------------------------------------------

function marks() {
  const top = document.getElementById("top");
  if (!st.project) return;
  const run = st.studio.running;
  put(top, 
    h("div.brand", {}, h("span.logo", {}, "BGA"), "Mix"),
    h("div.file", { title: st.path || "not saved yet" },
      h("strong", {}, st.project.name || "Untitled"), " ",
      h("span.dim", {}, st.path ? basename(st.path) : "not saved"),
      dirty() ? h("span.badge.warn", { title: "Changes not saved" }, "unsaved") : null),
    h("div.actions", {},
      h("button", { onclick: newShow, title: "A new show" }, "New"),
      h("button", { onclick: () => openShow(), title: "Open a show file (.bgamix)" }, "Open…"),
      h("button", { onclick: () => save(false), title: "Save (Ctrl+S)" }, "Save"),
      h("button", { onclick: () => save(true) }, "Save as…"),
    ),
    h("div.run", {},
      st.problems.length ? h("button.badge.bad", { onclick: () => showProblems(), title: "What is wrong with the show" }, `${st.problems.length} problem${st.problems.length > 1 ? "s" : ""}`) : h("span.badge.ok", {}, "ready"),
      stale() ? h("span.badge.warn", { title: "The running show was started before these changes: stop and start to apply them" }, "changed since start") : null,
      run ? h("span.onair", {}, "● LIVE") : null,
      run ? h("button.stop", { onclick: stop }, "Stop show") : h("button.go", { onclick: start, disabled: st.problems.length > 0, title: st.problems.length ? "Fix the problems first" : "Start the show" }, "Start show")),
  );
  document.title = (dirty() ? "• " : "") + (st.project.name || "BGAMix") + " — BGAMix";
}

function draw() {
  marks();
  const nav = document.getElementById("tabs");
  put(nav, ...TABS.map((t) => h("button", {
    class: t.id === st.tab ? "active" : "", onclick: () => { st.tab = t.id; try { localStorage.setItem("bgamix.tab", t.id); } catch {} draw(); },
  }, t.label, t.id === "log" && errorsSince() ? h("span.dot", {}) : null)));
  const main = document.getElementById("main");
  for (const t of TABS) if (t.id !== st.tab && t.mod.unmount) t.mod.unmount();
  const tab = TABS.find((t) => t.id === st.tab);
  put(main);
  main.className = "tab-" + tab.id;
  tab.mod.render(main, ctx);
}

let seenErrors = 0;
function errorsSince() { return st.events.filter((e) => e.kind === "error").length > seenErrors && st.tab !== "log"; }
ctx.seenErrors = () => { seenErrors = st.events.filter((e) => e.kind === "error").length; };

// ---- polling -----------------------------------------------------------------------------

async function poll() {
  try {
    const r = await api("studio?after=" + st.seq);
    const wasRunning = st.studio.running;
    st.studio = r;
    if (r.events.length) {
      st.events.push(...r.events);
      if (st.events.length > 1000) st.events.splice(0, st.events.length - 1000);
      for (const e of r.events) if (e.kind === "error" && e.fatal) toast("The mixer failed: " + e.text, "bad");
    }
    st.seq = r.seq;
    if (wasRunning !== r.running) {
      if (!r.running) st.started = "";
      draw();
    } else {
      if (st.tab === "live") live.update(ctx);
      if (st.tab === "log" && r.events.length) log.update(ctx);
      if (r.events.some((e) => e.kind === "error")) marks(), drawTabsOnly();
    }
  } catch (e) { /* the server went away: keep trying */ }
}

function drawTabsOnly() {
  for (const [i, t] of TABS.entries()) {
    const b = document.querySelectorAll("#tabs button")[i];
    if (!b) continue;
    const dot = b.querySelector(".dot");
    if (t.id === "log" && errorsSince() && !dot) b.append(h("span.dot", {}));
  }
}

// ---- start -------------------------------------------------------------------------------

window.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(false); }
});
window.addEventListener("beforeunload", (e) => { if (dirty()) { e.preventDefault(); e.returnValue = ""; } });

(async () => {
  st.meta = await api("meta");
  setNative(st.meta.native_dialogs);
  try { st.tab = localStorage.getItem("bgamix.tab") || "live"; } catch {}
  if (!TABS.some((t) => t.id === st.tab)) st.tab = "live";
  await poll();
  let path = st.meta.initial_path;
  if (!path && st.studio.running) path = st.studio.show_path;
  if (!path) { try { path = localStorage.getItem("bgamix.last") || ""; } catch {} }
  let opened = false;
  if (path) {
    try { const r = await api("project/open", { path }); load(r.project, r.path, r.problems || []); opened = true; } catch {}
  }
  if (!opened) load((await api("project/new", {})).project, "");
  if (st.studio.running) st.started = json(st.project);
  setInterval(poll, 500);
})();

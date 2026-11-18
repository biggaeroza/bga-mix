// The Log tab: the show's events (newest first), errors highlighted, and the project's problems.

import { h, put } from "./api.js";

let body = null, filter = "all";

export function unmount() { body = null; }

export function render(el, ctx) {
  const { st } = ctx;
  ctx.seenErrors();
  body = h("tbody", {});
  el.append(h("div.page", {},
    st.problems.length ? h("section.panel", {}, h("h2", {}, "Problems in the show"), h("ul.problems", {}, st.problems.map((p) => h("li", {}, p)))) : null,
    h("section.panel", {},
      h("div.editor-head", {}, h("h2", {}, "Events"),
        h("div.seg", {}, [["all", "All"], ["problems", "Errors and warnings"]].map(([k, n]) => h("button", { class: filter === k ? "active" : "", onclick: () => { filter = k; ctx.draw(); } }, n)))),
      h("table.log", {}, h("thead", {}, h("tr", {}, ["Time", "Where", "What", ""].map((t) => h("th", {}, t)))), body))));
  update(ctx);
}

export function update(ctx) {
  if (!body) return;
  ctx.seenErrors();
  const evs = ctx.st.events.filter((e) => filter === "all" || e.kind === "error" || e.kind === "warning").slice().reverse();
  put(body, ...evs.slice(0, 500).map((e) => h("tr", { class: e.kind === "error" ? "bad" : e.kind === "warning" ? "warn" : "" },
    h("td.nowrap", {}, new Date(e.time).toLocaleTimeString()),
    h("td", {}, e.feed ? "feed " + e.feed : e.recording || "show"),
    h("td", {}, e.kind),
    h("td", {}, e.text || ""))));
  if (!evs.length) body.append(h("tr", {}, h("td.dim", { colspan: 4 }, "Nothing yet.")));
}

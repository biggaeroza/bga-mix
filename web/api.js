// The API client, a small DOM builder and formatting.

export const token = new URLSearchParams(location.hash.slice(1)).get("token") || "";

export async function api(path, body) {
  const opts = { headers: { "X-BGAMix-Token": token } };
  if (body !== undefined) {
    opts.method = "POST";
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch("/api/" + path, opts);
  const data = await r.json().catch(() => ({ error: r.statusText }));
  if (!r.ok) {
    const e = new Error(data.error || r.statusText);
    e.problems = data.problems;
    e.status = r.status;
    throw e;
  }
  return data;
}

// url adds the token to an API address for <img> (images cannot send headers).
export function url(path, params = {}) {
  const q = new URLSearchParams(params);
  if (token) q.set("token", token);
  const s = q.toString();
  return "/api/" + path + (s ? "?" + s : "");
}

// h("div.cls#id", {attr, onclick}, ...children)
export function h(spec, attrs, ...kids) {
  const m = spec.match(/^([a-z0-9]+)?((?:[.#][\w-]+)*)$/i);
  const el = document.createElement(m[1] || "div");
  for (const part of (m[2] || "").match(/[.#][\w-]+/g) || []) {
    if (part[0] === ".") el.classList.add(part.slice(1));
    else el.id = part.slice(1);
  }
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.classList.add(...String(v).split(/\s+/).filter(Boolean));
    else if (k === "value") el.value = v;
    else if (k === "checked") el.checked = !!v;
    else if (k === "style" && typeof v === "object") {
      // custom properties (--x) need setProperty: assigning them is silently ignored
      for (const [sk, sv] of Object.entries(v)) if (sk.startsWith("--")) el.style.setProperty(sk, sv); else el.style[sk] = sv;
    }
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) {
    if (k === undefined || k === null || k === false) continue;
    el.append(k instanceof Node ? k : document.createTextNode(String(k)));
  }
  return el;
}

// put replaces an element's children: arrays are flattened and null, undefined and false left
// out (replaceChildren would write them as text).
export function put(el, ...kids) {
  el.replaceChildren(...kids.flat(Infinity).filter((k) => k !== null && k !== undefined && k !== false));
}

export function duration(s) {
  if (!(s > 0)) return "–";
  const m = Math.floor(s / 60), sec = s - m * 60;
  const hh = Math.floor(m / 60);
  const ss = sec.toFixed(1).padStart(4, "0");
  return hh ? `${hh}:${String(m % 60).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

export function bytes(n) {
  if (!(n > 0)) return "–";
  const u = ["B", "kB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < u.length - 1) { n /= 1000; i++; }
  return `${n.toFixed(n < 10 && i ? 1 : 0)} ${u[i]}`;
}

export function basename(p) { return String(p || "").split(/[\\/]/).pop(); }
export function dirname(p) {
  const s = String(p || "");
  const i = Math.max(s.lastIndexOf("/"), s.lastIndexOf("\\"));
  return i > 0 ? s.slice(0, i) : (i === 0 ? s.slice(0, 1) : "");
}
export function isAbs(p) { return /^([\\/]|[A-Za-z]:[\\/])/.test(p || ""); }
export function join(dir, p) { return isAbs(p) || !dir ? p : dir.replace(/[\\/]$/, "") + "/" + p; }

// relative gives a file relative to the show's folder when inside it (the show moves with its
// media), otherwise as it is.
export function relative(showPath, p) {
  const dir = dirname(showPath);
  if (!showPath || !dir || !isAbs(p)) return p;
  const pre = dir.replace(/[\\/]$/, "") + "/";
  const norm = p.replace(/\\/g, "/");
  return norm.startsWith(pre.replace(/\\/g, "/")) ? norm.slice(pre.length) : p;
}

// slug makes an id from a name: letters, digits, - and _.
export function slug(s) {
  const v = String(s || "").toLowerCase().replace(/\.[a-z0-9]+$/, "").replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 32);
  return /^[a-z0-9]/.test(v) ? v : "f" + v;
}

export function uniqueID(base, taken) {
  let id = slug(base) || "feed", n = 2;
  const set = new Set(taken);
  if (!set.has(id)) return id;
  while (set.has(`${id}-${n}`)) n++;
  return `${id}-${n}`;
}

// colour gives a feed a steady colour for its boxes.
export function colour(id) {
  let x = 0;
  for (const c of String(id)) x = (x * 31 + c.charCodeAt(0)) >>> 0;
  return `hsl(${x % 360} 55% 45%)`;
}

export function modal(title, body, buttons) {
  const back = h("div.modal-back", {});
  const close = () => back.remove();
  const box = h("div.modal", { role: "dialog", "aria-label": title },
    h("div.modal-head", {}, h("strong", {}, title), h("button.icon", { title: "Close", onclick: close }, "×")),
    h("div.modal-body", {}, body),
    buttons ? h("div.modal-foot", {}, buttons(close)) : null);
  back.append(box);
  back.addEventListener("click", (e) => { if (e.target === back) close(); });
  document.body.append(back);
  return close;
}

let toastTimer;
export function toast(msg, kind = "") {
  let el = document.getElementById("toast");
  if (!el) { el = h("div#toast", {}); document.body.append(el); }
  el.className = "show " + kind;
  el.textContent = msg;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = ""; }, kind === "bad" ? 6000 : 2500);
}

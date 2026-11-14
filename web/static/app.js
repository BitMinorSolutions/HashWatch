/* HashWatch v0.6.0 frontend (single page) — vanilla JS + uPlot. Design: docs/DESIGN.md */
(() => {
"use strict";
const APP = document.body.dataset.app || "HashWatch";
const TAG = document.body.dataset.tagline || "";
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const root = $("#root");
const S = { state: null, miner: null, data: null, hist: [], range: 3600, err: null, charts: [], can: false, route: "overview",
  ctl: { mode: "fixed", color: "#f5a524", bright: 100, dirty: false, init: false }, series: { h5s: false, h15m: true, h5m: true },
  alerts: [], alertSeen: 0, kinds: [], desktop: false, desktopOK: false, remote: {}, rem: null };
const LS = { get: (k, d = "") => { try { return localStorage.getItem(k) ?? d; } catch (e) { return d; } }, set: (k, v) => { try { localStorage.setItem(k, v); } catch (e) {} } };

/* ---------------- utils ---------------- */
const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const isNum = (v) => v !== null && v !== undefined && v !== "" && Number.isFinite(Number(v));
const fmt = (v, d = 0) => isNum(v) ? Number(v).toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d }) : "—";
const cap = (s) => String(s || "").replace(/^\w/, c => c.toUpperCase());
const ic = (n, c = "") => `<svg class="i ${c}" aria-hidden="true"><use href="#${n}"/></svg>`;
function si(v, d = 2) { if (!isNum(v)) return "—"; v = +v; const u = ["", "K", "M", "G", "T", "P", "E"]; let i = 0;
  while (Math.abs(v) >= 1000 && i < u.length - 1) { v /= 1000; i++; } return v.toFixed(i ? d : 0) + u[i]; }
function rel(unix) { if (!unix) return "—"; const s = Math.max(0, Math.round(Date.now() / 1000 - unix));
  if (s < 5) return "just now"; if (s < 60) return s + "s ago"; if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago"; return Math.floor(s / 86400) + "d ago"; }
const absTime = (u) => u ? new Date(u * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }) : "—";
const TONE = { celebrate: ["party-popper", "accent"], good: ["circle-check", "good"], warn: ["triangle-alert", "warn"], bad: ["circle-alert", "bad"], info: ["bell", "info"] };
function toast(msg, level = "good", sub = "") { const t = $("#toast"), [i, c] = TONE[level] || TONE.good;
  t.className = "t-" + c; t.innerHTML = ic(i) + `<span><b>${esc(msg)}</b>${sub ? `<small>${esc(sub)}</small>` : ""}</span>`;
  requestAnimationFrame(() => t.classList.add("on")); clearTimeout(t._h); t._h = setTimeout(() => t.classList.remove("on"), sub ? 5200 : 2600); }
async function api(path, opts = {}) {
  const o = { credentials: "same-origin", cache: "no-store", ...opts, headers: { ...(opts.body ? { "Content-Type": "application/json" } : {}), ...(opts.headers || {}) } };
  if (o.body && typeof o.body !== "string") o.body = JSON.stringify(o.body);
  const r = await fetch(path, o); let j = {}; try { j = await r.json(); } catch (e) {}
  if (r.status === 401 && path !== "/api/login" && path !== "/api/password") { boot(); throw new Error("Signed out"); }
  if (!r.ok) throw new Error(j.error || ("HTTP " + r.status));
  return j;
}
function tween(el, to, d, suffix = "") {
  if (!el) return;
  if (!isNum(to)) { el.textContent = "—"; el.dataset.v = ""; return; }
  const from = isNum(el.dataset.v) ? +el.dataset.v : (el.dataset.first ? null : to * 0.6); to = +to; el.dataset.v = to; el.dataset.first = 1;
  if (from === null || from === to || matchMedia("(prefers-reduced-motion: reduce)").matches) { el.textContent = fmt(to, d) + suffix; return; }
  const t0 = performance.now(), dur = 900; cancelAnimationFrame(el._raf);
  const step = (now) => { const p = Math.min(1, (now - t0) / dur), e = 1 - Math.pow(1 - p, 4);
    el.textContent = fmt(from + (to - from) * e, d) + suffix; if (p < 1) el._raf = requestAnimationFrame(step); };
  el._raf = requestAnimationFrame(step);
}
function unsk(el) { if (el && el.classList.contains("sk")) el.classList.remove("sk"); return el; }
const tgtOf = () => (S.data && S.data.temps && isNum(S.data.temps.target)) ? +S.data.temps.target : 90;
function tempTone(t) { if (!isNum(t)) return "var(--t2)"; const g = tgtOf(); return t > g ? "var(--bad)" : t >= g - 5 ? "var(--warn)" : "var(--t1)"; }
function heat(t, lo, hi) {
  if (!isNum(t)) return "#5f656e";
  const x = Math.max(0, Math.min(1, (t - lo) / Math.max(1, hi - lo)));
  const st = [[60, 159, 207], [60, 207, 142], [240, 180, 76], [240, 99, 99]];
  const seg = Math.min(2, Math.floor(x * 3)), f = x * 3 - seg;
  return `rgb(${st[seg].map((v, i) => Math.round(v + (st[seg + 1][i] - v) * f)).join(",")})`;
}
const logo = '<img src="/assets/icons/icon-192.png" alt="" width="28" height="28">';

/* ---------------- segmented control ---------------- */
function seg(el) {
  if (!el) return;
  let th = el.querySelector(".thumb"); if (!th) { th = document.createElement("span"); th.className = "thumb"; el.prepend(th); }
  const on = el.querySelector("button.on");
  if (!on) { th.style.opacity = 0; return; }
  th.style.opacity = 1; th.style.width = on.offsetWidth + "px"; th.style.transform = `translateX(${on.offsetLeft}px)`;
  if (!el._ro) { el._ro = new ResizeObserver(() => seg(el)); el._ro.observe(el); }
}
function segSet(el, attr, val) { $$("button", el).forEach(b => b.classList.toggle("on", b.dataset[attr] === String(val))); seg(el); }

/* ---------------- boot ---------------- */
async function loadSprite() {
  if ($("#lucide")) return;
  try { const t = await (await fetch("/assets/vendor/lucide.svg")).text(); const d = document.createElement("div"); d.id = "lucide"; d.innerHTML = t; d.style.display = "none"; document.body.prepend(d); } catch (e) {}
}
async function boot() {
  clearInterval(S.timer); await loadSprite();
  try { S.state = await api("/api/state"); }
  catch (e) { root.innerHTML = `<div class="center"><div class="auth"><h1>Can't reach ${esc(APP)}</h1><p class="lead">${esc(e.message)}</p></div></div>`; return; }
  if (S.state.auth_required && !S.state.authed) return viewLogin();
  if (S.state.setup_needed) return viewSetup();
  const ms = S.state.miners || [];
  S.miner = ms.find(m => m.id === localStorage.getItem("hw_miner")) || ms[0];
  shell();
  moveOffer();
}

async function copyText(t) {
  try { await navigator.clipboard.writeText(t); return; } catch (e) {}
  const ta = document.createElement("textarea"); ta.value = t; ta.style.cssText = "position:fixed;opacity:0;top:0"; document.body.append(ta); ta.select();
  const ok = document.execCommand("copy"); ta.remove(); if (!ok) throw new Error("clipboard unavailable");
}

/* ---------------- "Move to Applications?" (macOS app run from Downloads / a disk image) ---------------- */
function moveOffer() {
  const inst = S.state.install;
  if (!inst || !inst.offer || S.moveShown || LS.get("hw_move_no") === "1") return;
  S.moveShown = true;
  const name = inst.name || APP, where = { "disk image": "the disk image", downloads: "your Downloads folder", translocated: "your Downloads folder" }[inst.where] || "outside Applications";
  const sc = document.createElement("div"); sc.className = "scrim";
  const d = document.createElement("div"); d.className = "dialog mac-alert"; d.setAttribute("role", "alertdialog"); d.setAttribute("aria-modal", "true"); d.setAttribute("aria-labelledby", "mvT");
  d.innerHTML = `<img class="ma-icon" src="/assets/icons/icon-192.png" alt="" width="64" height="64">
    <h2 id="mvT">Move ${esc(name)} to Applications?</h2>
    <p>${esc(name)} is running from ${esc(where)}. Move it to your Applications folder so it keeps working after you ${inst.where === "disk image" ? "eject the disk image" : "tidy up Downloads"}, and so it can start automatically.</p>
    <div class="err" id="mvE" role="alert"></div>
    <div class="acts"><button class="btn ghost" data-a="no">Not now</button><button class="btn accent" data-a="move">Move to Applications</button></div>`;
  document.body.append(sc, d); requestAnimationFrame(() => { sc.classList.add("on"); d.classList.add("on"); });
  const close = () => { sc.classList.remove("on"); d.classList.remove("on"); setTimeout(() => { sc.remove(); d.remove(); }, 220); document.removeEventListener("keydown", key); };
  const no = async () => { LS.set("hw_move_no", "1"); close(); try { await api("/api/install", { method: "POST", body: { action: "dismiss" } }); } catch (e) {} };
  const key = (e) => { if (e.key === "Escape") no(); };
  document.addEventListener("keydown", key);
  $('[data-a="no"]', d).onclick = no;
  const mv = $('[data-a="move"]', d); mv.focus();
  mv.onclick = async () => {
    mv.disabled = true; mv.textContent = "Moving…"; $("#mvE", d).textContent = "";
    let r;
    try { r = await api("/api/install", { method: "POST", body: { action: "move" } }); }
    catch (e) { mv.disabled = false; mv.textContent = "Try again"; $("#mvE", d).textContent = e.message; return; }
    clearInterval(S.timer); close();
    root.innerHTML = `<div class="center"><div class="auth view"><img class="auth-logo" src="/assets/icons/icon-192.png" alt="">${TAG ? `<div class="auth-tag">${esc(APP)} · ${esc(TAG)}</div>` : ""}
      <h1>Moved to Applications</h1><p class="lead" id="mvL">Restarting ${esc(name)} from Applications…</p></div></div>`;
    const t0 = Date.now(), oldId = r.id;
    const poll = async () => {
      try { const p = await (await fetch("/api/ping", { cache: "no-store" })).json(); if (p.id && p.id !== oldId) { location.reload(); return; } } catch (e) {}
      if (Date.now() - t0 > 25000) { $("#mvL").innerHTML = `${esc(name)} is now in Applications. Open it from there (or Launchpad).${inst.where === "disk image" ? " You can eject the disk image." : " You can delete the copy in Downloads."}`; return; }
      setTimeout(poll, 700);
    };
    setTimeout(poll, 1200);
  };
}

/* ---------------- login / setup ---------------- */
function viewLogin() {
  root.innerHTML = `<div class="center"><div class="auth view">
    <img class="auth-logo" src="/assets/icons/icon-192.png" alt="">${TAG ? `<div class="auth-tag">${esc(APP)} · ${esc(TAG)}</div>` : ""}
    <h1>Welcome back</h1><p class="lead" id="lead">Enter your ${esc(APP)} password.</p>
    <form class="panel" id="f" autocomplete="on">
      <input type="text" name="username" autocomplete="username" value="${esc(APP)}" class="sr" tabindex="-1" aria-hidden="true">
      <div id="pwWrap"><label class="f" for="pw" style="margin-top:0">Password</label>
      <input class="input" id="pw" type="password" autocomplete="current-password" required autofocus></div>
      <div class="err" id="e"></div>
      <button class="btn accent lg block" id="b">Sign in</button>
    </form></div></div>`;
  $("#f").onsubmit = async (ev) => { ev.preventDefault(); const b = $("#b"); b.disabled = true; $("#e").textContent = "";
    const body = { password: $("#pw").value };
    const r = await fetch("/api/login", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    let j = {}; try { j = await r.json(); } catch (e) {}
    if (r.ok) { boot(); return; }
    b.disabled = false; $("#e").textContent = j.error || "HTTP " + r.status; };
}
function minerForm(id) {
  return `<label class="f" for="${id}-ip" style="margin-top:0">Miner IP address</label>
    <input class="input mono" style="font-size:16px" id="${id}-ip" inputmode="decimal" placeholder="192.168.1.50" autocomplete="off" autocapitalize="off" spellcheck="false">
    <label class="f" for="${id}-name">Name <span class="t3">· optional</span></label>
    <input class="input" id="${id}-name" placeholder="Office Nano 3" autocomplete="off">
    <div class="err" id="${id}-e"></div>
    <button type="submit" class="btn accent lg block" id="${id}-go">Connect miner</button>
    <div class="divider">or</div>
    <button type="button" class="btn ghost lg block" id="${id}-scan">${ic("radar")}Scan my network</button>
    <div class="found" id="${id}-found"></div>`;
}
function wireMinerForm(id, done) {
  const go = async (host, name) => { const b = $("#" + id + "-go"); b.disabled = true; b.textContent = "Connecting…"; $("#" + id + "-e").textContent = "";
    try { const r = await api("/api/miners", { method: "POST", body: { host, name } }); toast("Connected to " + (name || r.desc)); await done(r); }
    catch (e) { $("#" + id + "-e").textContent = e.message; } finally { b.disabled = false; b.textContent = "Connect miner"; } };
  $("#" + id + "-form").onsubmit = (ev) => { ev.preventDefault(); const ip = $("#" + id + "-ip").value.trim();
    if (!ip) { $("#" + id + "-e").textContent = "Enter the miner's IP address. You can find it in your router or the Avalon app."; return; }
    go(ip, $("#" + id + "-name").value.trim()); };
  $("#" + id + "-scan").onclick = async () => { const b = $("#" + id + "-scan"), out = $("#" + id + "-found"); b.disabled = true; b.innerHTML = '<span class="spin"></span>Scanning your network…'; out.innerHTML = "";
    try { const r = await api("/api/discover", { method: "POST", body: {} });
      out.innerHTML = (r.found || []).length ? r.found.map(f => `<button type="button" data-h="${esc(f.host)}"><span><b>${esc(f.desc)}</b><br><span class="mono t3">${esc(f.host)}</span></span><span class="pill accent">Add</span></button>`).join("")
        : '<p class="fine" style="margin-top:4px">No miners answered on port 4028. Make sure the miner is on the same network, then enter its IP above.</p>';
      $$("button", out).forEach(x => x.onclick = () => { $("#" + id + "-ip").value = x.dataset.h; go(x.dataset.h, ""); });
    } catch (e) { out.innerHTML = `<div class="err">${esc(e.message)}</div>`; } finally { b.disabled = false; b.innerHTML = ic("radar") + "Scan my network"; } };
}
function viewSetup() {
  root.innerHTML = `<div class="center"><div class="auth wide view">
    <img class="auth-logo" src="/assets/icons/icon-192.png" alt="">${TAG ? `<div class="auth-tag">${esc(APP)} · ${esc(TAG)}</div>` : ""}
    <h1>Connect your miner</h1><p class="lead">${esc(APP)} reads your Avalon directly over your local network. Nothing leaves your home.</p>
    <form class="panel" id="s-form" autocomplete="off">${minerForm("s")}</form>
    <p class="fine">The miner's API listens on port 4028. For another port, enter it like <span class="mono">192.168.1.50:4029</span>.</p></div></div>`;
  wireMinerForm("s", async () => boot());
}

/* ---------------- shell: ONE scrolling page (v0.6.0) ----------------
   Everything about the miner lives on the dashboard. Settings (incl. remote access/QR) and alert history open as a sheet. */
const PAGES = [["dashboard", "Dashboard"], ["settings", "Settings"], ["alerts", "Alerts"]];
// Old multi-page links (#/pools, #/controls, …) now scroll to that card on the single page.
const ANCHOR = { overview: "top", performance: "secShares", hardware: "secTemps", pools: "secPools", controls: "secControls", raw: "secRaw" };
function shell() {
  const ms = S.state.miners || [];
  root.innerHTML = `<div class="shell one">
    <header class="hdr" id="topbar">
      <div class="hdr-brand">${logo}<span class="brand-t"><b>${esc(APP)}</b>${TAG ? `<small>${esc(TAG)}</small>` : ""}</span></div>
      <span class="hdr-div"></span>
      <div class="hdr-miner miner-pick"><div class="btnish"><span class="m-txt"><b>${esc(S.miner.name)}</b><span id="mpSub">${S.miner.kind === "remote" ? "Preview" : esc(S.miner.host + ":" + (S.miner.port || 4028))}</span></span>${ms.length > 1 ? ic("chevron-down", "sm t3") : ""}</div>
        ${ms.length > 1 ? `<select id="msel" aria-label="Miner">${ms.map(m => `<option value="${esc(m.id)}"${m.id === S.miner.id ? " selected" : ""}>${esc(m.name)}</option>`).join("")}</select>` : ""}</div>
      ${isView() ? `<span class="pill view-pill">${ic("eye", "sm")}View only</span>` : ""}
      <span class="hdr-live" id="hdrLive"><span class="dot warn"></span><span>Connecting…</span></span>
      <span class="grow"></span>
      <div class="top-acts">
        ${isView() ? "" : `<a class="icon-btn rem-btn" href="#/settings?remote" aria-label="Remote access" title="Remote access">${ic("globe", "sm")}<em class="rem-dot" id="remDot"></em></a>`}
        <a class="icon-btn bell-btn" href="#/alerts" aria-label="Alerts" title="Alerts">${ic("bell", "sm")}<em class="badge dot-badge" data-badge></em></a>
        <a class="icon-btn" href="#/settings" aria-label="Settings" title="Settings">${ic("settings", "sm")}</a></div>
    </header>
    <main class="content" id="view"></main>
  </div>`;
  if ($("#msel")) $("#msel").onchange = (e) => { localStorage.setItem("hw_miner", e.target.value); boot(); };
  S.data = null; S.hist = []; S.can = false; S.ctl.init = false; S.remote = S.state.remote || {};
  S.charts.forEach(c => c.destroy()); S.charts = [];
  const v = $("#view"); v.innerHTML = `<div class="view one-page"><div id="celebrate"></div>${DASH.build()}</div>`;
  $$(".seg", v).forEach(seg); DASH.wire();
  window.onhashchange = route; route(true);
  refresh(true); loadAlerts(); S.lot = null; S.feat = null; S.rules = null; loadLotto(); loadFeatures();
  S.timer = setInterval(tick, 1000);
}
const onScroll = () => $("#topbar") && $("#topbar").classList.toggle("scrolled", window.scrollY > 8);
window.addEventListener("scroll", onScroll, { passive: true });
function route(first) {
  const r = (location.hash.replace(/^#\/?/, "").split(/[?#]/)[0] || "");
  if (r === "settings" || r === "alerts") { S.route = r; openSheet(r); }
  else {
    S.route = "dashboard"; closeSheet();
    const a = ANCHOR[r]; if (a && !first) { const t = a === "top" ? null : $("#" + a); t ? t.scrollIntoView({ block: "start", behavior: "smooth" }) : window.scrollTo({ top: 0, behavior: "smooth" }); }
    else if (a && first && a !== "top") setTimeout(() => { const t = $("#" + a); if (t) t.scrollIntoView({ block: "start" }); }, 60);
  }
  document.title = S.route === "dashboard" ? `${S.miner ? S.miner.name + " · " : ""}${APP}` : `${S.route === "settings" ? "Settings" : "Alerts"} · ${APP}`;
  renderCelebrate(); badge(); onScroll();
}
/* Settings / alerts sheet: centred panel on Mac & iPad, full-height sheet on iPhone. */
function openSheet(kind) {
  clearTimeout(S.remT);
  let sc = $("#shScrim"), sh = $("#sheet");
  if (!sh) {
    sc = document.createElement("div"); sc.className = "scrim"; sc.id = "shScrim";
    sh = document.createElement("div"); sh.className = "msheet"; sh.id = "sheet"; sh.setAttribute("role", "dialog"); sh.setAttribute("aria-modal", "true");
    document.body.append(sc, sh); sc.onclick = () => { location.hash = "#/"; };
    requestAnimationFrame(() => { sc.classList.add("on"); sh.classList.add("on"); });
    document.addEventListener("keydown", sheetKey); document.body.classList.add("sheet-open");
  }
  const title = kind === "settings" ? "Settings" : "Alerts";
  sh.setAttribute("aria-label", title);
  sh.innerHTML = `<div class="msheet-h"><div class="seg sm" id="shTabs"><button data-k="settings" class="${kind === "settings" ? "on" : ""}">Settings</button><button data-k="alerts" class="${kind === "alerts" ? "on" : ""}">Alerts <em class="badge" data-badge></em></button></div>
    <span class="grow"></span><button class="icon-btn" id="shX" aria-label="Close">${ic("x", "sm")}</button></div>
    <div class="msheet-b" id="sheetBody"><div class="view">${VIEWS[kind].build()}</div></div>`;
  seg($("#shTabs"));
  $("#shTabs").onclick = (e) => { const b = e.target.closest("button"); if (b && b.dataset.k !== kind) location.hash = "#/" + b.dataset.k; };
  $("#shX").onclick = () => { location.hash = "#/"; };
  if (VIEWS[kind].wire) VIEWS[kind].wire();
  if (VIEWS[kind].update) VIEWS[kind].update();
  badge();
}
function sheetKey(e) { if (e.key === "Escape" && !$(".dialog")) location.hash = "#/"; }
function closeSheet() {
  const sc = $("#shScrim"), sh = $("#sheet"); if (!sh) return;
  clearTimeout(S.remT); document.removeEventListener("keydown", sheetKey); document.body.classList.remove("sheet-open");
  sc.classList.remove("on"); sh.classList.remove("on"); sc.id = ""; sh.id = ""; setTimeout(() => { sc.remove(); sh.remove(); }, 260);
}
let nextIn = 0;
function tick() { if (--nextIn <= 0) refresh(false); liveUI(); $$("[data-rel]").forEach(el => el.textContent = rel(+el.dataset.rel)); }
function liveUI() {
  const d = S.data, age = d && d.fetched_at_unix ? Date.now() / 1000 - d.fetched_at_unix : null;
  let k = "good", t;
  if (S.err) { k = "bad"; t = "Can't refresh"; } else if (!d) { k = "warn"; t = "Connecting…"; }
  else if (!d.ok) { k = "bad"; t = "Miner offline"; } else { const p = S.pollS || 5; if (age > Math.max(60, p * 3)) k = "warn"; t = "Online · updated " + (age < 60 ? Math.max(0, Math.floor(age)) + "s ago" : rel(d.fetched_at_unix)); }
  const el = $("#hdrLive"); if (el) { el.className = "hdr-live k-" + k; el.innerHTML = `<span class="dot ${k === "good" ? "" : k}"></span><span>${esc(t)}</span>`; }
  const r = S.remote || {}, rd = $("#remDot");
  if (rd) rd.className = "rem-dot" + (r.enabled ? (r.state === "online" ? " on" : r.state === "error" ? " bad" : " warn") : "");
}
async function refresh(full) {
  nextIn = S.pollS || (S.state && S.state.poll_seconds) || 5;
  const since = full || !S.hist.length ? 0 : S.hist[S.hist.length - 1].t;
  try {
    const r = await api(`/api/live?miner=${encodeURIComponent(S.miner.id)}&since=${since}&a=${S.alertSeen || 0}`);
    S.err = null; S.can = !!r.can_control; if (r.remote) { S.remote = r.remote; S.state.local_viewer = r.local_viewer; }
    newAlerts(r.alerts || [], r.alerts_latest);
    S.hist = full ? (r.history || []) : S.hist.concat(r.history || []);
    const cut = Date.now() / 1000 - 86400; while (S.hist.length && S.hist[0].t < cut) S.hist.shift();
    if (r.poll_seconds) S.pollS = r.poll_seconds;
    if (r.data) { const first = !S.data; S.data = r.data; update(first); loadLotto(); } else if (full) setTimeout(() => refresh(true), 1500);
    // Ask again just after the server's next read (its interval includes any backoff), so data is never a full cycle stale.
    // r.next_poll_unix is when the server starts its next read; ask ~1.5 s after it (reads take ~1 s).
    const p = S.pollS || 5, nx = r.next_poll_unix, serverNow = r.server_time || Date.now() / 1000;
    nextIn = nx ? Math.min(p + 2, Math.max(1, Math.ceil(nx + 1.5 - serverNow))) : p;
  } catch (e) { S.err = e.message; nextIn = Math.max(nextIn, 15); }
  liveUI();
}
function update(first) { DASH.update(first); drawCharts(); }

/* ---------------- charts ---------------- */
function tipPlugin(tip, fmtRow) {
  return { hooks: { setCursor: (u) => {
    const i = u.cursor.idx; if (i == null || u.data[0][i] == null || u.cursor.left < 0) { tip.classList.remove("on"); return; }
    tip.innerHTML = `<div class="tt">${new Date(u.data[0][i] * 1000).toLocaleString(undefined, { hour: "numeric", minute: "2-digit", second: "2-digit" })}</div>` +
      u.series.slice(1).map((s, k) => s.show && s.label && u.data[k + 1][i] != null ? `<div class="tr"><span><i style="background:${s._c}"></i>${s.label}</span><b class="num">${fmtRow(u.data[k + 1][i])}</b></div>` : "").join("");
    const L = u.over.offsetLeft + u.cursor.left, W = tip.offsetWidth, PW = tip.parentElement.clientWidth;
    tip.style.left = Math.max(8, Math.min(PW - W - 8, L + 16 > PW - W - 8 ? L - W - 16 : L + 16)) + "px";
    tip.style.top = Math.max(0, u.over.offsetTop + u.cursor.top - 30) + "px"; tip.classList.add("on"); } } };
}
function xAxis() {
  return { stroke: "#5f656e", grid: { show: false }, ticks: { show: false }, font: "500 11px Geist, sans-serif", size: 30, space: 80, gap: 8,
    values: (u, ts) => { const span = (u.scales.x.max || 0) - (u.scales.x.min || 0); let last = "";
      return ts.map(t => { const s = new Date(t * 1000).toLocaleTimeString(undefined, span < 900 ? { hour: "numeric", minute: "2-digit", second: "2-digit" } : { hour: "numeric", minute: "2-digit" });
        if (s === last) return ""; last = s; return s; }); } };
}
function yAxis(f) { return { side: 1, stroke: "#5f656e", grid: { stroke: "rgba(255,255,255,.045)", width: 1, dash: [2, 4] }, ticks: { show: false }, font: "500 11px Geist, sans-serif", size: 52, gap: 10, space: 44, values: f }; }
function chart(el, series, yf, rowF, yRange) {
  if (!window.uPlot || !el) return null;
  const tip = document.createElement("div"); tip.className = "tip"; el.appendChild(tip);
  const spline = uPlot.paths.spline ? uPlot.paths.spline() : undefined;
  const u = new uPlot({
    width: el.clientWidth || 600, height: el.clientHeight || 280, padding: [12, 0, 0, 28], pxAlign: 0,
    cursor: { points: { size: 9, width: 2.5, fill: "#0b0c0e", stroke: (u, i) => u.series[i]._c }, drag: { x: false, y: false }, y: false },
    legend: { show: false }, scales: { x: { time: true }, y: { range: yRange } }, axes: [xAxis(), yAxis(yf)],
    series: [{}].concat(series.map(s => ({ label: s.label, stroke: s.c, width: s.w || 2, dash: s.dash, _c: s.c, _k: s.k, show: s.show !== false,
      paths: s.linear ? undefined : spline, points: { show: false }, spanGaps: false,
      fill: s.fill ? (u) => { const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + u.bbox.height); g.addColorStop(0, s.fill[0]); g.addColorStop(1, s.fill[1]); return g; } : undefined }))),
    plugins: [tipPlugin(tip, rowF)],
  }, [[], ...series.map(() => [])], el);
  u._keys = series.map(s => s.k);
  new ResizeObserver(() => { if (el.clientWidth) u.setSize({ width: el.clientWidth, height: el.clientHeight }); }).observe(el);
  el.addEventListener("mouseleave", () => tip.classList.remove("on"));
  S.charts.push(u); return u;
}
const HASH_SERIES = () => [
  { k: "h5s", label: "5s", c: "rgba(255,255,255,.26)", w: 1, linear: true, show: S.series.h5s },
  { k: "h15m", label: "15m avg", c: "rgba(255,210,150,.55)", w: 1.25, dash: [3, 4], show: S.series.h15m },
  { k: "h5m", label: "5m avg", c: "#f5a524", w: 2.25, fill: ["rgba(245,165,36,.30)", "rgba(245,165,36,0)"], show: S.series.h5m } ];
const hashRange = (u, lo, hi) => { if (lo == null) return [0, 5]; const p = Math.max(.2, (hi - lo) * .3); return [Math.max(0, lo - p), hi + p]; };
function drawCharts() {
  const c = Date.now() / 1000 - S.range, tg = tgtOf(), gap = Math.max(90, (S.pollS || (S.state && S.state.poll_seconds) || 5) * 8), h = [];
  S.hist.filter(x => x.t >= c).forEach((x, i, a) => { if (i && x.t - a[i - 1].t > gap) h.push({ t: a[i - 1].t + 1 }); h.push(x); });
  S.charts.forEach(u => {
    const ts = h.map(x => x.t);
    u.setData([ts, ...u._keys.map(k => k === "target" ? h.map(() => tg) : k === "eff" ? h.map(x => isNum(x.w) && x.h5m ? x.w / x.h5m : null) : h.map(x => isNum(x[k]) ? +x[k] : null))]);
    const e = u.root.parentElement.querySelector(".chart-empty");
    if (e) e.textContent = h.length < 2 ? `Collecting history… a new point every ${S.pollS || (S.state && S.state.poll_seconds) || 5}s.` : "";
  });
}
function rangeSeg() { return `<div class="seg" id="range">${[[3600, "1H"], [21600, "6H"], [86400, "24H"]].map(([r, l]) => `<button data-r="${r}" class="${S.range === r ? "on" : ""}">${l}</button>`).join("")}</div>`; }
function wireRange() { ["#range", "#range2"].forEach(sel => { const el = $(sel); if (!el) return; el.onclick = (e) => { const b = e.target.closest("button"); if (!b) return; S.range = +b.dataset.r;
  ["#range", "#range2"].forEach(x => $(x) && segSet($(x), "r", S.range)); drawCharts(); }; }); }
function spark(vals, color, id) {
  const pts = vals.filter(isNum); if (pts.length < 2) return "";
  let lo = Math.min(...pts), hi = Math.max(...pts); const mid = (lo + hi) / 2, minSp = Math.abs(mid) * .08 || 1; if (hi - lo < minSp) { lo = mid - minSp / 2; hi = mid + minSp / 2; } const sp = hi - lo, n = vals.length; let d = "", f = true;
  vals.forEach((v, i) => { if (!isNum(v)) return; d += (f ? "M" : "L") + (i / (n - 1) * 100).toFixed(2) + " " + (26 - (v - lo) / sp * 22).toFixed(2); f = false; });
  return `<svg viewBox="0 0 100 28" preserveAspectRatio="none"><defs><linearGradient id="sg${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${color}" stop-opacity=".22"/><stop offset="1" stop-color="${color}" stop-opacity="0"/></linearGradient></defs><path d="${d} L100 28 L0 28 Z" fill="url(#sg${id})"/><path d="${d}" fill="none" stroke="${color}" stroke-width="1.5" vector-effect="non-scaling-stroke" stroke-linejoin="round" stroke-linecap="round"/></svg>`;
}
const recent = (k, secs = 3600) => { const c = Date.now() / 1000 - secs; return S.hist.filter(h => h.t >= c).map(h => typeof k === "function" ? k(h) : h[k]); };

/* ---------------- shared bits ---------------- */
const D = () => S.data || {};
function heroVal() { const hr = D().hashrate || {}; return isNum(hr.ths_hero) ? +hr.ths_hero : isNum(hr.ths_5m) ? +hr.ths_5m : isNum(hr.ghs_spd) ? hr.ghs_spd / 1000 : null; }
function activePool() { const ps = D().pools || []; return ps.find(p => p.stratum_active) || ps.find(p => (p.status || "").toLowerCase() === "alive"); }
const kv = (rows) => rows.map(([k, v, i]) => `<div class="row-kv"><span>${i ? ic(i, "sm t3") : ""}${esc(k)}</span><b>${v}</b></div>`).join("");
function notices() {
  const d = D(), w = d.warnings || [];
  if (!d.ok) return `<div class="notice bad"><div class="ic">${ic("wifi-off", "sm")}</div><div><b>Can't read the miner</b><span>${esc((d.query_errors || []).join("; ") || "No reply.")}</span></div></div>`;
  if (!w.length) return `<div class="notice good"><div class="ic">${ic("circle-check", "sm")}</div><div><b>All systems normal</b><span>No errors or warnings reported by the miner.</span></div></div>`;
  return w.map(x => `<div class="notice warn"><div class="ic">${ic("triangle-alert", "sm")}</div><div><b>${esc(x.split(/(?<=\.)\s/)[0])}</b><span>${x.includes("temperature") ? "Chips are running hot. Check airflow or lower the work speed." : x.includes("hardware error") ? "A small hardware-error rate is normal; watch it if it keeps rising." : ""}</span></div></div>`).join("");
}
function sharesBlock() {
  const sh = D().shares || {}, er = D().errors || {}, tot = (+sh.accepted || 0) + (+sh.rejected || 0) + (+sh.stale || 0);
  return { tot, html: `<div class="label">Accepted shares</div>
    <div style="display:flex;align-items:baseline;gap:10px;margin-top:6px"><span class="big num" id="accV">${fmt(sh.accepted)}</span><span class="t2 num">${tot ? (sh.accepted / tot * 100).toFixed(2) + "%" : ""}</span></div>
    <div class="ratio">${tot ? `<i style="flex-grow:${sh.accepted};background:var(--good)"></i><i style="flex-grow:${Math.max(sh.stale, tot * .004)};background:var(--warn)"></i><i style="flex-grow:${Math.max(sh.rejected, tot * .004)};background:var(--bad)"></i>` : ""}</div>
    <div class="legend" style="margin-bottom:8px"><span><i style="background:var(--good)"></i>Accepted</span><span><i style="background:var(--warn)"></i>Stale <b class="num">${fmt(sh.stale_pct, 2)}%</b></span><span><i style="background:var(--bad)"></i>Rejected <b class="num">${fmt(sh.reject_pct, 2)}%</b></span></div>` };
}

/* ---------------- views ---------------- */
const VIEWS = {};
VIEWS.overview = {
  build: () => `
    <section class="hero">
      <div class="hero-top">
        <div><div class="hero-label">${ic("zap", "sm")}Hashrate <span class="t3">· 5-minute average</span></div>
          <div class="hero-num num"><span id="heroV" class="sk">0.000</span><span class="unit">TH/s</span></div>
          <div class="hero-meta" id="heroMeta"><span class="sk" style="width:280px;height:20px"></span></div></div>
        ${rangeSeg()}
      </div>
      <div class="hero-chart reveal" id="chHero"><div class="chart-empty"></div></div>
      <div class="hero-range">${rangeSeg().replace('id="range"', 'id="range2"')}</div>
    </section>
    <div id="linkCard"></div>
    <section class="panel strip" id="strip">${Array.from({ length: 5 }, () => '<div class="metric"><div class="sk" style="height:14px;width:60%"></div><div class="sk" style="height:28px;width:70%;margin-top:6px"></div><div class="sk" style="height:12px;width:50%;margin-top:6px"></div></div>').join("")}</section>
    <div class="cols">
      <section class="panel panel-pad span-7"><div class="sec-h"><h2>Status</h2><span class="t3" id="stUpd"></span></div><div id="notices"><div class="sk" style="height:48px"></div></div></section>
      <section class="panel panel-pad span-5"><div class="sec-h"><h2>Mining</h2><a class="t3" href="#/pools" style="display:inline-flex;align-items:center;gap:4px;font-size:13px">Pools${ic("chevron-right", "sm")}</a></div><div id="mining"><div class="sk" style="height:120px"></div></div></section>
    </div>`,
  wire: () => {
    wireRange();
    chart($("#chHero"), HASH_SERIES().filter(s => s.k !== "h5s").map(s => ({ ...s, show: true })), (u, v) => v.map(x => x.toFixed(1)), v => fmt(v, 3) + " TH/s", hashRange);
  },
  update: () => {
    const d = D(), hr = d.hashrate || {}, tp = d.temps || {}, pw = d.power || {}, fan = d.fan || {}, up = d.uptime || {}, er = d.errors || {};
    const hv = unsk($("#heroV")); if (d.ok) tween(hv, heroVal(), 3); else { hv.textContent = "Offline"; hv.dataset.v = ""; }
    const h1 = S.hist.find(h => h.t >= Date.now() / 1000 - 3600 && isNum(h.h5m)); let delta = '<span class="delta">collecting history</span>';
    if (h1 && heroVal() && S.hist.length > 20) { const p = (heroVal() - h1.h5m) / h1.h5m * 100; delta = `<span class="delta ${p >= .05 ? "up" : p <= -.05 ? "down" : ""}">${p >= 0 ? "↑" : "↓"} ${Math.abs(p).toFixed(1)}% <span style="opacity:.7;font-weight:500">1h</span></span>`; }
    const hashing = er.hash_status == 1 || (er.hash_status == null && d.ok), ap = activePool();
    $("#heroMeta").innerHTML = `${delta}<span class="pill ${d.ok ? (hashing ? "good" : "bad") : "bad"}"><span class="dot"></span>${d.ok ? (hashing ? "Hashing" : "Not hashing") : "Offline"}</span>` +
      (ap ? `<span class="sep"></span><span>${esc((ap.stratum_url || ap.url || "").replace(/^stratum\+tcp:\/\//, "").replace(/:\d+$/, ""))}</span>` : "") +
      `<span class="sep desk-meta"></span><span class="desk-meta">5s <b class="num">${fmt(hr.ths_5s, 2)}</b></span><span class="sep desk-meta"></span><span class="desk-meta">15m <b class="num">${fmt(hr.ths_15m, 2)}</b></span>`;
    const hv5 = heroVal(), eff = isNum(pw.watts) && hv5 ? pw.watts / hv5 : null, tg = tgtOf(), sh = d.shares || {};
    const M = (icn, label, val, unit, sub, extra, color) => `<div class="metric"><div class="label">${ic(icn)}${label}</div><div class="val num"${color ? ` style="color:${color}"` : ""}>${val}<small>${unit}</small></div><div class="sub">${sub}</div>${extra || ""}</div>`;
    $("#strip").innerHTML =
      M("thermometer", "Max chip temp", fmt(tp.max), "°C", `avg <b>${fmt(tp.avg)}°</b> · target <b>${fmt(tp.target)}°</b>`, `<div class="bar"><i style="width:${isNum(tp.max) ? Math.min(100, tp.max / 110 * 100) : 0}%;background:${tempTone(tp.max)}"></i><em style="left:${tg / 110 * 100}%"></em></div>`, tempTone(tp.max)) +
      M("plug", "Power", fmt(pw.watts), "W", `<b>${fmt(pw.voltage_v, 2)}</b> V · MPO <b>${fmt(pw.mpo_w)}</b> W`, `<div class="spark">${spark(recent("w"), "#9aa0a9", "p")}</div>`) +
      M("leaf", "Efficiency", fmt(eff, 1), "J/TH", `<b>${hv5 && pw.watts ? fmt(hv5 * 1000 / pw.watts, 1) : "—"}</b> GH/W`, `<div class="spark">${spark(recent(h => isNum(h.w) && h.h5m ? h.w / h.h5m : null), "#9aa0a9", "e")}</div>`) +
      M("fan", "Fan", fmt(fan.rpm), "RPM", `<b>${fmt(fan.percent)}%</b> of max speed`, `<div class="bar"><i style="width:${isNum(fan.percent) ? fan.percent : 0}%"></i></div>`) +
      M("clock", "Uptime", esc(up.human || "—"), "", `since <b>${up.seconds ? esc(absTime(Math.floor(Date.now() / 1000) - up.seconds)) : "—"}</b>`);
    $("#stUpd").innerHTML = `Updated <span data-rel="${d.fetched_at_unix || 0}">${rel(d.fetched_at_unix)}</span>`;
    $("#notices").innerHTML = notices();
    linkCard();
  },
};
VIEWS.performance = {
  build: () => `
    <section class="panel panel-pad">
      <div class="sec-h"><h2>Hashrate</h2>${rangeSeg()}</div>
      <div class="legend" id="hsLegend" style="margin-bottom:8px"></div>
      <div class="chart reveal" id="chPerf" style="height:var(--chart-h)"><div class="chart-empty"></div></div>
    </section>
    <section class="panel strip" id="hrStrip"></section>
    <div class="cols">
      <section class="panel panel-pad span-6"><div class="sec-h"><h2>Shares</h2></div><div id="shares"><div class="sk" style="height:220px"></div></div></section>
      <section class="panel panel-pad span-6"><div class="sec-h"><h2>Power <span class="t3">watts</span></h2></div>
        <div class="chart reveal" id="chPow" style="height:150px"><div class="chart-empty"></div></div><div class="list" id="powList" style="margin-top:12px"></div></section>
    </div>`,
  wire: () => {
    wireRange();
    chart($("#chPerf"), HASH_SERIES(), (u, v) => v.map(x => x.toFixed(1)), v => fmt(v, 3) + " TH/s", hashRange);
    chart($("#chPow"), [{ k: "w", label: "Power", c: "#d4d7dd", w: 1.75, fill: ["rgba(255,255,255,.10)", "rgba(255,255,255,0)"] }], (u, v) => v.map(x => x + "W"), v => fmt(v) + " W",
      (u, lo, hi) => lo == null ? [0, 200] : [Math.max(0, Math.floor(lo * .9 / 10) * 10), Math.ceil(hi * 1.08 / 10) * 10]);
    const lg = $("#hsLegend");
    lg.innerHTML = HASH_SERIES().map(s => `<button data-k="${s.k}" class="${S.series[s.k] ? "" : "off"}"><i style="background:${s.c};width:12px;height:${s.k === "h5m" ? 3 : 2}px;border-radius:2px;display:inline-block"></i>${s.label}</button>`).join("");
    lg.onclick = (e) => { const b = e.target.closest("button"); if (!b) return; const k = b.dataset.k; S.series[k] = !S.series[k]; b.classList.toggle("off", !S.series[k]);
      const u = S.charts[0]; const i = u._keys.indexOf(k) + 1; u.setSeries(i, { show: S.series[k] }); };
  },
  update: () => {
    const d = D(), hr = d.hashrate || {}, sh = d.shares || {}, er = d.errors || {}, pw = d.power || {}, c = d.control || {};
    const M = (l, v, u, s) => `<div class="metric"><div class="label">${l}</div><div class="val num">${v}<small>${u}</small></div>${s ? `<div class="sub">${s}</div>` : ""}</div>`;
    $("#hrList").innerHTML = kv([["5 seconds", `${fmt(hr.ths_5s, 2)} <span class="t3">· short-term</span>`], ["1 minute", fmt(hr.ths_1m, 2)], ["5 minutes", `<span style="color:var(--accent)">${fmt(hr.ths_5m, 2)}</span>`],
      ["15 minutes", fmt(hr.ths_15m, 2)], ["Lifetime average", fmt(hr.ths_avg, 2)], ["GHS spd · mm", `${fmt(hr.ghs_spd, 0)} · ${fmt(hr.ghs_mm, 0)}`]]);
    const ap = activePool();
    const echu = (er.echu && er.echu.length) ? er.echu.join(" ") : "0";
    $("#shares").innerHTML = sharesBlock().html + `<div class="list two">${kv([
      ["Rejected", `${fmt(sh.rejected)} <span class="t3">· ${fmt(sh.reject_pct, 2)}%</span>`], ["Stale", `${fmt(sh.stale)} <span class="t3">· ${fmt(sh.stale_pct, 2)}%</span>`],
      ["Hardware errors", `${fmt(sh.hw_errors)} <span class="t3">· ${isNum(sh.dh_pct) ? fmt(sh.dh_pct, 3) + "%" : "—"}</span>`], ["Best share", `<span title="${fmt(sh.best_share)}">${si(sh.best_share)}</span>`],
      ["Pool ping", isNum(er.ping_ms) ? fmt(er.ping_ms) + " ms" : "—"], ["Get / remote fails", `${fmt(sh.get_failures)} / ${fmt(sh.remote_failures)}`],
      ["ECHU / ECMM / CRC", `${esc(echu)} / ${fmt(er.ecmm)} / ${fmt(er.crc)}`], ["Work utility", fmt(hr.wu, 0)], ["GHS avg", fmt(hr.ghs_avg, 1)], ["Blocks found", fmt(sh.found_blocks)],
      ["Last share", ap && ap.last_share_unix ? `<span data-rel="${ap.last_share_unix}">${rel(ap.last_share_unix)}</span>` : "—"], ["Block height", ap && ap.block_height ? fmt(ap.block_height) : "—"]])}</div>`;
    const hv = heroVal();
    $("#powList").innerHTML = kv([["Draw", `${fmt(pw.watts)} W <span class="t3">· ${fmt(pw.voltage_v, 2)} V</span>`], ["Efficiency", isNum(pw.watts) && hv ? `${fmt(pw.watts / hv, 1)} J/TH` : "—"],
      ["MPO limit", isNum(pw.mpo_w) ? fmt(pw.mpo_w) + " W" : "—"], ["Work speed", c.work_name ? cap(c.work_name) : (isNum(pw.work_level) ? "Level " + pw.work_level : "—")],
      ["Power supply", `${pw.ps_ok === false ? '<span style="color:var(--bad)">Fault</span>' : "OK"} <span class="t3 mono">${esc((pw.ps_raw || []).join(" "))}</span>`]]);
  },
};
VIEWS.hardware = {
  build: () => `
    <div class="cols">
      <section class="panel panel-pad span-8"><div class="sec-h"><h2>Temperatures</h2>${rangeSeg()}</div>
        <div class="legend" id="tLegend" style="margin-bottom:8px"></div><div class="chart reveal" id="chTemp" style="height:calc(var(--chart-h) * .78)"><div class="chart-empty"></div></div></section>
      <section class="panel panel-pad span-4"><div class="sec-h"><h2>Thermals</h2></div><div class="list" id="thermList"><div class="sk" style="height:200px"></div></div></section>
      <section class="panel panel-pad span-12"><div class="sec-h"><h2>Chips <span class="t3" id="chipHint"></span></h2><span class="ramp">cool<i></i>hot</span></div>
        <div class="heat num" id="heat">${Array.from({ length: 10 }, () => '<div class="cell sk"></div>').join("")}</div></section>
      <section class="panel panel-pad span-12"><div class="sec-h"><h2>Device</h2></div><div class="list three" id="devList"></div></section>
      <section class="panel panel-pad span-6"><div class="sec-h"><h2>Network drops <span class="t3" id="nfN"></span></h2></div><div class="list" id="nfList"></div></section>
      <div class="span-6" style="display:flex;flex-direction:column;gap:12px"><a class="link-row" href="#/raw">${ic("table", "t3")}<span class="grow">All miner fields</span><span class="t3" id="rawCount"></span>${ic("chevron-right", "sm t3")}</a></div>
    </div>`,
  wire: () => {
    wireRange();
    chart($("#chTemp"), [{ k: "tmax", label: "Max", c: "#f06363", w: 2 }, { k: "tavg", label: "Avg", c: "#f5a524", w: 2 },
      { k: "tamb", label: "Board", c: "#3ccf8e", w: 1.5 }, { k: "tout", label: "Outlet", c: "#6ea8fe", w: 1.5 }, { k: "target", label: "Target", c: "rgba(255,255,255,.35)", w: 1, dash: [3, 4], linear: true }],
      (u, v) => v.map(x => x + "°"), v => fmt(v) + "°C", (u, lo, hi) => lo == null ? [20, 100] : [Math.max(0, Math.floor((lo - 6) / 10) * 10), Math.ceil((hi + 4) / 10) * 10]);
  },
  update: () => {
    const d = D(), tp = d.temps || {}, fan = d.fan || {}, id = d.identity || {}, ch = d.chips || {}, er = d.errors || {}, dev = d.device || {}, pw = d.power || {};
    $("#tLegend").innerHTML = [["Max", tp.max, "#f06363"], ["Avg", tp.avg, "#f5a524"], ["Board", tp.ambient, "#3ccf8e"], ["Outlet", tp.outlet, "#6ea8fe"]].map(([k, v, c]) => `<span><i style="background:${c}"></i>${k} <b class="num">${fmt(v)}°</b></span>`).join("") + `<span><i style="background:rgba(255,255,255,.35)"></i>Target <b class="num">${fmt(tp.target)}°</b></span>`;
    $("#thermList").innerHTML = kv([["Max chip", `<span style="color:${tempTone(tp.max)}">${fmt(tp.max)}°C</span>`, "flame"], ["Average", fmt(tp.avg) + "°C", "thermometer"], ["Target", fmt(tp.target) + "°C", "gauge"],
      ["Board", fmt(tp.ambient) + "°C", "circuit-board"], ["Outlet", fmt(tp.outlet) + "°C", "waypoints"], ["Fan", `${fmt(fan.rpm)} RPM <span class="t3">· ${fmt(fan.percent)}%</span>`, "fan"]]);
    const ct = ch.temps || [], cv = ch.volts_mv || [], cn = ch.nonces || [], n = Math.max(ct.length, cv.length, cn.length);
    if (n) { const v = ct.filter(isNum), lo = Math.min(...v), hi = Math.max(...v), tg = tgtOf(), mwMax = Math.max(...cn.filter(isNum), 1);
      $("#chipHint").textContent = v.length ? `${fmt(lo)}–${fmt(hi)}°C · spread ${fmt(hi - lo)}°` : "";
      $("#heat").innerHTML = Array.from({ length: n }, (_, i) => `<div class="cell" style="--c:${heat(ct[i], Math.min(lo - 4, tg - 6), Math.max(hi + 2, tg + 4))}"><div class="n"><span>Chip ${i + 1}</span>${ct[i] === hi ? ic("flame", "sm") : ""}</div>
        <div class="t">${isNum(ct[i]) ? fmt(ct[i]) : "—"}<small>°C</small></div>
        <div><div class="m"><span>${isNum(cv[i]) ? fmt(cv[i]) + " mV" : ""}</span><span class="v2">${isNum(cn[i]) ? si(cn[i], 1) + " MW" : ""}</span></div><div class="mw"><i style="width:${isNum(cn[i]) ? cn[i] / mwMax * 100 : 0}%"></i></div></div></div>`).join("");
    } else { $("#heat").innerHTML = '<div class="t3">No per-chip data in this reply.</div>'; }
    const c = d.control || {};
    $("#devList").innerHTML = kv([["Model", esc([id.product, id.model].filter(Boolean).join(" ") || "—")], ["Core", esc(id.core ? `${id.core} · ${id.chips || "?"} chips` : "—")],
      ["Firmware", `<span class="mono">${esc(id.firmware || "—")}</span>`], ["Hardware", `<span class="mono">${esc(id.hardware || "—")}</span>`],
      ["Type", `<span class="mono">${esc([id.hwtype, id.swtype].filter(Boolean).join(" / ") || "—")}</span>`], ["cgminer · API", `${esc(id.cgminer || "—")} · ${esc(id.api || "—")}`],
      ["Device", esc([dev.name, dev.status].filter(Boolean).join(" · ") || "—")], ["Work speed", c.work_name ? cap(c.work_name) : "—"],
      ["Light", c.led_mode ? `${c.led_hex ? `<span class="sw-i" style="background:${esc(c.led_hex)}"></span>` : ""}${esc(cap(c.led_mode))}${isNum(c.led_brightness) ? " · " + c.led_brightness + "%" : ""}` : (er.led ? "LED " + esc(er.led) : "—")],
      ["Soft-off", esc(er.soft_off == null ? "0" : er.soft_off)], ["Host", d.source === "remote" ? "Preview" : `<span class="mono">${esc(d.miner_host || "—")}</span>`], ["Uptime", esc((d.uptime || {}).human || "—")]]);
    const nf = (er.net_fail || []).slice().reverse();
    $("#nfN").textContent = nf.length ? `${nf.length} recorded` : "";
    $("#nfList").innerHTML = nf.length ? nf.slice(0, 6).map(x => `<div class="row-kv"><span>${ic("wifi-off", "sm t3")}${esc(absTime(x.unix))}</span><b class="t2" data-rel="${x.unix}">${rel(x.unix)}</b></div>`).join("") : '<div class="row-kv"><span>None recorded</span><b></b></div>';
    $("#rawCount").textContent = Object.keys(d.mm_fields || {}).length + " fields";
  },
};
VIEWS.pools = {
  build: () => `<section class="panel" id="poolList"><div class="pool"><div class="sk" style="height:40px;grid-column:1/-1"></div></div></section>
    <section class="panel panel-pad"><div class="sec-h"><h2>Connection</h2></div><div class="list three" id="poolStats"></div></section>`,
  update: () => {
    const d = D(), ps = d.pools || [], sh = d.shares || {}, er = d.errors || {};
    $("#poolList").innerHTML = ps.length ? ps.map(p => { const alive = (p.status || "").toLowerCase() === "alive", url = (p.url || "").replace(/^stratum\+tcp:\/\//, "");
      return `<div class="pool${p.stratum_active ? " active" : ""}"><div class="pi">${ic(p.stratum_active ? "zap" : alive ? "globe" : "wifi-off", "sm")}</div>
        <div style="min-width:0"><div class="pu">${esc(url)}</div><div class="pw mono">${esc(p.user || "")}</div>
          <div class="pstats num"><div><span>Accepted</span><b>${fmt(p.accepted)}</b></div><div><span>Rejected · stale</span><b>${fmt(p.rejected)} · ${fmt(p.stale)}</b></div>
            <div><span>Difficulty</span><b>${fmt(p.difficulty)}</b></div><div><span>Last share</span><b>${p.last_share_unix ? `<span data-rel="${p.last_share_unix}">${rel(p.last_share_unix)}</span>` : "never"}</b></div>
            ${p.block_height ? `<div><span>Block</span><b>${fmt(p.block_height)}</b></div>` : ""}</div></div>
        <div>${p.stratum_active ? '<span class="pill accent"><span class="dot" style="background:var(--accent)"></span>Active</span>' : alive ? '<span class="pill">Standby</span>' : `<span class="pill bad"><span class="dot"></span>${esc(p.status || "Down")}</span>`}</div></div>`; }).join("")
      : '<div class="pool"><span class="t3" style="grid-column:1/-1">No pools reported.</span></div>';
    $("#poolStats").innerHTML = kv([["Pools alive", `${ps.filter(p => (p.status || "").toLowerCase() === "alive").length} of ${ps.length}`], ["Pool ping", isNum(er.ping_ms) ? fmt(er.ping_ms) + " ms" : "—"],
      ["Best share", si(sh.best_share)], ["Get failures", fmt(sh.get_failures)], ["Remote failures", fmt(sh.remote_failures)], ["Network blocks", fmt(sh.network_blocks)]]);
  },
};

/* ---------------- controls ---------------- */
const PRESETS = [["Red", "#ff3b30"], ["Orange", "#ff6a00"], ["Amber", "#f0a020"], ["Yellow", "#ffd60a"], ["Green", "#30d158"],
  ["Cyan", "#40c8e0"], ["Blue", "#0a84ff"], ["Purple", "#8000ff"], ["Pink", "#ff40a0"], ["White", "#ffffff"]];
const LEVEL = { low: "Low", medium: "Medium", high: "High" };
const MODE = { off: "Off", fixed: "Steady", flash: "Flash", pulse: "Pulse", loop: "Loop" };
VIEWS.controls = {
  build: () => `
    <div id="ctlBanner"></div>
    <div class="cols" id="ctlCols">
      <section class="panel panel-pad span-12 ws-panel" id="wsPanel"><div class="ws-text">
        <div class="ctl-head"><h2 style="margin:0;font-size:15px;font-weight:600">Work speed</h2><span class="t3" id="wsNow">—</span></div>
        <p class="ctl-desc">Higher speed means more hashrate, power, heat and fan noise.</p></div>
        <div class="ws-ctl"><div class="seg lg full" id="wsSeg"><button data-l="low">Low</button><button data-l="medium">Medium</button><button data-l="high">High</button></div>
        <div class="status-line" id="wsMsg"></div></div>
      </section>
      <section class="panel panel-pad span-12" id="ledPanel">
        <div class="ctl-head"><h2 style="margin:0;font-size:15px;font-weight:600">Light</h2><span class="t3" id="ledNow">—</span></div>
        <p class="ctl-desc">Colour, brightness and effect of the ring on the front of the miner.</p>
        <div class="cols" style="--gap:24px">
          <div class="span-6"><div class="device" id="dev"><div class="glow"></div><div class="unit"><div class="ring"></div></div><div class="cap" id="devCap"></div></div></div>
          <div class="span-6">
            <div class="ctl-field"><div class="lab">Colour <span id="hexLab" class="mono"></span></div>
              <div class="chips" id="chips">${PRESETS.map(([n, h]) => `<button class="chip-c" data-hex="${h}" title="${n}" aria-label="${n}" style="--sw:${h}"></button>`).join("")}<label class="custom-c" id="customC" title="Custom colour"><input type="color" id="cPick" aria-label="Custom colour"></label></div></div>
            <div class="ctl-field"><div class="lab">Brightness <span class="num" id="brVal">100%</span></div>
              <div class="slider">${ic("lightbulb", "sm")}<input type="range" id="br" min="0" max="100" value="100" aria-label="Brightness"></div></div>
            <div class="ctl-field"><div class="lab">Effect</div>
              <div class="seg full" id="fxSeg">${Object.entries(MODE).map(([k, v]) => `<button data-m="${k}">${v}</button>`).join("")}</div></div>
          </div>
        </div>
        <div class="apply-bar"><span class="t3" id="ledDirty">No changes</span><button class="btn ghost" id="ledReset" disabled>Reset</button><button class="btn" id="ledApply" disabled>Apply</button></div>
        <div class="status-line" id="ledMsg"></div>
      </section>
    </div>`,
  wire: () => {
    $("#wsSeg").onclick = async (e) => {
      const b = e.target.closest("button"); if (!b || !S.can) return;
      const l = b.dataset.l, cur = (D().control || {}).work_name;
      if (l === cur) return;
      if (!await confirmDialog(`Set work speed to ${LEVEL[l]}?`, `From ${cur ? LEVEL[cur] || cur : "the current setting"} to <b style="color:var(--t1)">${LEVEL[l]}</b>. Hashrate, power use, heat and fan noise will change. The miner may take a minute to settle.`, `Set ${LEVEL[l]}`)) return;
      send("/api/control/workmode", { miner: S.miner.id, mode: l }, `Work speed set to ${LEVEL[l]}`, "#wsMsg");
    };
    $("#chips").onclick = (e) => { const b = e.target.closest(".chip-c"); if (b) setLed({ color: b.dataset.hex }); };
    $("#cPick").oninput = (e) => setLed({ color: e.target.value });
    $("#br").oninput = (e) => setLed({ bright: +e.target.value });
    $("#fxSeg").onclick = (e) => { const b = e.target.closest("button"); if (b) setLed({ mode: b.dataset.m }); };
    $("#ledReset").onclick = () => { S.ctl.init = false; ctlFromMiner(); };
    $("#ledApply").onclick = async () => {
      if (!S.can) return; const { mode, color, bright } = S.ctl;
      if (!await confirmDialog("Update the miner light?", `<span class="sw-i" style="background:${esc(color)}"></span><b style="color:var(--t1)">${MODE[mode]}</b> · <span class="mono">${esc(color)}</span> · ${bright}% brightness`, "Apply")) return;
      const c = D().control || {}, body = { miner: S.miner.id, mode, color, brightness: bright }; if (Number.isInteger(c.led_temper)) body.temper = c.led_temper;
      send("/api/control/led", body, "Light updated", "#ledMsg", () => { S.ctl.dirty = false; ledUI(); });
    };
    ctlFromMiner(); ledUI();
  },
  update: () => {
    const c = D().control || {}, remote = D().source === "remote";
    $("#ctlBanner").innerHTML = S.can ? "" : `<div class="banner">${ic(remote ? "lock" : "info")}<div><b>${remote ? "Read-only preview." : "Controls unavailable."}</b> ${remote ? "Controls are switched off here, so nothing on this page can change this miner. When you run ${esc(APP)} on your own network, they talk directly to your miner." : "This miner can't be controlled from here."}</div></div>`;
    $("#ctlCols").classList.toggle("ctl-locked", !S.can);
    $$("#ctlCols button, #ctlCols input").forEach(x => x.disabled = !S.can || x.id === "ledApply" && !S.ctl.dirty || x.id === "ledReset" && !S.ctl.dirty);
    $("#wsNow").textContent = c.work_name ? "Currently " + (LEVEL[c.work_name] || c.work_name) : "—";
    segSet($("#wsSeg"), "l", c.work_name || "");
    $("#ledNow").innerHTML = c.led_mode ? `Currently ${c.led_hex ? `<span class="sw-i" style="background:${esc(c.led_hex)}"></span>` : ""}${esc(MODE[c.led_mode] || c.led_mode)}${isNum(c.led_brightness) ? " · " + c.led_brightness + "%" : ""}` : "—";
    if (!S.ctl.init) ctlFromMiner();
  },
};
function ctlFromMiner() {
  const c = D().control; if (!c) return;
  if (c.led_hex) S.ctl.color = c.led_hex; if (Number.isInteger(c.led_brightness)) S.ctl.bright = c.led_brightness; if (c.led_mode && MODE[c.led_mode]) S.ctl.mode = c.led_mode;
  S.ctl.init = true; S.ctl.dirty = false; ledUI();
}
function setLed(p) { Object.assign(S.ctl, p); S.ctl.dirty = true; ledUI(); }
function ledUI() {
  const dv = $("#dev"); if (!dv) return; const { mode, color, bright, dirty } = S.ctl;
  dv.style.setProperty("--lc", color); dv.style.setProperty("--lb", Math.max(.05, bright / 100));
  dv.className = "device m-" + mode;
  $("#devCap").innerHTML = `<span class="sw-i" style="background:${esc(color)}"></span>${MODE[mode]} · ${bright}%`;
  $$("#chips .chip-c").forEach(b => b.classList.toggle("on", b.dataset.hex === color.toLowerCase()));
  $("#customC").classList.toggle("on", !PRESETS.some(p => p[1] === color.toLowerCase()));
  $("#cPick").value = color; $("#hexLab").textContent = color;
  const br = $("#br"); br.value = bright; br.style.setProperty("--p", bright + "%"); $("#brVal").textContent = bright + "%";
  segSet($("#fxSeg"), "m", mode);
  $("#ledDirty").textContent = dirty ? "Unsaved changes" : "No changes";
  $("#ledApply").disabled = $("#ledReset").disabled = !dirty || !S.can;
  $("#ledApply").className = "btn" + (dirty ? " accent" : "");
}
async function send(path, body, okText, msgSel, after) {
  const m = $(msgSel); m.className = "status-line on"; m.innerHTML = '<span class="spin"></span>Sending to the miner, then reading it back…';
  $$("#ctlCols button, #ctlCols input").forEach(x => x.disabled = true);
  try {
    const r = await api(path, { method: "POST", body });
    if (r.data) S.data = r.data; if (after) after();
    update(false);
    const c = r.control || {}, mm = $(msgSel);
    if (mm) { mm.className = "status-line on ok"; mm.innerHTML = `${ic("circle-check", "sm")}<span><b style="color:var(--t1);font-weight:550">${esc(okText)}.</b> Miner replied “${esc(r.message || "OK")}”. Now ${esc(LEVEL[c.work_name] || c.work_name || "—")} · light ${esc(MODE[c.led_mode] || c.led_mode || "—")}.</span>`; }
    toast(okText);
  } catch (e) { update(false); const mm = $(msgSel); if (mm) { mm.className = "status-line on bad"; mm.innerHTML = `${ic("circle-alert", "sm")}<span><b>Not applied.</b> ${esc(e.message)}</span>`; } }
}
function confirmDialog(title, html, okLabel) {
  return new Promise((resolve) => {
    const sc = document.createElement("div"); sc.className = "scrim";
    const d = document.createElement("div"); d.className = "dialog"; d.setAttribute("role", "alertdialog"); d.setAttribute("aria-modal", "true");
    d.innerHTML = `<h2>${esc(title)}</h2><p>${html}</p><div class="acts"><button class="btn ghost" data-a="0">Cancel</button><button class="btn accent" data-a="1">${esc(okLabel)}</button></div>`;
    document.body.append(sc, d); requestAnimationFrame(() => { sc.classList.add("on"); d.classList.add("on"); });
    const done = (v) => { sc.classList.remove("on"); d.classList.remove("on"); setTimeout(() => { sc.remove(); d.remove(); }, 220); document.removeEventListener("keydown", key); resolve(v); };
    const key = (e) => { if (e.key === "Escape") done(false); };
    document.addEventListener("keydown", key); sc.onclick = () => done(false);
    $$("[data-a]", d).forEach(b => b.onclick = () => done(b.dataset.a === "1")); $('[data-a="1"]', d).focus();
  });
}

/* ---------------- the single dashboard page ---------------- */
const skList = (h) => `<div class="sk" style="height:${h}px"></div>`;
const DASH = {
  build: () => `
    <section class="hero" id="secHash">
      <div class="hero-top">
        <div><div class="hero-label">${ic("zap", "sm")}Hashrate <span class="t3">· 5-minute average</span></div>
          <div class="hero-num num"><span id="heroV" class="sk">0.000</span><span class="unit">TH/s</span></div>
          <div class="hero-meta" id="heroMeta"><span class="sk" style="width:280px;height:20px"></span></div></div>
        <div class="hero-side"><div class="legend" id="hsLegend"></div>${rangeSeg()}</div>
      </div>
      <div class="hero-chart reveal" id="chHero"><div class="chart-empty"></div></div>
      <div class="hero-range">${rangeSeg().replace('id="range"', 'id="range2"')}</div>
    </section>
    <div id="linkCard"></div>
    <section class="panel strip" id="strip">${Array.from({ length: 5 }, () => '<div class="metric"><div class="sk" style="height:14px;width:60%"></div><div class="sk" style="height:28px;width:70%;margin-top:6px"></div><div class="sk" style="height:12px;width:50%;margin-top:6px"></div></div>').join("")}</section>
    <div class="cols dash">
      ${LOTTO.build()}
      <section class="panel panel-pad span-7" id="secControls"${isView() ? ' hidden' : ''}>
        <div class="sec-h"><h2>Controls</h2><span class="t3 ctl-hint">${ic("lock", "sm")}Every change asks you to confirm</span></div>
        <div id="ctlBanner"></div>
        <div id="ctlCols">
          <div class="ctl-row"><div class="ctl-head"><b>Work speed</b><span class="t3" id="wsNow">—</span></div>
            <div class="seg lg full" id="wsSeg"><button data-l="low">Low</button><button data-l="medium">Medium</button><button data-l="high">High</button></div>
            <p class="ctl-desc">Higher speed means more hashrate, power, heat and fan noise.</p>
            <div class="status-line" id="wsMsg"></div></div>
          <div class="ctl-row" id="ledPanel"><div class="ctl-head"><b>Light</b><span class="t3" id="ledNow">—</span></div>
            <div class="led-grid">
              <div class="device" id="dev"><div class="glow"></div><div class="unit"><div class="ring"></div></div><div class="cap" id="devCap"></div></div>
              <div class="led-fields">
                <div class="ctl-field"><div class="lab">Colour <span id="hexLab" class="mono"></span></div>
                  <div class="chips" id="chips">${PRESETS.map(([n, h]) => `<button class="chip-c" data-hex="${h}" title="${n}" aria-label="${n}" style="--sw:${h}"></button>`).join("")}<label class="custom-c" id="customC" title="Custom colour"><input type="color" id="cPick" aria-label="Custom colour"></label></div></div>
                <div class="ctl-field"><div class="lab">Brightness <span class="num" id="brVal">100%</span></div>
                  <div class="slider">${ic("lightbulb", "sm")}<input type="range" id="br" min="0" max="100" value="100" aria-label="Brightness"></div></div>
                <div class="ctl-field"><div class="lab">Mode</div>
                  <div class="seg full" id="fxSeg">${Object.entries(MODE).map(([k, v]) => `<button data-m="${k}">${v}</button>`).join("")}</div></div>
              </div>
            </div>
            <div class="apply-bar"><span class="t3" id="ledDirty">No changes</span><button class="btn ghost" id="ledReset" disabled>Reset</button><button class="btn" id="ledApply" disabled>Apply light</button></div>
            <div class="status-line" id="ledMsg"></div></div>
        </div>
      </section>
      <div class="${isView() ? "span-12 stack two-up" : "span-5 stack"}">
        <section class="panel panel-pad" id="secStatus"><div class="sec-h"><h2>Status</h2><span class="t3" id="stUpd"></span></div><div id="notices">${skList(48)}</div></section>
        <section class="panel panel-pad"><div class="sec-h"><h2>Hashrate</h2><span class="t3">TH/s</span></div><div class="list" id="hrList">${skList(150)}</div></section>
        <section class="panel panel-pad" id="autoCard">${skList(120)}</section>
      </div>
      <section class="panel panel-pad span-8" id="secTemps"><div class="sec-h"><h2>Temperature</h2><span class="t3">same range as hashrate</span></div>
        <div class="legend" id="tLegend" style="margin-bottom:8px"></div><div class="chart reveal" id="chTemp" style="height:calc(var(--chart-h) * .7)"><div class="chart-empty"></div></div></section>
      <section class="panel panel-pad span-4"><div class="sec-h"><h2>Thermals &amp; fan</h2></div><div class="list" id="thermList">${skList(200)}</div></section>
      <section class="panel panel-pad span-12" id="secChips"><div class="sec-h"><h2>Chips <span class="t3" id="chipHint"></span></h2><span class="ramp">cool<i></i>hot</span></div>
        <div class="heat num" id="heat">${Array.from({ length: 10 }, () => '<div class="cell sk"></div>').join("")}</div></section>
      <section class="panel panel-pad span-6" id="secShares"><div class="sec-h"><h2>Shares &amp; errors</h2></div><div id="shares">${skList(220)}</div></section>
      <section class="panel panel-pad span-6" id="secPower"><div class="sec-h"><h2>Power <span class="t3">watts</span></h2></div>
        <div class="chart reveal" id="chPow" style="height:150px"><div class="chart-empty"></div></div><div class="list" id="powList" style="margin-top:12px"></div></section>
      <section class="panel span-7 pools-card" id="secPools"><div class="sec-h panel-pad-h"><h2>Pools</h2></div><div id="poolList"><div class="pool"><div class="sk" style="height:40px;grid-column:1/-1"></div></div></div>
        <div class="panel-pad pools-stats"><div class="list two" id="poolStats"></div></div></section>
      <section class="panel panel-pad span-5" id="secDevice"><div class="sec-h"><h2>Device</h2></div><div class="list" id="devList"></div>
        <div class="sec-h" style="margin-top:20px"><h2>Network drops <span class="t3" id="nfN"></span></h2></div><div class="list" id="nfList"></div></section>
      <details class="panel span-12 raw-card" id="secRaw"><summary class="panel-pad-h">${ic("table", "sm t3")}<span class="grow">All miner fields</span><span class="t3" id="rawCount"></span>${ic("chevron-down", "sm t3 chev")}</summary>
        <div class="raw-in"><div class="search">${ic("search", "sm")}<input class="input" id="rawQ" placeholder="Filter fields" autocomplete="off" autocapitalize="off" spellcheck="false"></div>
        <div class="raw-table" id="rawT"></div></div></details>
    </div>
    <footer class="foot one-foot"><span>${logo.replace('width="28" height="28"', 'width="16" height="16"')}<b>${esc(APP)}</b> ${esc(S.state.version || "")}${TAG ? ` · ${esc(TAG)}` : ""}</span><span id="footHost">${S.miner.kind === "remote" ? "Read-only preview" : esc(S.miner.host + ":" + (S.miner.port || 4028))}</span></footer>`,
  wire: () => {
    wireRange();
    chart($("#chHero"), HASH_SERIES(), (u, v) => v.map(x => x.toFixed(1)), v => fmt(v, 3) + " TH/s", hashRange);
    const lg = $("#hsLegend");
    lg.innerHTML = HASH_SERIES().map(s => `<button data-k="${s.k}" class="${S.series[s.k] ? "" : "off"}"><i style="background:${s.c};width:12px;height:${s.k === "h5m" ? 3 : 2}px;border-radius:2px;display:inline-block"></i>${s.label}</button>`).join("");
    lg.onclick = (e) => { const b = e.target.closest("button"); if (!b) return; const k = b.dataset.k; S.series[k] = !S.series[k]; b.classList.toggle("off", !S.series[k]);
      const u = S.charts.find(c => c._keys.includes(k) && c.root.closest("#chHero")); if (u) u.setSeries(u._keys.indexOf(k) + 1, { show: S.series[k] }); };
    VIEWS.hardware.wire();
    chart($("#chPow"), [{ k: "w", label: "Power", c: "#d4d7dd", w: 1.75, fill: ["rgba(255,255,255,.10)", "rgba(255,255,255,0)"] }], (u, v) => v.map(x => x + "W"), v => fmt(v) + " W",
      (u, lo, hi) => lo == null ? [0, 200] : [Math.max(0, Math.floor(lo * .9 / 10) * 10), Math.ceil(hi * 1.08 / 10) * 10]);
    VIEWS.controls.wire();
    VIEWS.raw.wire();
    $("#secRaw").addEventListener("toggle", () => VIEWS.raw.update());
  },
  update: (first) => {
    if (!$("#heroV")) return;
    [VIEWS.overview, VIEWS.performance, VIEWS.hardware, VIEWS.pools, VIEWS.controls].forEach(v => v.update(first));
    if ($("#secRaw").open) VIEWS.raw.update();
  },
};

/* ---------------- raw ---------------- */
VIEWS.raw = {
  build: () => `<div class="search">${ic("search", "sm")}<input class="input" id="rawQ" placeholder="Filter fields" autocomplete="off" autocapitalize="off" spellcheck="false"></div>
    <section class="panel" style="overflow:hidden"><div class="raw-table" id="rawT"></div></section>`,
  wire: () => { $("#rawQ").oninput = () => VIEWS.raw.update(); },
  update: () => {
    const mm = D().mm_fields || {}, q = ($("#rawQ").value || "").toLowerCase();
    const ks = Object.keys(mm).sort((a, b) => a.localeCompare(b)).filter(k => !q || k.toLowerCase().includes(q) || String(mm[k]).toLowerCase().includes(q));
    $("#rawT").innerHTML = ks.map(k => `<div><span>${esc(k)}</span><b>${esc(mm[k])}</b></div>`).join("") || '<div><span>No matches</span></div>';
  },
};

/* ---------------- alerts ---------------- */
const KIND_ICON = { block: "party-popper", offline: "wifi-off", hashrate: "activity", temp: "thermometer", pool: "network", link: "link", test: "bell", nearmiss: "crosshair", badge: "award", summary: "calendar-clock", auto: "sliders-horizontal" };
async function loadAlerts() {
  try {
    const r = await api("/api/alerts");
    S.alerts = r.items || []; S.kinds = r.kinds || []; S.desktop = r.desktop; S.desktopOK = r.desktop_available;
    const seen = +LS.get("hw_alert_seen", 0);
    S.alertSeen = Math.max(seen, r.latest || 0); LS.set("hw_alert_seen", S.alertSeen);
    if (!LS.get("hw_alert_read")) LS.set("hw_alert_read", r.latest || 0);
    badge(); renderCelebrate(); if (S.route === "alerts") VIEWS.alerts.update();
  } catch (e) {}
}
function newAlerts(items, latest) {
  if (!S.alertSeen) return; // first load sets the baseline (loadAlerts)
  const fresh = items.filter(a => a.id > S.alertSeen && !S.alerts.some(x => x.id === a.id));
  if (!fresh.length) return;
  fresh.forEach(a => S.alerts.unshift(a));
  S.alertSeen = Math.max(S.alertSeen, latest || 0, ...fresh.map(a => a.id)); LS.set("hw_alert_seen", S.alertSeen);
  const last = fresh[fresh.length - 1];
  if (last.kind !== "block") toast(last.title, last.level, last.body);
  badge(); renderCelebrate(); if (S.route === "alerts") VIEWS.alerts.update();
}
function badge() {
  const read = +LS.get("hw_alert_read", 0), n = S.alerts.filter(a => a.id > read && a.kind !== "test").length;
  $$("[data-badge]").forEach(b => { b.textContent = b.classList.contains("dot-badge") ? "" : (n > 9 ? "9+" : n || ""); b.classList.toggle("on", n > 0); });
}
function renderCelebrate() {
  const el = $("#celebrate"); if (!el) return;
  const ack = +LS.get("hw_block_ack", 0), b = S.route === "alerts" ? null : S.alerts.find(a => a.kind === "block" && a.id > ack);
  if (!b) { el.innerHTML = ""; return; }
  if (b.t > Date.now() / 1000 - 6 * 3600 && LS.get("hw_block_big") !== String(b.id)) { LS.set("hw_block_big", b.id); bigCelebrate(b); }
  if (el.dataset.id === String(b.id) && el.innerHTML) return;
  el.dataset.id = b.id;
  el.innerHTML = `<section class="celebrate" role="status"><canvas class="coins" aria-hidden="true"></canvas>
    <div class="cel-ic">${ic("party-popper")}</div>
    <div class="cel-txt"><div class="cel-k">Block found · <span data-rel="${b.t}">${rel(b.t)}</span></div><h2>${esc(b.miner_name || "Your miner")} found a block!</h2><p>${esc(b.body)}</p></div>
    <div class="cel-acts"><a class="btn ghost" href="#/alerts">Details</a><button class="btn accent" id="celOk">Dismiss</button></div></section>`;
  $("#celOk").onclick = () => { LS.set("hw_block_ack", b.id); el.firstChild.classList.add("out"); setTimeout(renderCelebrate, 260); };
  if (!reducedMotion()) coinSheet(() => { const sec = el.firstChild, cv = sec && $(".coins", sec), icn = sec && $(".cel-ic", sec); if (!cv || !icn) return;
    coinShower(cv, null, Object.assign({}, COIN_BANNER, { origin: () => { const a = sec.getBoundingClientRect(), r = icn.getBoundingClientRect(); return [r.left - a.left + r.width / 2, r.top - a.top + r.height / 2]; } })); });
}
function dayLabel(t) {
  const d = new Date(t * 1000), now = new Date(), y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === now.toDateString()) return "Today"; if (d.toDateString() === y.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, { weekday: "long", month: "short", day: "numeric" });
}
VIEWS.alerts = {
  build: () => `<section class="panel panel-pad"><div class="sec-h"><h2>Alert history <span class="t3" id="alCount"></span></h2>
      <div style="display:flex;gap:8px"><a class="btn ghost sm" href="#/settings?alerts">${ic("settings", "sm")}Alert settings</a><button class="btn ghost sm" id="alClear">Clear</button></div></div>
      <div id="alList"><div class="sk" style="height:64px"></div></div></section>`,
  wire: () => {
    $("#alClear").onclick = async () => { if (!await confirmDialog("Clear alert history?", "This removes every alert from the list. Alert settings stay the same.", "Clear")) return;
      try { await api("/api/alerts/clear", { method: "POST", body: {} }); S.alerts = []; VIEWS.alerts.update(); badge(); } catch (e) { toast(e.message, "bad"); } };
    if (S.state.locked) $("#alClear").disabled = true;
  },
  update: () => {
    const el = $("#alList"); if (!el) return;
    const top = S.alerts.length ? S.alerts[0].id : 0; LS.set("hw_alert_read", Math.max(+LS.get("hw_alert_read", 0), top)); badge();
    $("#alCount").textContent = S.alerts.length ? S.alerts.length : "";
    if (!S.alerts.length) { el.innerHTML = `<div class="empty">${ic("bell")}<b>No alerts yet</b><span>Block finds, offline miners, hashrate drops, heat and pool problems show up here, and as a notification on this computer.</span></div>`; return; }
    let last = "";
    el.innerHTML = S.alerts.map(a => { const [, c] = TONE[a.level] || TONE.info, dl = dayLabel(a.t), h = dl !== last ? `<div class="al-day">${esc(dl)}</div>` : ""; last = dl;
      return h + `<div class="al-row ${a.kind === "block" ? "is-block" : ""}"><div class="al-ic t-${c}">${ic(KIND_ICON[a.kind] || "bell", "sm")}</div>
        <div class="al-txt"><b>${esc(a.title)}</b><span>${esc(a.body)}</span>${a.url ? `<span class="mono al-url">${esc(a.url)}</span>` : ""}</div>
        <div class="al-t"><span data-rel="${a.t}">${rel(a.t)}</span><small>${esc(new Date(a.t * 1000).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" }))}</small></div></div>`; }).join("");
  },
};

/* ---------------- remote link card (overview) ---------------- */
const qrSrc = (u) => "/api/remote/qr.svg?u=" + encodeURIComponent(u);
async function copyLink(u, btn) {
  try { await navigator.clipboard.writeText(u); } catch (e) { const t = document.createElement("textarea"); t.value = u; document.body.append(t); t.select(); document.execCommand("copy"); t.remove(); }
  toast("Link copied"); if (btn) { const h = btn.innerHTML; btn.innerHTML = ic("check", "sm") + "Copied"; setTimeout(() => btn.innerHTML = h, 1600); }
}
function linkCard() {
  const el = $("#linkCard"); if (!el) return;
  const r = S.remote || {};
  if (!r.enabled || !r.url || r.state !== "online" || !S.state.local_viewer) { el.innerHTML = ""; el.dataset.u = ""; return; }
  const seen = LS.get("hw_seen_link"), changed = !!seen && seen !== r.url;
  if (!seen) LS.set("hw_seen_link", r.url);
  const key = r.url + changed; if (el.dataset.u === key) return; el.dataset.u = key;
  el.innerHTML = `<section class="panel link-card ${changed ? "is-new" : ""}">
    <a class="lc-qr" href="#/settings?remote" aria-label="Show a larger QR code"><img src="${qrSrc(r.url)}" alt="QR code for your remote link" width="104" height="104"></a>
    <div class="lc-txt"><div class="lc-k">${ic("globe", "sm")}Remote link ${changed ? '<span class="pill accent">New link · re-scan</span>' : '<span class="pill good"><span class="dot"></span>On</span>'}</div>
      <div class="lc-url mono">${esc(r.url.replace(/^https:\/\//, ""))}</div>
      <p>${changed ? "Your link changed since you last saw it. Scan this code again on your phone and replace the old Home Screen icon." : "Scan with your phone's camera, then in Safari tap Share → Add to Home Screen."}</p></div>
    <div class="lc-acts"><button class="btn ghost" id="lcCopy">${ic("copy", "sm")}Copy</button>${changed ? '<button class="btn accent" id="lcOk">Got it</button>' : ""}</div></section>`;
  $("#lcCopy").onclick = (e) => copyLink(r.url, e.currentTarget);
  const ok = $("#lcOk"); if (ok) ok.onclick = () => { LS.set("hw_seen_link", r.url); el.dataset.u = ""; linkCard(); };
}

/* ---------------- settings ---------------- */
const sw = (id, on, dis, label) => `<button class="switch" role="switch" id="${id}" aria-checked="${on ? "true" : "false"}"${dis ? " disabled" : ""} aria-label="${esc(label || "")}"><i></i></button>`;
const setH = (icn, title, desc, right) => `<div class="set-h"><div class="set-ic">${ic(icn)}</div><div class="set-t"><h2>${title}</h2>${desc ? `<p>${desc}</p>` : ""}</div>${right || ""}</div>`;
VIEWS.settings = {
  build: () => {
    const st = S.state, locked = st.locked;
    if (isView()) return `<div class="banner">${ic("eye", "sm")}<div><b>View-only access.</b> You can watch everything, but the owner's settings, controls and remote access can't be changed from here.</div></div>
      <div class="cols set-cols"><div class="span-7 set-col"><section class="panel panel-pad" id="funSet">${skList(200)}</section></div>
      <div class="span-5 set-col"><section class="panel panel-pad">${setH("info", "About", TAG ? esc(TAG) : "")}<p class="t2" style="margin:0;font-size:13.5px;line-height:1.55">${esc(APP)} ${esc(st.version || "")} · view-only.</p>
        <button class="btn ghost block" id="so" style="margin-top:16px">${ic("log-out", "sm")}Sign out</button></section></div></div>`;
    return `${locked ? `<div class="banner">${ic("lock", "sm")}<div><b>Read-only preview.</b> Settings, remote access and alert switches are locked here. In your own copy of ${esc(APP)} they all work.</div></div>` : ""}
    <div class="cols set-cols">
      <div class="span-7 set-col">
        <section class="panel panel-pad" id="remoteSet"><div class="sk" style="height:120px"></div></section>
        <section class="panel panel-pad" id="secSet"><div class="sk" style="height:220px"></div></section>
        <section class="panel panel-pad" id="sessSet"><div class="sk" style="height:120px"></div></section>
        <section class="panel panel-pad" id="autoFeat"><div class="sk" style="height:220px"></div></section>
        <section class="panel panel-pad" id="alertsSet"><div class="sk" style="height:220px"></div></section>
      </div>
      <div class="span-5 set-col">
        <section class="panel panel-pad">${setH("server", "Miners", "")}
          <div class="list">${(st.miners || []).map(m => `<div class="row-kv"><span style="flex-direction:column;align-items:flex-start;gap:0"><b style="color:var(--t1);font-weight:550">${esc(m.name)}</b><span class="mono t3">${m.kind === "remote" ? "preview source" : esc(m.host + ":" + (m.port || 4028))}</span></span>${locked ? "" : `<button class="btn danger sm" data-del="${esc(m.id)}">Remove</button>`}</div>`).join("")}</div>
          ${locked ? "" : `<details class="disclose"><summary>${ic("plus", "sm")}Add a miner</summary><form id="a-form" autocomplete="off" style="padding-top:12px">${minerForm("a")}</form></details>`}</section>
        <section class="panel panel-pad" id="funSet"><div class="sk" style="height:200px"></div></section>
        <section class="panel panel-pad" id="autoSet"><div class="sk" style="height:56px"></div></section>
        <section class="panel panel-pad">${setH("lock", "Password", locked ? "Managed by the preview host." : st.auth_required ? "Required to open " + esc(APP) + ", on your network and through remote access." : "Optional on your home network. Required for remote access.")}
          ${locked ? "" : `<form id="pwf"><input type="text" autocomplete="username" value="${esc(APP)}" class="sr" tabindex="-1" aria-hidden="true">
            ${st.auth_required ? `<label class="f" style="margin-top:0" for="pwc">Current password</label><input class="input" id="pwc" type="password" autocomplete="current-password">` : ""}
            <label class="f" for="pwn"${st.auth_required ? "" : ' style="margin-top:0"'}>${st.auth_required ? "New password · leave empty to remove" : "New password"}</label><input class="input" id="pwn" type="password" autocomplete="new-password" minlength="6">
            <div class="err" id="pwe"></div><button class="btn ghost block" style="margin-top:12px">${st.auth_required ? "Update password" : "Set password"}</button></form>`}</section>
        ${(st.lan_urls || []).length ? `<section class="panel panel-pad">${setH("smartphone", "On your Wi-Fi", "Open this on a phone on the same network:")}${st.lan_urls.map(u => `<div class="mono" style="font-size:14px;padding:3px 0">${esc(u)}</div>`).join("")}<p class="fine" style="margin-top:10px">Then Share → Add to Home Screen.</p></section>` : ""}
        <section class="panel panel-pad">${setH("info", "About", TAG ? esc(TAG) : "")}<p class="t2" style="margin:0;font-size:13.5px;line-height:1.55">${esc(APP)} ${esc(st.version || "")} reads your miner over its local API (port 4028). It changes miner settings only when you confirm a change in Controls on the dashboard. Running ${esc(APP)} again opens this page instead of starting a second copy.</p>
          ${st.auth_required ? `<button class="btn ghost block" id="so" style="margin-top:16px">${ic("log-out", "sm")}Sign out</button>` : ""}
          <button class="btn ghost block" id="dbg" style="margin-top:16px">${ic("copy", "sm")}Copy debug info</button>
          ${st.can_quit && st.local_viewer ? `<button class="btn danger block" id="quit" style="margin-top:8px">${ic("power", "sm")}Quit ${esc(APP)}</button><p class="fine" style="margin-top:8px">Stops ${esc(APP)} on this computer${S.remote && S.remote.enabled ? ", including remote access" : ""}. Open the app again to restart it.</p>` : ""}</section>
      </div></div>`;
  },
  wire: () => {
    const st = S.state, locked = st.locked;
    if (isView()) { $("#so").onclick = async () => { await api("/api/logout", { method: "POST", body: {} }); boot(); }; loadFeatures(); return; }
    $$("[data-del]").forEach(b => b.onclick = async () => { if (!await confirmDialog("Remove this miner?", "Its chart history is deleted too.", "Remove")) return;
      try { await api("/api/miners/" + encodeURIComponent(b.dataset.del), { method: "DELETE", headers: { "Content-Type": "application/json" } }); boot(); } catch (e) { toast(e.message, "bad"); } });
    if (!locked) {
      wireMinerForm("a", async (r) => { LS.set("hw_miner", r.id); boot(); });
      $("#pwf").onsubmit = async (e) => { e.preventDefault();
        try { await api("/api/password", { method: "POST", body: { current: ($("#pwc") || {}).value || "", new: $("#pwn").value } }); toast($("#pwn").value ? "Password saved" : "Password removed"); boot(); }
        catch (er) { $("#pwe").textContent = er.message; } };
    }
    const so = $("#so"); if (so) so.onclick = async () => { await api("/api/logout", { method: "POST", body: {} }); boot(); };
    const db = $("#dbg"); if (db) db.onclick = async () => {
      try { const t = await (await fetch("/api/debug", { cache: "no-store" })).text(); await copyText(t); toast("Debug info copied. Paste it into a message to support."); }
      catch (e) { toast("Couldn't copy debug info: " + e.message, "bad"); } };
    const qb = $("#quit"); if (qb) qb.onclick = async () => {
      if (!await confirmDialog("Quit " + APP + "?", `Monitoring, alerts and remote access stop until you open ${esc(APP)} again.`, "Quit")) return;
      qb.disabled = true;
      try { await api("/api/quit", { method: "POST", body: {} }); } catch (e) { toast(e.message, "bad"); qb.disabled = false; return; }
      clearInterval(S.timer);
      root.innerHTML = `<div class="center"><div class="auth view"><img class="auth-logo" src="/assets/icons/icon-192.png" alt="">${TAG ? `<div class="auth-tag">${esc(APP)} · ${esc(TAG)}</div>` : ""}
        <h1>${esc(APP)} has stopped</h1><p class="lead">You can close this tab. To start it again, open ${esc(APP)} from Applications (or run it again).</p></div></div>`;
    };
    S.remForm = false; remoteLoad(); alertsSetUI(); secLoad(); sessLoad(); S.rules = null; autoSetUI(); funSetUI(); loadFeatures();
    const q = location.hash.split("?")[1]; if (q) setTimeout(() => { const t = $({ alerts: "#alertsSet", remote: "#remoteSet", auto: "#autoFeat", security: "#secSet" }[q] || null); if (t) t.scrollIntoView({ block: "start", behavior: "smooth" }); }, 120);
  },
};
async function remoteLoad() {
  clearTimeout(S.remT);
  try { S.rem = await api("/api/remote"); } catch (e) { return; }
  if (!$("#remoteSet")) return;
  remoteUI(); autoUI();
  const busy = S.rem.status && ["downloading", "starting", "restarting"].includes(S.rem.status.state);
  S.remT = setTimeout(remoteLoad, busy ? 800 : 5000);
}
function remoteUI() {
  const el = $("#remoteSet"); if (!el) return;
  const R = S.rem, st = R.status || { state: "off" }, on = R.enabled, can = R.available && R.local_viewer;
  const pill = !R.available ? '<span class="pill">Off in preview</span>' : !on ? "" : st.state === "online" ? '<span class="pill good"><span class="dot"></span>Online</span>'
    : st.state === "error" ? '<span class="pill bad">Problem</span>' : '<span class="pill warn"><span class="spin"></span>Connecting</span>';
  let body = "";
  if (!R.available) body = `<div class="notice-s">${ic("info", "sm")}<span>${esc(R.note || "Remote access isn't available here.")}</span></div>`;
  else if (!R.local_viewer) body = `<div class="notice-s">${ic("lock", "sm")}<span>For safety, remote access can only be switched from your home network.</span></div>`;
  if (R.available && S.remForm && !on) body += `<form class="rp-form" id="rpf"><div class="rp-k">${ic("lock", "sm")}Create a password first</div><p>Anyone who opens your link will need it. Use at least 6 characters.</p>
      <input type="text" autocomplete="username" value="${esc(APP)}" class="sr" tabindex="-1" aria-hidden="true">
      <div class="rp-in"><input class="input" id="rp1" type="password" autocomplete="new-password" placeholder="New password" minlength="6" required><input class="input" id="rp2" type="password" autocomplete="new-password" placeholder="Repeat password" minlength="6" required></div>
      <div class="err" id="rpe"></div><div class="rp-acts"><button type="button" class="btn ghost" id="rpx">Cancel</button><button class="btn accent" id="rpgo">Create password & turn on</button></div></form>`;
  else if (on && st.state === "downloading") { const p = Math.round((st.progress || 0) * 100);
    body += `<div class="dl"><div class="dl-k"><span>${esc(st.detail || "Downloading cloudflared…")}</span><b class="num">${p}%</b></div><div class="dl-bar"><i style="width:${p}%"></i></div><p class="fine">One-time download of Cloudflare's official cloudflared (about 20–40 MB), checked against Cloudflare's published SHA-256.</p></div>`; }
  else if (on && st.state === "online" && st.url) body += `<div class="rl">
      <div class="rl-qr"><img src="${qrSrc(st.url)}" alt="QR code for your remote link" width="168" height="168"></div>
      <div class="rl-main"><div class="label">Your link</div><div class="rl-url"><span class="mono">${esc(st.url)}</span></div>
        <div class="rl-acts"><button class="btn ghost" id="rlCopy">${ic("copy", "sm")}Copy link</button><a class="btn ghost" href="${esc(st.url)}" target="_blank" rel="noopener">${ic("external-link", "sm")}Open</a></div>
        <ol class="steps"><li><span>Scan the code with your phone's camera.</span></li><li><span>Sign in with your ${esc(APP)} password.</span></li><li><span>In Safari tap Share → <b>Add to Home Screen</b>.</span></li></ol></div></div>
      <div class="rl-foot">${ic("circle-check", "sm")}<span>Connected through Cloudflare${st.cloudflared ? " · cloudflared " + esc(st.cloudflared) : ""} · since <span data-rel="${st.since}">${rel(st.since)}</span>${st.restarts ? ` · reconnected ${st.restarts}×` : ""}</span></div>
      ${R.named ? "" : `<div class="notice-s warn">${ic("triangle-alert", "sm")}<span><b>This free link changes</b> when ${esc(APP)} or the computer restarts. You'll get a “New link” alert and the card on the dashboard will ask you to re-scan. Turn on <b>Start automatically</b> below so restarts are rare, or set up a permanent link.</span></div>`}`;
  else if (on && st.state === "error") body += `<div class="notice-s bad">${ic("circle-alert", "sm")}<span><b>Couldn't start remote access.</b> ${esc(st.error || "")}</span></div><button class="btn ghost" id="rlRetry" style="margin-top:12px">${ic("refresh-cw", "sm")}Try again</button>`;
  else if (on) body += `<div class="conn"><span class="spin"></span><span>${esc(st.detail || "Connecting to Cloudflare…")}</span></div>${st.error && st.state === "restarting" ? `<p class="fine">${esc(st.error)}</p>` : ""}`;
  else if (R.available) body += `<ul class="facts"><li>${ic("lock", "sm")}<span>Password-protected. Settings like this one can only be changed from home.</span></li>
      <li>${ic("globe", "sm")}<span>Uses a free Cloudflare quick tunnel. No account, no router changes.</span></li>
      <li>${ic("refresh-cw", "sm")}<span>The free link changes when ${esc(APP)} restarts. ${esc(APP)} shows the new one here and alerts you.</span></li></ul>`;
  const adv = R.available ? `<details class="disclose" ${R.named ? "open" : ""}><summary>${ic("link", "sm")}Permanent link <span class="t3">· optional, needs a free Cloudflare account</span></summary>
      <div style="padding-top:12px"><p class="t2" style="margin:0 0 4px;font-size:13px;line-height:1.55">In Cloudflare Zero Trust → Networks → Tunnels, create a tunnel, add a public hostname that points to <span class="mono">http://127.0.0.1:${location.port || 80}</span>, then paste the tunnel token and that hostname here. The link then never changes.</p>
      <label class="f" for="tkT">Tunnel token</label><input class="input mono" id="tkT" type="password" autocomplete="off" placeholder="${R.named ? "•••••••• (saved)" : "eyJhIjoi…"}"${can ? "" : " disabled"}>
      <label class="f" for="tkH">Hostname</label><input class="input mono" id="tkH" placeholder="miner.example.com" value="${esc(R.hostname || "")}"${can ? "" : " disabled"} autocapitalize="off" spellcheck="false">
      <div class="err" id="tkE"></div><div style="display:flex;gap:8px;margin-top:12px"><button class="btn ghost" id="tkS"${can ? "" : " disabled"}>Save</button>${R.named ? `<button class="btn danger" id="tkC"${can ? "" : " disabled"}>Use free link instead</button>` : ""}</div></div></details>` : "";
  el.innerHTML = setH("globe", `Remote access ${pill}`, `Open ${esc(APP)} from anywhere, like on mobile data, through a private https link. Off by default.`,
    sw("remSw", on || S.remForm, !can, "Remote access")) + body + adv;
  const s = $("#remSw"); if (s) s.onclick = remoteToggle;
  const c = $("#rlCopy"); if (c) c.onclick = (e) => copyLink(st.url, e.currentTarget);
  const rt = $("#rlRetry"); if (rt) rt.onclick = async () => { await remoteSet({ enabled: false }); await remoteSet({ enabled: true }); };
  const f = $("#rpf"); if (f) { $("#rp1").focus(); $("#rpx").onclick = () => { S.remForm = false; remoteUI(); };
    f.onsubmit = async (e) => { e.preventDefault(); const a = $("#rp1").value, b = $("#rp2").value;
      if (a.length < 6) { $("#rpe").textContent = "Use at least 6 characters."; return; } if (a !== b) { $("#rpe").textContent = "The passwords don't match."; return; }
      $("#rpgo").disabled = true; try { await remoteSet({ enabled: true, password: a }); S.remForm = false; toast("Password created · remote access starting"); S.state = await api("/api/state"); route(); } catch (er) { $("#rpe").textContent = er.message; $("#rpgo").disabled = false; } }; }
  const ts = $("#tkS"); if (ts) ts.onclick = async () => { const t = $("#tkT").value.trim(), h = $("#tkH").value.trim();
    if (!t && !R.named) { $("#tkE").textContent = "Paste the tunnel token."; return; }
    try { await remoteSet({ enabled: R.enabled, token: t || undefined, hostname: h }); toast("Permanent link saved"); } catch (er) { $("#tkE").textContent = er.message; } };
  const tc = $("#tkC"); if (tc) tc.onclick = async () => { try { await remoteSet({ enabled: R.enabled, token: "", hostname: "" }); toast("Using the free link"); } catch (er) { $("#tkE").textContent = er.message; } };
}
async function remoteSet(body) {
  const r = await api("/api/remote", { method: "POST", body });
  await remoteLoad(); return r;
}
async function remoteToggle() {
  const R = S.rem;
  if (R.enabled) {
    if (!await confirmDialog("Turn off remote access?", "Your remote link stops working right away. Next time you turn it on you'll get a new link.", "Turn off")) return;
    try { await remoteSet({ enabled: false }); toast("Remote access is off"); } catch (e) { toast(e.message, "bad"); }
    return;
  }
  if (S.remForm) { S.remForm = false; remoteUI(); return; }
  if (!R.password_set) { S.remForm = true; remoteUI(); return; }
  if (!await confirmDialog("Turn on remote access?", `${esc(APP)} will open a private Cloudflare link to this computer. Anyone with the link still needs your password. The first time, ${esc(APP)} downloads Cloudflare's official cloudflared.`, "Turn on")) return;
  try { await remoteSet({ enabled: true }); } catch (e) { toast(e.message, "bad"); }
}
function autoUI() {
  const el = $("#autoSet"); if (!el) return;
  const a = S.rem.autostart, locked = S.state.locked;
  const can = a && a.supported && !locked && S.rem.local_viewer;
  el.innerHTML = setH("power", "Start automatically", `Start ${esc(APP)} quietly ${(S.state.shell || "").includes("native") ? "in the menu bar" : "in the background"} when this computer starts, with no window. Fewer restarts also means your remote link changes less often.`,
    sw("autoSw", a && a.enabled, !can, "Start automatically")) +
    `<div class="set-meta">${locked ? `${ic("lock", "sm")}<span>Not available in the preview.</span>` : !a ? `${ic("info", "sm")}<span>Not available here.</span>` :
      `${ic(a.enabled ? "circle-check" : "info", "sm")}<span>${a.enabled ? `On · ${esc(a.method)}` : `Uses a ${esc(a.method)}`}${a.note ? ` · ${esc(a.note)}` : ""}</span>`}</div>
     <div class="set-meta">${ic("external-link", "sm")}<span>${(S.state.shell || "").includes("native") ? `To open it later, click the ${esc(APP)} icon in the menu bar or open ${esc(APP)} again.` : `To open it later, just run ${esc(APP)} again. It opens your browser to the copy that's already running.`}</span></div>`;
  const s = $("#autoSw"); if (s) s.onclick = async () => { const want = !(a && a.enabled); s.disabled = true;
    try { const r = await api("/api/autostart", { method: "POST", body: { enabled: want } }); S.rem.autostart = r.autostart; toast(want ? `${APP} will start automatically` : "Auto-start is off"); }
    catch (e) { toast(e.message, "bad"); } autoUI(); };
}
function alertsSetUI() {
  const el = $("#alertsSet"); if (!el) return;
  const locked = S.state.locked;
  el.innerHTML = setH("bell", "Alerts", `${esc(APP)} watches your miners and tells you here and with a notification on this computer. No extra app needed.`) +
    `<div class="toggles">${S.kinds.map(k => `<div class="tg-row"><div class="tg-ic">${ic(KIND_ICON[k.kind] || "bell", "sm")}</div><div class="tg-t"><b>${esc(k.label)}</b><span>${esc(k.desc)}</span></div>${sw("ak-" + k.kind, k.on, locked, k.label)}</div>`).join("")}
      <div class="tg-row tg-sep"><div class="tg-ic">${ic("monitor", "sm")}</div><div class="tg-t"><b>Desktop notifications</b><span>${S.desktopOK ? "Native notifications on the computer running " + esc(APP) + "." : "Off in the preview. The preview runs on a server with no screen."}</span></div>${sw("akDesk", S.desktop, locked || !S.desktopOK, "Desktop notifications")}</div></div>
    <div class="set-foot"><button class="btn ghost" id="akTest"${locked ? " disabled" : ""}>${ic("bell-ring", "sm")}Send a test alert</button><a class="t2 link" href="#/alerts">View history${ic("chevron-right", "sm")}</a></div>`;
  $$(".tg-row .switch", el).forEach(b => b.onclick = async () => {
    const on = b.getAttribute("aria-checked") !== "true"; b.setAttribute("aria-checked", on); b.disabled = true;
    const body = b.id === "akDesk" ? { desktop: on } : { kinds: { [b.id.slice(3)]: on } };
    try { const r = await api("/api/alerts/settings", { method: "POST", body }); S.kinds = r.kinds; S.desktop = r.desktop; } catch (e) { toast(e.message, "bad"); }
    alertsSetUI(); });
  $("#akTest").onclick = async () => { try { await api("/api/alerts/test", { method: "POST", body: {} }); refresh(false); } catch (e) { toast(e.message, "bad"); } };
}

/* ---------------- lottery: odds, tickets, best shares, blocks feed, badges ---------------- */
const SFX = ["", "K", "M", "G", "T", "P", "E", "Z", "Y"];
function big(v, d = 1) { if (!isNum(v)) return "—"; v = +v; let i = 0; while (Math.abs(v) >= 1000 && i < SFX.length - 1) { v /= 1000; i++; } return (i ? v.toFixed(v < 10 ? d : 0) : fmt(v)) + SFX[i]; }
const WORDS = ["", " thousand", " million", " billion", " trillion", " quadrillion", " quintillion", " sextillion", " septillion"];
function words(v) { if (!isNum(v)) return "—"; v = +v; let i = 0; while (v >= 1000 && i < WORDS.length - 1) { v /= 1000; i++; } return (v < 10 ? v.toFixed(1) : Math.round(v)) + WORDS[i]; }
function oneIn(x) { return isNum(x) && x > 0 ? "1 in " + words(x) : "—"; }
function pct(p) { if (!isNum(p) || p <= 0) return "0%"; p *= 100; if (p >= 1) return p.toFixed(1) + "%"; if (p < 1e-8) return "<0.00000001%"; return p.toFixed(Math.min(8, 1 - Math.floor(Math.log10(p)))) + "%"; }
function yrs(y) { if (!isNum(y) || y <= 0) return "—"; if (y < 1 / 12) return Math.round(y * 365) + " days"; if (y < 2) return (y * 12).toFixed(0) + " months"; return words(y) + " years"; }
const usd = (v) => isNum(v) && v > 0 ? "$" + Math.round(v).toLocaleString() : "—";
const btc = (sat) => isNum(sat) ? (sat / 1e8).toFixed(3) + " BTC" : "—";
const isView = () => !!(S.state && S.state.view_only);
// Undecided features: one server switch each in internal/app/optional.go; their UI is tagged OPTIONAL:<name>.
const opt = (k) => !(S.state && S.state.optional && S.state.optional[k] === false);

async function loadLotto() {
  if (!S.miner || !$("#secLotto")) return;
  try { S.lot = await api("/api/lottery?miner=" + encodeURIComponent(S.miner.id)); LOTTO.update(); } catch (e) {}
}
async function loadFeatures() {
  clearTimeout(S.featT);
  if (!S.miner) return;
  try { S.feat = await api("/api/features?miner=" + encodeURIComponent(S.miner.id)); } catch (e) { S.featT = setTimeout(loadFeatures, 30000); return; }
  autoCard(); if ($("#autoFeat")) autoSetUI(); if ($("#funSet")) funSetUI();
  S.featT = setTimeout(loadFeatures, 30000);
}
const LOTTO = {
  build: () => `
    <section class="panel panel-pad lotto span-12" id="secLotto">
      <div class="sec-h"><h2>${ic("ticket", "sm")}Lottery</h2><span class="t3" id="lotNet"></span></div>
      <div class="lot-grid" id="lotGrid">${skList(150)}</div>
      <div class="lot-close" id="lotClose"></div>
    </section>
    <section class="panel panel-pad span-5" id="secBest"><div class="sec-h"><h2>${ic("medal", "sm")}Best shares</h2><span class="t3" id="bestN"></span></div><div id="bestList">${skList(180)}</div></section>
    <section class="panel panel-pad span-7" id="secBlocks"><div class="sec-h"><h2>${ic("blocks", "sm")}Network blocks</h2><span class="t3" id="blkTip"></span></div><div id="blkList">${skList(180)}</div></section>
    <section class="panel panel-pad span-12" id="secBadges"><div class="sec-h"><h2>${ic("award", "sm")}${opt("badges") ? "Badges" : "Summary"}</h2><span id="streak"></span></div>${opt("badges") ? `<div class="badges" id="badgeList">${skList(90)}</div>` : ""}<div id="sumList"></div></section>`,
  update: () => {
    const L = S.lot; if (!L || !$("#lotGrid")) return;
    const o = L.odds || {}, st = L.stats || {}, net = L.net || {}, rw = L.reward;
    $("#lotNet").innerHTML = !net.network_on ? `${ic("eye-off", "sm")}Network data off` : L.difficulty ? `difficulty <b class="num">${big(L.difficulty, 1)}</b>${net.network_stale ? ' · <span style="color:var(--warn)">stale</span>' : ""}` : "waiting for network data…";
    const has = o.difficulty > 0 && o.hashrate_hs > 0;
    const T = (icn, k, v, sub, cls) => `<div class="lot-t ${cls || ""}"><div class="label">${ic(icn, "sm")}${k}</div><div class="lot-v num">${v}</div><div class="sub">${sub}</div></div>`;
    $("#lotGrid").innerHTML =
      T("dice-5", "Chance today", has ? oneIn(o.one_in_per_day) : "—", has ? `${pct(o.per_day)} per day · ${pct(o.per_year)} per year` : "needs hashrate and network difficulty", "hl") +
      T("hourglass", "Time to block", has ? yrs(o.years_per_block) : "—", has ? `on average, at ${fmt(o.hashrate_hs / 1e12, 2)} TH/s. Every hash is a fresh draw.` : "") +
      T("ticket", "Tickets today", words(st.tickets_today), `<b>${words(st.tickets_all)}</b> since ${st.first_seen ? esc(new Date(st.first_seen * 1000).toLocaleDateString(undefined, { month: "short", day: "numeric" })) : "—"} · 1 hash = 1 ticket`) +
      T("bitcoin", "Block reward", rw ? (rw.usd ? usd(rw.usd) : btc(rw.total_sat)) : "—", rw ? `${btc(rw.subsidy_sat)} + ${btc(rw.fees_sat)} fees${rw.usd ? " · " + usd(rw.price_usd) + "/BTC" : ""}` : (net.network_on ? "waiting for network data…" : "network data off in Settings"));
    const best = o.best_share || 0, cl = o.best_share_closeness || 0;
    const w = cl > 0 ? Math.max(1.5, Math.min(100, (Math.log10(cl) + 9) / 9 * 100)) : 0;
    $("#lotClose").innerHTML = best ? `<div class="lc-row"><span>${ic("crosshair", "sm")}Your best share <b class="num">${big(best, 2)}</b> is <b class="num">${pct(cl)}</b> of a winning ticket</span><span class="t3">${L.difficulty ? "needs " + big(L.difficulty, 1) : ""}</span></div>
      <div class="lc-bar" title="log scale"><i style="width:${w}%"></i>${[1e-6, 1e-3].map(m => `<em style="left:${(Math.log10(m) + 9) / 9 * 100}%"><small>${m === 1e-6 ? "0.0001%" : "0.1%"}</small></em>`).join("")}<em style="left:100%"><small>block</small></em></div>` : "";
    // leaderboard
    const lb = st.leaderboard || [];
    $("#bestN").textContent = lb.length ? "top " + lb.length : "";
    $("#bestList").innerHTML = lb.length ? `<ol class="lb">${lb.slice(0, 8).map((s, i) => `<li class="${i === 0 ? "top" : ""}"><span class="lb-r">${i + 1}</span><span class="lb-d num">${big(s.diff, 2)}</span><span class="lb-p">${s.net_diff ? pct(s.diff / s.net_diff) : ""}</span><span class="lb-t t3" title="${esc(absTime(s.t))}">${esc(rel(s.t))}</span></li>`).join("")}</ol>`
      : `<div class="empty sm">${ic("medal")}<b>No best shares yet</b><span>Each time your miner beats its own best share, it lands here.</span></div>`;
    // network blocks
    const bl = L.blocks || [];
    $("#blkTip").innerHTML = L.tip_height ? `tip <b class="num">${fmt(L.tip_height)}</b>` : "";
    $("#blkList").innerHTML = !net.network_on ? `<div class="empty sm">${ic("eye-off")}<b>Network data is off</b><span>Turn it on in Settings › Privacy to see who found each new block.</span></div>`
      : bl.length ? `<div class="blk">${bl.slice(0, 7).map(b => `<div class="blk-r${b.solo ? " solo" : ""}"><span class="blk-h num">${fmt(b.height)}</span><span class="blk-p">${b.solo ? ic("sparkles", "sm") : ""}${esc(b.pool || "Unknown")}${b.solo ? '<span class="pill accent">solo miner</span>' : ""}</span><span class="blk-x t3 num">${fmt(b.tx_count)} tx</span><span class="blk-t t3" data-rel="${b.time}">${rel(b.time)}</span></div>`).join("")}</div>
        <p class="fine">Highlighted: blocks found by solo miners like you. It happens a few times a year.</p>` : skList(180);
    // summary, then badges + streak (OPTIONAL:badges)
    const sm = (st.summaries || [])[0];
    $("#sumList").innerHTML = sm ? `<div class="sum">${ic("calendar-clock", "sm t3")}<span><b>${sm.kind === "weekly" ? "Last week" : "Yesterday"}</b> · ${fmt(sm.avg_ths, 2)} TH/s avg · ${fmt(sm.uptime_pct, 1)}% online · best share ${big(sm.best_share, 2)} · ${big(sm.hashes)} tickets${sm.blocks ? ` · <b style="color:var(--accent)">${sm.blocks} block!</b>` : ""}</span></div>` : "";
    if (!opt("badges")) return;
    const bd = st.badges || [];
    $("#streak").innerHTML = st.streak_days ? `<span class="pill accent">${ic("flame", "sm")}${st.streak_days}-day streak</span>` : "";
    $("#badgeList").innerHTML = bd.map(b => `<div class="bdg${b.earned ? " got" : ""}" title="${esc(b.desc)}"><div class="bdg-ic">${ic(b.icon || "award")}</div><div class="bdg-t"><b>${esc(b.title)}</b><span>${b.earned ? "Earned " + esc(new Date(b.earned * 1000).toLocaleDateString(undefined, { month: "short", day: "numeric" })) : esc(b.hint || b.desc)}</span>${b.earned ? "" : `<div class="bdg-bar"><i style="width:${Math.round((b.progress || 0) * 100)}%"></i></div>`}</div></div>`).join("");
  },
};

/* ---------------- automations card (dashboard) ---------------- */
const AUTO_ROWS = [["schedule", "calendar-clock", "Work speed schedule"], ["failover", "network", "Backup pool failover"], ["light", "lightbulb", "Light effects"]];
function autoOn(o, k) { if (!o) return false; if (k === "light") return !!(o.light && ((opt("status_lamp") && o.light.lamp) || o.light.celebrate || (opt("best_flash") && o.light.best_flash))); return !!(o[k] && o[k].on); }
function autoCard() {
  const el = $("#autoCard"); if (!el) return;
  const F = S.feat; if (!F) { el.innerHTML = skList(120); return; }
  const o = F.opts || {}, a = F.auto || {}, n = AUTO_ROWS.filter(([k]) => autoOn(o, k)).length;
  const desc = (k) => {
    if (!autoOn(o, k)) return "Off";
    if (k === "schedule") return F.schedule_now ? (F.schedule_now.mode ? `Now ${cap(F.schedule_now.mode)}` : "No rule right now") : "On";
    if (k === "failover") return "On";
    return [opt("status_lamp") && o.light.lamp && "status lamp", o.light.celebrate && "block flash", opt("best_flash") && o.light.best_flash && "best-share flash"].filter(Boolean).join(" · ");
  };
  const acts = (a.actions || []).slice(0, 3);
  el.innerHTML = `<div class="sec-h"><h2>Automations</h2>${isView() ? "" : `<a class="btn ghost sm" href="#/settings?auto">${ic("sliders-horizontal", "sm")}Set up</a>`}</div>
    <div class="list">${AUTO_ROWS.map(([k, icn, t]) => `<div class="row-kv"><span>${ic(icn, "sm t3")}${t}</span><b class="${autoOn(o, k) ? "" : "t3"}">${desc(k)}</b></div>`).join("")}</div>
    ${n ? "" : `<p class="fine">All off. ${esc(APP)} never changes your miner on its own until you turn one on.</p>`}
    ${acts.length ? `<div class="acts-log">${acts.map(x => `<div>${ic(x.ok ? "circle-check" : "circle-alert", "sm")}<span>${esc(x.text)}</span><span class="t3" data-rel="${x.t}">${rel(x.t)}</span></div>`).join("")}</div>` : ""}`;
}

/* ---------------- block celebration: falling gold Bitcoins ----------------
   Sprites are the 3D gold glyph from the Camaro wallpaper video (same three.js geometry, material, lights and studio HDR),
   pre-rendered at 24 tumble angles (one end-over-end flip) into a 6x4 sheet of 128px cells. Drawn on a canvas with rAF. */
const COIN = { url: "/assets/icons/btc-coin-sprites.webp", cell: 128, cols: 6, n: 24, img: null, soft: null, glint: null, ready: false, wait: [] };
function coinSheet(cb) {
  if (COIN.ready) return cb();
  COIN.wait.push(cb); if (COIN.img) return;
  const im = new Image(); COIN.img = im; im.decoding = "async";
  im.onload = () => {
    // soft copy for the near, out-of-focus coins: downscale then upscale (a cheap blur that works in every browser)
    const w = im.naturalWidth, h = im.naturalHeight, k = 0.3, a = document.createElement("canvas"), b = document.createElement("canvas");
    a.width = Math.round(w * k); a.height = Math.round(h * k); a.getContext("2d").drawImage(im, 0, 0, a.width, a.height);
    b.width = w; b.height = h; const bx = b.getContext("2d"); bx.imageSmoothingQuality = "high"; bx.drawImage(a, 0, 0, w, h); COIN.soft = b;
    const g = document.createElement("canvas"); g.width = g.height = 64; const gx = g.getContext("2d"), rg = gx.createRadialGradient(32, 32, 0, 32, 32, 32);
    rg.addColorStop(0, "rgba(255,255,240,1)"); rg.addColorStop(.18, "rgba(255,236,170,.7)"); rg.addColorStop(1, "rgba(255,200,90,0)");
    gx.fillStyle = rg; gx.fillRect(0, 0, 64, 64); gx.fillStyle = "rgba(255,255,245,.9)"; gx.fillRect(30.5, 2, 3, 60); gx.fillRect(2, 30.5, 60, 3); COIN.glint = g;
    COIN.ready = true; COIN.wait.splice(0).forEach(f => f());
  };
  im.onerror = () => { COIN.wait.length = 0; COIN.img = null; };
  im.src = COIN.url;
}
// host: positioned element to fill. layers: [back canvas, front canvas or null]. origin(): burst point in host px.
function coinShower(back, front, o) {
  const host = back.parentElement, cx = [back, front].filter(Boolean).map(c => c.getContext("2d"));
  const P = [], R = (a, b) => a + Math.random() * (b - a);
  let W = 0, H = 0, dpr = 1, t = 0, last = 0, spawnAcc = 0, raf = 0, dead = false;
  const size = () => { dpr = Math.min(2, window.devicePixelRatio || 1); W = host.clientWidth; H = host.clientHeight;
    [back, front].forEach(c => { if (!c) return; c.width = Math.round(W * dpr); c.height = Math.round(H * dpr); }); };
  size(); const onR = () => size(); window.addEventListener("resize", onR);
  const sc = () => Math.max(.55, Math.min(1.25, H / 900));               // physics scale so phones and big screens feel the same
  const coin = (x, y, vx, vy, burst) => {
    const z = Math.random() ** 1.15, near = !burst && front && Math.random() < o.near;   // most coins far/mid, a few big blurred ones up front
    const zz = near ? R(.9, 1) : z, sz = near ? R(o.max * .95, o.max * 1.25) : o.min + (o.max - o.min) * zz;
    P.push({ x, y, vx, vy, sz, near, burst, age: 0,
      vt: (o.vt * (.62 + .55 * zz)) * sc(),                                // terminal fall speed: small/far coins drift, near ones drop faster (parallax)
      fl: Math.random() * o.frames, fs: (burst ? R(2.2, 3.6) : R(.7, 1.9)) * (Math.random() < .5 ? -1 : 1),   // end-over-end flips per second
      rot: R(-.6, .6), vr: R(-1.4, 1.4), sw: R(.6, 1.6), swA: R(18, 46) * (.5 + zz), ph: R(0, 6.28),
      a: near ? .62 : .72 + .28 * zz });
  };
  const burst = () => { const [ox, oy] = o.origin(), s = sc();
    for (let i = 0; i < o.burst; i++) { const ang = -Math.PI / 2 + R(-1.05, 1.05), sp = R(.55, 1) * o.burstV * s;
      coin(ox + R(-30, 30), oy + R(-14, 14), Math.cos(ang) * sp * 1.15, Math.sin(ang) * sp, true); } };
  const tick = (now) => {
    if (dead) return;
    if (!back.isConnected) return stop();
    const dt = last ? Math.min(.033, (now - last) / 1000) : 1 / 60; last = now; t += dt;
    const g = o.g * sc(), fade = t < o.rain ? 1 : Math.max(0, 1 - (t - o.rain) / o.fadeOut);
    if (t < o.rain && P.length < o.cap) { spawnAcc += dt * o.rate * (t < .5 ? .4 : 1);
      while (spawnAcc >= 1 && P.length < o.cap) { spawnAcc--; coin(R(-20, W + 20), -o.max * 1.4, R(-40, 40), R(40, 140) * sc(), false); } }
    cx.forEach(c => { c.setTransform(1, 0, 0, 1, 0, 0); c.clearRect(0, 0, c.canvas.width, c.canvas.height); });
    for (let i = P.length - 1; i >= 0; i--) {
      const p = P[i]; p.age += dt;
      // drag ramps in: burst coins fly free like they were fired, then open up and flutter down like confetti
      const settle = p.burst ? Math.min(1, (p.age / 1.1) ** 2) : 1, k = .35 + (g / p.vt - .35) * settle;
      p.vy += (g - k * p.vy) * dt; p.vx += (-k * p.vx) * dt;
      const flut = Math.sin(p.age * p.sw * 2.4 + p.ph) * p.swA * settle;
      p.x += (p.vx + flut * p.sw) * dt; p.y += p.vy * dt;
      p.fl += p.fs * o.frames * dt * (p.burst ? 1 - .45 * settle : 1); p.rot += p.vr * dt * (p.burst ? 2.2 - 1.2 * settle : 1);
      if (p.y > H + p.sz * 1.5 || (p.age > 1 && (p.x < -200 || p.x > W + 200))) { P.splice(i, 1); continue; }
      const c = cx[p.near && cx[1] ? 1 : 0], f = ((Math.floor(p.fl) % o.frames) + o.frames) % o.frames;
      const sx = (f % COIN.cols) * COIN.cell, sy = Math.floor(f / COIN.cols) * COIN.cell;
      c.setTransform(dpr, 0, 0, dpr, 0, 0); c.translate(p.x, p.y); c.rotate(p.rot);
      c.globalAlpha = p.a * fade * Math.min(1, p.age * 6);
      c.drawImage(p.near ? COIN.soft : COIN.img, sx, sy, COIN.cell, COIN.cell, -p.sz / 2, -p.sz / 2, p.sz, p.sz);
      // glint: a quick star flash as the face swings toward the light (frames 0 and 12 are face-on)
      if (!p.near && p.sz > o.min + 8) { const fr = ((p.fl % 12) + 12) % 12, gl = Math.max(0, 1 - Math.min(fr, 12 - fr) / 1.1);
        if (gl > 0) { c.globalCompositeOperation = "lighter"; c.globalAlpha = gl * .75 * fade; const gs = p.sz * .9;
          c.rotate(-p.rot + .4); c.drawImage(COIN.glint, -p.sz * .22 - gs / 2, -p.sz * .26 - gs / 2, gs, gs); c.globalCompositeOperation = "source-over"; } }
    }
    if (t > o.rain + o.fadeOut || (t > o.rain && !P.length)) return stop();
    raf = requestAnimationFrame(tick);
  };
  const stop = () => { dead = true; cancelAnimationFrame(raf); window.removeEventListener("resize", onR);
    cx.forEach(c => { c.setTransform(1, 0, 0, 1, 0, 0); c.clearRect(0, 0, c.canvas.width, c.canvas.height); }); o.done && o.done(); };
  burst(); raf = requestAnimationFrame(tick);
  return { stop };
}
const COIN_BIG = { frames: 24, burst: 72, burstV: 1550, cap: 170, rate: 24, rain: 7, fadeOut: 1.2, min: 20, max: 56, near: .09, vt: 330, g: 1900 };
const COIN_BANNER = { frames: 24, burst: 22, burstV: 620, cap: 46, rate: 7, rain: 4.5, fadeOut: 1, min: 14, max: 30, near: 0, vt: 120, g: 900 };
const reducedMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

/* ---------------- block celebration: confetti + sound + flash ---------------- */
function chime() {
  if (S.feat && S.feat.fun && S.feat.fun.sound_off) return;
  try { const A = window.AudioContext || window.webkitAudioContext; const c = new A(); const t0 = c.currentTime;
    [523.25, 659.25, 783.99, 1046.5].forEach((f, i) => { const o = c.createOscillator(), g = c.createGain(); o.type = "triangle"; o.frequency.value = f;
      g.gain.setValueAtTime(0, t0 + i * .12); g.gain.linearRampToValueAtTime(.18, t0 + i * .12 + .02); g.gain.exponentialRampToValueAtTime(.001, t0 + i * .12 + .9);
      o.connect(g).connect(c.destination); o.start(t0 + i * .12); o.stop(t0 + i * .12 + 1); });
  } catch (e) {}
}
function bigCelebrate(b) {
  if ($("#bigCel")) return;
  const d = document.createElement("div"); d.id = "bigCel"; d.className = "big-cel"; d.setAttribute("role", "dialog"); d.setAttribute("aria-label", "Block found");
  const rw = S.lot && S.lot.reward;
  const still = reducedMotion();
  d.innerHTML = `<div class="bc-flash"></div><div class="bc-glow" aria-hidden="true"></div><canvas class="coins" aria-hidden="true"></canvas>
    <div class="bc-card">${still ? `<img class="bc-coin" src="/assets/icons/btc-coin.webp" alt="" width="96" height="96">` : ""}<div class="bc-ic">${ic("trophy")}</div><div class="cel-k">Block found · <span data-rel="${b.t}">${rel(b.t)}</span></div>
      <h1>${esc(b.miner_name || "Your miner")} found a block!</h1>
      <p>${rw ? `About <b>${rw.usd ? usd(rw.usd) : btc(rw.total_sat)}</b> (${btc(rw.total_sat)}) is heading to your payout address.` : esc(b.body)}</p>
      <p class="fine">Odds were ${S.lot && S.lot.odds && S.lot.odds.one_in_per_block ? oneIn(S.lot.odds.one_in_per_block) + " per block" : "astronomical"}. Check your pool or wallet; the reward unlocks after 100 confirmations.</p>
      <div class="acts"><button class="btn accent lg" id="bcOk">Woohoo!</button></div></div>
    <canvas class="coins front" aria-hidden="true"></canvas>`;
  document.body.append(d); requestAnimationFrame(() => d.classList.add("on"));
  chime();
  let show = null;
  if (!still) coinSheet(() => { if (!d.isConnected) return; const card = $(".bc-card", d);
    show = coinShower($(".coins", d), $(".coins.front", d), Object.assign({}, COIN_BIG, { origin: () => { const r = card.getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height * .32]; } })); });
  $("#bcOk").onclick = () => { LS.set("hw_block_ack", b.id); d.classList.remove("on"); setTimeout(() => { if (show) show.stop(); d.remove(); renderCelebrate(); }, 300); };
}

/* ---------------- settings: security ---------------- */
async function secLoad() { try { S.sec = await api("/api/security"); } catch (e) { return; } secUI(); }
function sessLoad() { sessUI(); }
function secUI() {
  const el = $("#secSet"); if (!el || !S.sec) return;
  const X = S.sec, locked = S.state.locked, need = !X.password_set;
  el.innerHTML = setH("shield-check", "Security", `Sign-in protection for ${esc(APP)}, at home and through remote access.`) +
    `<div class="toggles">
      <div class="tg-row"><div class="tg-ic">${ic("timer", "sm")}</div><div class="tg-t"><b>Lockout after wrong passwords</b><span>Always on. ${X.lockout.free_tries} tries, then a growing wait (up to ${X.lockout.max_lock_min} min). Remote sign-ins are also capped at ${X.lockout.remote_max} per ${X.lockout.remote_window_min} min in total.</span></div><span class="pill good">${ic("check", "sm")}On</span></div>
      <div class="tg-row"><div class="tg-ic">${ic("eye", "sm")}</div><div class="tg-t"><b>View-only password</b><span>${X.view_password_set ? "On · people who sign in with it can see everything but change nothing." : "A second password for family or friends: they can watch, but can't change the miner, settings or remote access."}</span></div><button class="btn ghost sm" id="vpBtn"${locked || need ? " disabled" : ""}>${X.view_password_set ? "Change" : "Set"}</button></div>
      <div class="tg-row"><div class="tg-ic">${ic("share", "sm")}</div><div class="tg-t"><b>View-only link</b><span>${X.view_link_on ? (X.view_link_url ? `<span class="mono vl">${esc(X.view_link_url)}</span>` : "On · works through your remote link once remote access is on.") : "Share a link that opens a view-only dashboard with no password. Turn it off or make a new one anytime."}</span></div>
        <div class="tg-acts">${X.view_link_on ? `${X.view_link_url ? `<button class="btn ghost sm" id="vlCopy">${ic("copy", "sm")}Copy</button>` : ""}<button class="btn ghost sm" id="vlNew">New link</button><button class="btn danger sm" id="vlOff">Off</button>` : `<button class="btn ghost sm" id="vlOn"${locked || need ? " disabled" : ""}>Create</button>`}</div></div>
      <div class="tg-row"><div class="tg-ic">${ic("info", "sm")}</div><div class="tg-t"><b>App signing</b><span>${esc(X.signing_note)}</span></div></div>
    </div>`;
  const on = (id, f) => { const b = $("#" + id); if (b) b.onclick = f; };
  on("vpBtn", async () => { const pw = await promptDialog("View-only password", "Anyone who signs in with this can see the dashboard but can't change anything. Leave empty to remove it.", "View-only password", "password", "Save", true); if (pw == null) return;
    try { S.sec = await api("/api/security/view", { method: "POST", body: { password: pw } }); toast(pw ? "View-only password saved" : "View-only password removed"); secUI(); } catch (e) { toast(e.message, "bad"); } });
  const vl = async (action, msg) => { try { S.sec = await api("/api/security/viewlink", { method: "POST", body: { action } }); toast(msg); secUI(); } catch (e) { toast(e.message, "bad"); } };
  on("vlOn", () => vl("create", "View-only link created"));
  on("vlNew", async () => { if (await confirmDialog("Make a new view-only link?", "The old link stops working and anyone watching through it is signed out.", "New link")) vl("rotate", "New view-only link · the old one no longer works"); });
  on("vlOff", async () => { if (await confirmDialog("Turn off the view-only link?", "Anyone watching through it is signed out.", "Turn off")) vl("off", "View-only link is off"); });
  on("vlCopy", (e) => copyLink(X.view_link_url, e.currentTarget));
}
function sessUI() {
  const el = $("#sessSet"); if (!el) return;
  const locked = S.state.locked, remoteOn = S.rem && S.rem.enabled && !(S.rem && S.rem.named);
  el.innerHTML = setH("log-out", "Sign out everywhere", "Lost a phone, or shared your password or link by mistake?") +
    `<div class="set-foot sec-acts"><button class="btn ghost" id="soAll"${locked || !S.state.auth_required ? " disabled" : ""}>${ic("log-out", "sm")}Sign out everywhere else</button>
      <button class="btn ghost" id="newLink"${locked || !remoteOn ? " disabled" : ""} title="${remoteOn ? "" : "Needs remote access with the free link"}">${ic("refresh-cw", "sm")}New remote link</button></div>
    <p class="fine">Sign out everywhere else: every other phone, tablet and browser has to sign in again; this one stays. New remote link: the old link stops working at once and everyone using it is signed out.${S.state.auth_required ? "" : " Set a password first: without one there are no sign-ins to end."}</p>`;
  $("#soAll").onclick = async () => { if (!await confirmDialog("Sign out everywhere else?", "Every other phone, tablet and browser has to sign in again. This device stays signed in.", "Sign out others")) return;
    try { const r = await api("/api/sessions/revoke", { method: "POST", body: {} }); toast(`Signed out ${r.signed_out} other session${r.signed_out === 1 ? "" : "s"}`); } catch (e) { toast(e.message, "bad"); } };
  $("#newLink").onclick = async () => { if (!await confirmDialog("Get a new remote link?", "The current link stops working right away and everyone using it is signed out. Scan the new QR code on your phone afterwards.", "New link")) return;
    try { const r = await api("/api/remote/rotate", { method: "POST", body: {} }); toast("New link on its way", "good", `${r.signed_out} remote session${r.signed_out === 1 ? "" : "s"} signed out`); remoteLoad(); } catch (e) { toast(e.message, "bad"); } };
}
function promptDialog(title, html, label, type, okLabel, allowEmpty) {
  return new Promise((resolve) => {
    const sc = document.createElement("div"); sc.className = "scrim";
    const d = document.createElement("div"); d.className = "dialog"; d.setAttribute("role", "dialog"); d.setAttribute("aria-modal", "true");
    d.innerHTML = `<h2>${esc(title)}</h2><p>${html}</p><form id="pdF"><input type="text" autocomplete="username" value="${esc(APP)}" class="sr" tabindex="-1" aria-hidden="true"><label class="f" for="pdI" style="margin-top:0">${esc(label)}</label><input class="input" id="pdI" type="${type || "text"}" autocomplete="${type === "password" ? "new-password" : "off"}">
      <div class="acts"><button type="button" class="btn ghost" data-a="0">Cancel</button><button class="btn accent">${esc(okLabel)}</button></div></form>`;
    document.body.append(sc, d); requestAnimationFrame(() => { sc.classList.add("on"); d.classList.add("on"); $("#pdI", d).focus(); });
    const done = (v) => { sc.classList.remove("on"); d.classList.remove("on"); setTimeout(() => { sc.remove(); d.remove(); }, 220); document.removeEventListener("keydown", key); resolve(v); };
    const key = (e) => { if (e.key === "Escape") done(null); };
    document.addEventListener("keydown", key); sc.onclick = () => done(null); $('[data-a="0"]', d).onclick = () => done(null);
    $("#pdF", d).onsubmit = (e) => { e.preventDefault(); const v = $("#pdI", d).value; if (!v && !allowEmpty) return; done(v); };
  });
}

/* ---------------- settings: automations ---------------- */
const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
function autoSetUI() {
  const el = $("#autoFeat"); if (!el) return; const F = S.feat; if (!F) { el.innerHTML = skList(200); return; }
  const o = F.opts || {}, can = F.can_control && !S.state.locked, dis = can ? "" : " disabled";
  const sc = o.schedule || {}, fo = o.failover || {}, li = o.light || {};
  const rule = (r, i) => `<div class="rule" data-i="${i}"><div class="days">${DAYS.map((d, k) => `<button type="button" class="${(r.days || []).includes(k) ? "on" : ""}" data-d="${k}"${dis}>${d[0]}</button>`).join("")}</div>
      <input class="input mono" type="time" value="${esc(r.start)}" data-k="start"${dis}><span class="t3">to</span><input class="input mono" type="time" value="${esc(r.end)}" data-k="end"${dis}>
      <div class="seg sm" data-k="mode">${["low", "medium", "high"].map(m => `<button type="button" class="${r.mode === m ? "on" : ""}" data-m="${m}"${dis}>${cap(m)}</button>`).join("")}</div>
      <button type="button" class="icon-btn rm" aria-label="Remove rule"${dis}>${ic("trash-2", "sm")}</button></div>`;
  S.rules = S.rules || (sc.rules || []).map(r => ({ ...r, days: [...(r.days || [])] }));
  el.innerHTML = setH("sliders-horizontal", "Automations", `Let ${esc(APP)} look after the miner while you're away. <b>All off by default</b>: installing ${esc(APP)} never changes your miner until you turn one on here.`) +
    (can ? "" : `<div class="notice-s">${ic("lock", "sm")}<span>${S.state.locked ? "Locked in the preview." : isView() ? "View-only access." : "Only from your own copy on your home network."}</span></div>`) +
    `<div class="toggles">
    <div class="tg-row"><div class="tg-ic">${ic("calendar-clock", "sm")}</div><div class="tg-t"><b>Work speed schedule</b><span>${sc.on && F.schedule_now ? `On · ${F.schedule_now.mode ? "now " + cap(F.schedule_now.mode) : "no rule matches right now"}` : "Run quieter at night or while you're home, faster when you're out."}</span></div>${sw("auSched", sc.on, !can, "Schedule")}</div>
    <div class="au-body${sc.on ? " open" : ""}" id="schedBody"><div id="rules">${S.rules.map(rule).join("")}</div>
      <div class="au-row"><button type="button" class="btn ghost sm" id="addRule"${dis}>${ic("plus", "sm")}Add rule</button><span class="grow"></span><span class="t3">Outside rules</span>
        <select class="input sm" id="schedDef"${dis}><option value="">Leave as is</option>${["low", "medium", "high"].map(m => `<option value="${m}"${sc.default === m ? " selected" : ""}>${cap(m)}</option>`).join("")}</select></div>
      <div class="au-row"><span class="fine" style="margin:0">Times are this computer's local time. An end before the start runs past midnight.</span><span class="grow"></span><button type="button" class="btn accent sm" id="saveSched"${dis}>Save schedule</button></div></div>
    <div class="tg-row tg-sep"><div class="tg-ic">${ic("network", "sm")}</div><div class="tg-t"><b>Backup pool failover</b><span>${fo.on ? `On · switch after ${fo.dead_min || 5} min down, back after ${fo.return_min || 15} min, ${fo.cool_min || 5} min cooldown` : "If the pool you're on stays dead or rejecting, switch to the next pool already set on the miner. It never changes addresses or passwords, and switches back when the main pool recovers."}</span></div>${sw("auFail", fo.on, !can, "Backup pool failover")}</div>
    <div class="au-body${fo.on ? " open" : ""}" id="failBody">
      <div class="au-row"><span class="t3">Wait before switching</span><input class="input sm mono" type="number" min="1" max="120" id="foDead" value="${fo.dead_min || 5}"${dis}><span class="t3">min</span></div>
      <div class="au-row"><span class="t3">Wait before switching back</span><input class="input sm mono" type="number" min="1" max="1440" id="foReturn" value="${fo.return_min || 15}"${dis}><span class="t3">min</span></div>
      <div class="au-row"><span class="t3">Cooldown between switches</span><input class="input sm mono" type="number" min="1" max="120" id="foCool" value="${fo.cool_min || 5}"${dis}><span class="t3">min</span></div>
      <p class="fine" style="margin:10px 0 0">Only among pools already configured on the miner. This sends cgminer's <span class="mono">switchpool</span> command, which hasn't been tried on a real Nano 3 yet — some firmware may ignore it. Every switch is logged and shown as an alert.</p>
      <div class="au-row"><span class="grow"></span><button type="button" class="btn accent sm" id="saveFail"${dis}>Save failover</button></div></div>
    <div class="tg-row tg-sep"><div class="tg-ic">${ic("party-popper", "sm")}</div><div class="tg-t"><b>Flash gold on a block</b><span>The light flashes gold for an hour when you find a block, then your own colour comes back.</span></div>${sw("liCel", li.celebrate, !can, "Block flash")}</div>
    ${opt("status_lamp") ? /* OPTIONAL:lamp */ `<div class="tg-row"><div class="tg-ic">${ic("lightbulb", "sm")}</div><div class="tg-t"><b>Light as a status lamp</b><span>Steady green while hashing, red when something's wrong (not hashing, no pool, too hot).</span></div>${sw("liLamp", li.lamp, !can, "Status lamp")}</div>` : ""}
    ${opt("best_flash") ? /* OPTIONAL:bestflash */ `<div class="tg-row"><div class="tg-ic">${ic("sparkles", "sm")}</div><div class="tg-t"><b>Flash on a new best share</b><span>A short purple flash when the miner beats its own best share. Your own light comes back after.</span></div>${sw("liBest", li.best_flash, !can, "Best-share flash")}</div>` : ""}
    </div>`;
  $$(".rule .seg", el).forEach(seg);
  const put = async (body, msg) => { try { S.feat = await api("/api/features", { method: "POST", body: { miner: S.miner.id, ...body } }); toast(msg); S.rules = null; autoSetUI(); autoCard(); } catch (e) { toast(e.message, "bad"); autoSetUI(); } };
  const confirmOn = (what, extra) => confirmDialog(`Turn on ${what}?`, `${esc(APP)} will change your miner on its own when this triggers. ${extra} Every change is logged and shown as an alert.`, "Turn on");
  const sched = (on) => ({ on, default: $("#schedDef").value, rules: S.rules });
  const tog = (id, f) => { const b = $("#" + id); if (b) b.onclick = f; };
  tog("auSched", async () => { if (!sc.on && !S.rules.length) { S.rules.push({ days: [0, 1, 2, 3, 4, 5, 6], start: "22:00", end: "07:00", mode: "low" }); } if (!sc.on && !await confirmOn("the work speed schedule", "It sets the work speed at the times you choose.")) return; put({ schedule: sched(!sc.on) }, sc.on ? "Schedule off" : "Schedule on"); });
  const fail = (on) => ({ on, dead_min: +$("#foDead").value || 5, cool_min: +$("#foCool").value || 5, return_min: +$("#foReturn").value || 15 });
  tog("auFail", async () => { if (!fo.on && !await confirmOn("backup pool failover", "If a pool stays dead or rejecting, it switches to the next pool already set on the miner, then back when the main pool recovers. It never changes pool addresses or passwords.")) return; put({ failover: fail(!fo.on) }, fo.on ? "Failover off" : "Failover on"); });
  tog("saveFail", () => put({ failover: fail(true) }, "Failover saved"));
  [["liLamp", "lamp"], ["liCel", "celebrate"], ["liBest", "best_flash"]].forEach(([id, k]) => tog(id, async () => { const want = !li[k];
    if (want && !await confirmDialog("Let " + APP + " control the light?", "It changes the miner's light for this effect, then puts your own colour back.", "Turn on")) return;
    put({ light: { [k === "best_flash" ? "BestFlash" : cap(k)]: want } }, want ? "Light effect on" : "Light effect off"); }));
  // schedule editor
  const rulesEl = $("#rules");
  rulesEl.onclick = (e) => { const r = e.target.closest(".rule"); if (!r) return; const i = +r.dataset.i, R = S.rules[i];
    const d = e.target.closest("[data-d]"); if (d) { const k = +d.dataset.d; R.days = R.days.includes(k) ? R.days.filter(x => x !== k) : [...R.days, k].sort(); d.classList.toggle("on"); }
    const m = e.target.closest("[data-m]"); if (m) { R.mode = m.dataset.m; $$("[data-m]", r).forEach(x => x.classList.toggle("on", x === m)); seg(m.parentElement); }
    if (e.target.closest(".rm")) { S.rules.splice(i, 1); autoSetUI(); } };
  rulesEl.oninput = (e) => { const r = e.target.closest(".rule"); if (r && e.target.dataset.k) S.rules[+r.dataset.i][e.target.dataset.k] = e.target.value; };
  tog("addRule", () => { S.rules.push({ days: [1, 2, 3, 4, 5], start: "09:00", end: "17:00", mode: "high" }); autoSetUI(); });
  tog("saveSched", () => put({ schedule: sched(sc.on) }, "Schedule saved"));
}

/* ---------------- settings: lottery extras + privacy ---------------- */
function funSetUI() {
  const el = $("#funSet"); if (!el || !S.feat) return;
  const f = S.feat.fun || {}, p = S.feat.privacy || {}, locked = S.state.locked || isView();
  el.innerHTML = setH("sparkles", "Lottery extras", "The fun part of solo mining.") +
    `<div class="toggles">
      <div class="tg-row"><div class="tg-ic">${ic(f.sound_off ? "volume-x" : "volume-2", "sm")}</div><div class="tg-t"><b>Block celebration sound</b><span>Confetti always; a short fanfare too, if this is on.</span></div>${sw("fnSound", !f.sound_off, locked, "Sound")}</div>
      <div class="tg-row"><div class="tg-ic">${ic("crosshair", "sm")}</div><div class="tg-t"><b>Near-miss alerts</b><span>Alert on every new personal best${f.near_miss_diff ? `, and any share above ${big(f.near_miss_diff, 1)}` : ""}.</span></div>
        <select class="input sm" id="fnNear"${locked ? " disabled" : ""}>${[[0, "Personal bests"], [1e9, "Above 1G"], [1e10, "Above 10G"], [1e11, "Above 100G"], [1e12, "Above 1T"]].map(([v, l]) => `<option value="${v}"${(f.near_miss_diff || 0) === v ? " selected" : ""}>${l}</option>`).join("")}</select></div>
      <div class="tg-row wrap"><div class="tg-ic">${ic("calendar-clock", "sm")}</div><div class="tg-t"><b>Summary</b><span>Average hashrate, time online, best share and tickets, as an alert.</span></div>
        <div class="seg sm" id="fnSum">${["daily", "weekly", "off"].map(k => `<button class="${(f.summary || "daily") === k ? "on" : ""}" data-k="${k}"${locked ? " disabled" : ""}>${cap(k)}</button>`).join("")}</div></div>
      ${(S.state.shell || "").includes("native") && opt("menu_sparkline") ? /* OPTIONAL:sparkline */ `<div class="tg-row"><div class="tg-ic">${ic("chart-spline", "sm")}</div><div class="tg-t"><b>Menu bar sparkline</b><span>A tiny hashrate graph next to the menu bar icon.</span></div>${sw("fnSpark", !f.menu_sparkline_off, locked, "Menu bar sparkline")}</div>` : ""}
    </div>
    <div class="set-h" style="margin-top:22px">${""}<div class="set-ic">${ic("eye-off")}</div><div class="set-t"><h2>Privacy</h2><p>Only public network data is fetched, from mempool.space. Nothing about you or your miner is sent.</p></div></div>
    <div class="toggles">
      <div class="tg-row"><div class="tg-ic">${ic("blocks", "sm")}</div><div class="tg-t"><b>Network data</b><span>Difficulty, newest blocks and who found them, every 2 minutes. Needed for live odds.</span></div>${sw("pvNet", !p.network_off, S.state.locked || isView(), "Network data")}</div>
      <div class="tg-row"><div class="tg-ic">${ic("bitcoin", "sm")}</div><div class="tg-t"><b>Bitcoin price</b><span>For the block reward in dollars, every 60 seconds.</span></div>${sw("pvPrice", !p.price_off, S.state.locked || isView(), "Bitcoin price")}</div>
    </div>`;
  seg($("#fnSum"));
  const putFun = async (patch) => { try { S.feat = await api("/api/features", { method: "POST", body: { miner: S.miner.id, fun: { ...f, ...patch } } }); funSetUI(); } catch (e) { toast(e.message, "bad"); } };
  const b = (id, fn) => { const x = $("#" + id); if (x) x.onclick = fn; };
  b("fnSound", () => putFun({ sound_off: !f.sound_off }));
  b("fnSpark", () => putFun({ menu_sparkline_off: !f.menu_sparkline_off }));
  const nr = $("#fnNear"); if (nr) nr.onchange = () => putFun({ near_miss_diff: +nr.value });
  $("#fnSum").onclick = (e) => { const x = e.target.closest("button"); if (x) putFun({ summary: x.dataset.k }); };
  const pv = async (body) => { try { await api("/api/privacy", { method: "POST", body }); await loadFeatures(); loadLotto(); } catch (e) { toast(e.message, "bad"); } };
  b("pvNet", () => pv({ NetworkOff: !p.network_off }));
  b("pvPrice", () => pv({ PriceOff: !p.price_off }));
}

document.addEventListener("visibilitychange", () => { if (!document.hidden && S.miner && $("#view")) refresh(false); });
boot();
})();

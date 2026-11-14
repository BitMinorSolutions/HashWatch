/* Hashwatch frontend — vanilla JS + uPlot. */
(() => {
"use strict";
const APP = document.body.dataset.app || "Hashwatch";
const $ = (s, el = document) => el.querySelector(s);
const root = $("#root");
const S = { state: null, miner: null, data: null, hist: [], range: 3600, lastFetch: 0, timer: null, charts: {}, prev: {} };

/* ---------------- utils ---------------- */
const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const isNum = (v) => v !== null && v !== undefined && v !== "" && Number.isFinite(Number(v));
const fmt = (v, d = 0) => isNum(v) ? Number(v).toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d }) : "—";
function si(v, d = 2) {
  if (!isNum(v)) return "—"; v = Number(v);
  const u = ["", "K", "M", "G", "T", "P", "E"]; let i = 0;
  while (Math.abs(v) >= 1000 && i < u.length - 1) { v /= 1000; i++; }
  return v.toFixed(i ? d : 0) + u[i];
}
function rel(unix) {
  if (!unix) return "—";
  const s = Math.max(0, Math.round(Date.now() / 1000 - unix));
  if (s < 5) return "just now";
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.floor(s / 60) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
}
const absTime = (unix) => unix ? new Date(unix * 1000).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }) : "—";
function toast(msg) { const t = $("#toast"); t.textContent = msg; t.classList.add("on"); clearTimeout(t._h); t._h = setTimeout(() => t.classList.remove("on"), 2600); }
async function api(path, opts = {}) {
  const o = { credentials: "same-origin", cache: "no-store", ...opts, headers: { ...(opts.body ? { "Content-Type": "application/json" } : {}), ...(opts.headers || {}) } };
  if (o.body && typeof o.body !== "string") o.body = JSON.stringify(o.body);
  const r = await fetch(path, o);
  let j = {}; try { j = await r.json(); } catch (e) {}
  if (r.status === 401 && path !== "/api/login" && path !== "/api/password") { boot(); throw new Error("Signed out"); }
  if (!r.ok) throw new Error(j.error || ("HTTP " + r.status));
  return j;
}
function tempColor(t, target) {
  if (!isNum(t)) return "var(--text-3)";
  const tg = isNum(target) ? Number(target) : 90;
  if (t > tg) return "var(--bad)";
  if (t >= tg - 5) return "var(--warn)";
  if (t >= tg - 15) return "var(--accent-2)";
  return "var(--good)";
}
function heat(t, lo, hi) { // green → amber → red
  if (!isNum(t)) return "#6c717d";
  const x = Math.max(0, Math.min(1, (t - lo) / Math.max(1, hi - lo)));
  const stops = [[61, 220, 151], [245, 185, 66], [255, 107, 107]];
  const seg = x < .5 ? 0 : 1, f = x < .5 ? x * 2 : (x - .5) * 2;
  const c = stops[seg].map((v, i) => Math.round(v + (stops[seg + 1][i] - v) * f));
  return `rgb(${c.join(",")})`;
}
function tween(el, to, d, suffix = "") {
  if (!el) return;
  if (!isNum(to)) { el.textContent = "—"; el.dataset.v = ""; return; }
  const from = isNum(el.dataset.v) ? Number(el.dataset.v) : null; to = Number(to); el.dataset.v = to;
  if (from === null || from === to || matchMedia("(prefers-reduced-motion: reduce)").matches) { el.textContent = fmt(to, d) + suffix; return; }
  const t0 = performance.now(), dur = 700;
  const step = (now) => { const p = Math.min(1, (now - t0) / dur), e = 1 - Math.pow(1 - p, 3);
    el.textContent = fmt(from + (to - from) * e, d) + suffix; if (p < 1) requestAnimationFrame(step); };
  requestAnimationFrame(step);
}
const I = {
  hash: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M13 2 4 14h7l-1 8 9-12h-7z"/></svg>',
  temp: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M14 14.8V5a2 2 0 1 0-4 0v9.8a4 4 0 1 0 4 0z"/></svg>',
  power: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M12 3v8M6.3 7.3a8 8 0 1 0 11.4 0"/></svg>',
  eff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 20 20 4M7 4h-3v3M20 17v3h-3"/><circle cx="7" cy="17" r="2.5"/><circle cx="17" cy="7" r="2.5"/></svg>',
  fan: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="2"/><path d="M12 10c0-4 1-7 4-7s3 4-2 7M14 12c4 0 7 1 7 4s-4 3-7-2M12 14c0 4-1 7-4 7s-3-4 2-7M10 12c-4 0-7-1-7-4s4-3 7 2"/></svg>',
  clock: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg>',
  gear: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/></svg>',
  warn: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 3 2 20h20L12 3z"/><path d="M12 10v4M12 17h.01"/></svg>',
  ok: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/></svg>',
  x: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6 6 18"/></svg>',
};
const logo = '<img src="/assets/icons/icon-192.png" alt="" width="30" height="30">';

/* ---------------- boot ---------------- */
async function boot() {
  clearInterval(S.timer);
  try { S.state = await api("/api/state"); } catch (e) { root.innerHTML = `<div class="center"><div class="auth"><h1>Can't reach ${esc(APP)}</h1><p class="lead">${esc(e.message)}</p></div></div>`; return; }
  if (S.state.auth_required && !S.state.authed) return viewLogin();
  if (S.state.setup_needed) return viewSetup();
  const ms = S.state.miners || [];
  const saved = localStorage.getItem("hw_miner");
  S.miner = ms.find(m => m.id === saved) || ms[0];
  viewDash();
}

/* ---------------- login ---------------- */
function viewLogin() {
  root.innerHTML = `<div class="center"><div class="auth fade-in">
    <img class="auth-logo" src="/assets/icons/icon-192.png" alt="">
    <h1>Welcome back</h1><p class="lead">Enter your ${esc(APP)} password to see your miners.</p>
    <form class="card" id="f" autocomplete="on">
      <input type="text" name="username" autocomplete="username" value="${esc(APP)}" class="sr" tabindex="-1" aria-hidden="true">
      <label class="f" for="pw" style="margin-top:0">Password</label>
      <input class="input" id="pw" type="password" autocomplete="current-password" required autofocus>
      <div class="err" id="e"></div>
      <button class="btn block" id="b" style="margin-top:6px">Sign in</button>
    </form></div></div>`;
  $("#f").onsubmit = async (ev) => {
    ev.preventDefault(); const b = $("#b"); b.disabled = true; $("#e").textContent = "";
    try { await api("/api/login", { method: "POST", body: { password: $("#pw").value } }); boot(); }
    catch (e) { $("#e").textContent = e.message; b.disabled = false; }
  };
}

/* ---------------- setup ---------------- */
function minerForm(id) {
  return `<label class="f" for="${id}-ip" style="margin-top:0">Miner IP address</label>
    <input class="input mono" id="${id}-ip" inputmode="decimal" placeholder="192.168.1.50" autocomplete="off" autocapitalize="off" spellcheck="false">
    <label class="f" for="${id}-name">Name <span class="dim">(optional)</span></label>
    <input class="input" id="${id}-name" placeholder="Office Nano 3" autocomplete="off">
    <div class="err" id="${id}-e"></div>
    <button type="submit" class="btn block" id="${id}-go">Connect miner</button>
    <div class="divider">or</div>
    <button type="button" class="btn ghost block" id="${id}-scan">Scan my network</button>
    <div class="found" id="${id}-found"></div>`;
}
function wireMinerForm(id, done) {
  const go = async (host, name) => {
    const b = $("#" + id + "-go"); b.disabled = true; b.textContent = "Connecting…"; $("#" + id + "-e").textContent = "";
    try { const r = await api("/api/miners", { method: "POST", body: { host, name } }); toast("Connected to " + (name || r.desc)); await done(r); }
    catch (e) { $("#" + id + "-e").textContent = e.message; }
    finally { b.disabled = false; b.textContent = "Connect miner"; }
  };
  $("#" + id + "-form").onsubmit = (ev) => { ev.preventDefault(); const ip = $("#" + id + "-ip").value.trim();
    if (!ip) { $("#" + id + "-e").textContent = "Enter the miner's IP address. You can find it in your router or the Avalon app."; return; }
    go(ip, $("#" + id + "-name").value.trim()); };
  $("#" + id + "-scan").onclick = async () => {
    const b = $("#" + id + "-scan"), out = $("#" + id + "-found"); b.disabled = true; b.textContent = "Scanning your network…"; out.innerHTML = "";
    try {
      const r = await api("/api/discover", { method: "POST", body: {} });
      if (!r.found || !r.found.length) out.innerHTML = '<div class="fine" style="margin-top:4px">No miners answered on port 4028. Make sure the miner is on the same network, then enter its IP above.</div>';
      else out.innerHTML = r.found.map(f => `<button type="button" data-h="${esc(f.host)}" data-d="${esc(f.desc)}"><span><b>${esc(f.desc)}</b><br><span class="mono dim">${esc(f.host)}</span></span><span class="pill good plain">Add</span></button>`).join("");
      out.querySelectorAll("button").forEach(x => x.onclick = () => { $("#" + id + "-ip").value = x.dataset.h; go(x.dataset.h, ""); });
    } catch (e) { out.innerHTML = `<div class="err">${esc(e.message)}</div>`; }
    finally { b.disabled = false; b.textContent = "Scan my network"; }
  };
}
function viewSetup() {
  root.innerHTML = `<div class="center"><div class="auth wide fade-in">
    <img class="auth-logo" src="/assets/icons/icon-192.png" alt="">
    <h1>Connect your miner</h1>
    <p class="lead">${esc(APP)} reads your Avalon's stats directly over your local network. Nothing leaves your home.</p>
    <form class="card" id="s-form" autocomplete="off">${minerForm("s")}</form>
    <p class="fine">Tip: the miner's API listens on port 4028. If yours uses another port, enter it like <span class="mono">192.168.1.50:4029</span>.</p>
  </div></div>`;
  wireMinerForm("s", async () => boot());
}

/* ---------------- dashboard ---------------- */
function kpi(cls, icon, label) {
  return `<div class="card kpi ${cls}"><div class="label"><span>${label}</span>${icon}</div>
    <div class="value num"><span class="sk" data-k="v">00.0</span></div>
    <div class="spark" data-k="spark"></div><div class="foot" data-k="foot"><span class="sk">&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;</span></div></div>`;
}
function viewDash() {
  const ms = S.state.miners || [];
  const sel = ms.length > 1
    ? `<div class="miner-select"><select id="msel" aria-label="Miner">${ms.map(m => `<option value="${esc(m.id)}"${m.id === S.miner.id ? " selected" : ""}>${esc(m.name)}</option>`).join("")}</select></div>`
    : `<div class="miner-name">${esc(S.miner.name)}</div>`;
  root.innerHTML = `
  <header class="topbar"><div class="topbar-in">
    <div class="brand">${logo}<span class="brand-name">${esc(APP)}</span></div><span class="crumb">/</span>${sel}
    <div class="spacer"></div>
    <div class="live" id="live"><span class="dot"></span><span id="liveTxt">Connecting…</span></div>
    <button class="icon-btn" id="openSettings" aria-label="Settings">${I.gear}</button>
  </div></header>
  <main class="page">
    <div class="headline">
      <div><h1 id="title">${esc(S.miner.name)}</h1><div class="sub" id="subline"><span class="sk">Loading live data from your miner</span></div></div>
      <div class="pills" id="pills"></div>
    </div>
    <div class="grid">
      <section class="kpis" id="kpis">
        ${kpi("k-hash", I.hash, "Hashrate")}${kpi("k-temp", I.temp, "Max chip temp")}${kpi("k-power", I.power, "Power")}
        ${kpi("k-eff", I.eff, "Efficiency")}${kpi("k-fan", I.fan, "Fan")}${kpi("k-up", I.clock, "Uptime")}
      </section>

      <section class="card c-hash">
        <div class="card-h"><div class="card-t">Hashrate</div>
          <div class="seg" id="range"><button data-r="3600" class="on">1H</button><button data-r="21600">6H</button><button data-r="86400">24H</button></div></div>
        <div class="hero-row">
          <div><div class="hero-num num"><span id="heroV" class="sk">0.000</span><span class="u">TH/s</span></div>
            <div class="hero-cap"><span>5-minute average</span><span class="delta" id="heroD"></span></div></div>
          <div class="stats-inline num" id="hrStats"></div>
        </div>
        <div class="chart" id="chHash"><div class="chart-empty" id="chHashE"></div><div class="tip" id="tipHash"></div></div>
        <div class="legend" style="margin-top:10px"><span><i style="background:var(--accent)"></i>5m avg</span><span><i style="background:#ffd8a3;opacity:.7"></i>15m avg</span><span><i style="background:rgba(255,255,255,.35)"></i>5s</span></div>
        <div class="subgrid num" id="hrSub"></div>
      </section>

      <section class="card c-health">
        <div class="card-h"><div class="card-t">Health</div><span class="dim" id="hUpd"></span></div>
        <div class="alerts" id="alerts"><div class="sk" style="height:40px"></div></div>
        <div class="health-split">
          <div>
            <div class="eyebrow">Accepted shares</div>
            <div class="big-acc num"><b id="acc" class="sk">000,000</b><span class="dim" id="accPct"></span></div>
            <div class="ratio" id="ratio"></div>
            <div class="legend" style="margin-bottom:12px"><span><i style="background:var(--good)"></i>Accepted</span><span><i style="background:var(--warn)"></i>Stale</span><span><i style="background:var(--bad)"></i>Rejected</span></div>
          </div>
          <div class="kv num" id="shareKv"></div>
        </div>
      </section>

      <section class="card c-temp">
        <div class="card-h"><div class="card-t">Temperatures <span class="hint" id="tHint"></span></div></div>
        <div class="legend" id="tLegend"></div>
        <div class="chart sm" id="chTemp"><div class="chart-empty" id="chTempE"></div><div class="tip" id="tipTemp"></div></div>
      </section>

      <section class="card c-chips">
        <div class="card-h"><div class="card-t">Chips <span class="hint" id="chipHint"></span></div><div class="chip-scale"><span>cool</span><i></i><span>hot</span></div></div>
        <div class="chips num" id="chips">${Array.from({ length: 10 }, () => '<div class="chip sk" style="height:66px"></div>').join("")}</div>
      </section>

      <section class="card c-pools">
        <div class="card-h"><div class="card-t">Pools <span class="hint" id="poolHint"></span></div></div>
        <div class="pools" id="pools"><div class="sk" style="height:60px"></div><div class="sk" style="height:60px"></div></div>
      </section>

      <section class="card c-device">
        <div class="card-h"><div class="card-t">Device</div></div>
        <div class="kv" id="devKv"><div><span class="sk">Model</span><b class="sk">Avalon</b></div></div>
      </section>

      <section class="card c-raw">
        <details class="raw" id="raw"><summary><span class="card-t">All miner fields <span class="hint" id="rawN"></span></span><span class="chev"></span></summary>
          <div class="raw-tools"><input class="input" id="rawQ" placeholder="Filter fields…" autocomplete="off" autocapitalize="off" spellcheck="false"></div>
          <div class="raw-grid" id="rawGrid"></div></details>
      </section>
    </div>
    <div class="footer"><span id="foot1">${esc(APP)}${S.state.version ? " " + esc(S.state.version) : ""} · read-only · data stays on your network</span><span id="foot2"></span></div>
  </main>`;
  if ($("#msel")) $("#msel").onchange = (e) => { localStorage.setItem("hw_miner", e.target.value); boot(); };
  $("#openSettings").onclick = openSettings;
  $("#range").onclick = (e) => { const b = e.target.closest("button"); if (!b) return; S.range = +b.dataset.r;
    $("#range").querySelectorAll("button").forEach(x => x.classList.toggle("on", x === b)); drawCharts(); };
  $("#rawQ").oninput = renderRaw;
  S.data = null; S.hist = []; S.charts = {};
  initCharts();
  refresh(true);
  S.timer = setInterval(tick, 1000);
}
let nextIn = 0;
function tick() {
  nextIn--;
  if (nextIn <= 0) refresh(false);
  liveLabel();
  if (S.data) { const d = S.data; $("#hUpd").textContent = "Updated " + rel(d.fetched_at_unix); renderPoolTimes(); }
}
function liveLabel() {
  const el = $("#live"), t = $("#liveTxt"); if (!el) return;
  const age = S.data && S.data.fetched_at_unix ? Date.now() / 1000 - S.data.fetched_at_unix : null;
  el.classList.toggle("down", !!(S.data && !S.data.ok) || S.err);
  el.classList.toggle("stale", age !== null && age > 60 && !(S.data && !S.data.ok));
  if (S.err) t.textContent = "Can't refresh";
  else if (!S.data) t.textContent = "Connecting…";
  else if (!S.data.ok) t.textContent = "Miner offline";
  else t.innerHTML = '<span class="txt-long">Live · </span>' + esc(rel(S.data.fetched_at_unix));
}
async function refresh(full) {
  nextIn = (S.state && S.state.poll_seconds) || 15;
  const since = full || !S.hist.length ? 0 : S.hist[S.hist.length - 1].t;
  try {
    const r = await api(`/api/live?miner=${encodeURIComponent(S.miner.id)}&since=${since}`);
    S.err = null;
    if (full) S.hist = r.history || []; else S.hist = S.hist.concat(r.history || []);
    const cutoff = Date.now() / 1000 - 86400; while (S.hist.length && S.hist[0].t < cutoff) S.hist.shift();
    if (r.data) { S.data = r.data; render(); }
    else if (full) setTimeout(() => refresh(true), 1500);
    drawCharts();
  } catch (e) { S.err = e.message; }
  liveLabel();
}

/* ---------------- render ---------------- */
function setKpi(cls, value, d, unit, foot, spark, valueColor) {
  const card = $(".kpi." + cls); if (!card) return;
  const v = card.querySelector('[data-k="v"]');
  if (v.classList.contains("sk")) { v.classList.remove("sk"); v.textContent = ""; v.parentElement.classList.add("fade-in"); }
  if (typeof value === "string") { v.textContent = value; } else tween(v, value, d);
  let u = card.querySelector(".value .u"); if (!u) { u = document.createElement("span"); u.className = "u"; v.after(u); }
  u.textContent = unit || "";
  v.style.color = valueColor || "";
  card.querySelector('[data-k="foot"]').innerHTML = foot;
  const sp = card.querySelector('[data-k="spark"]');
  if (spark) sp.innerHTML = spark; else if (spark === null) sp.style.display = "none";
}
function sparkSVG(vals, color, id) {
  const pts = vals.filter(isNum); if (pts.length < 2) return '<svg viewBox="0 0 100 30"></svg>';
  const lo = Math.min(...pts), hi = Math.max(...pts), span = hi - lo || 1, n = vals.length;
  let d = "", first = true;
  vals.forEach((v, i) => { if (!isNum(v)) return; const x = (i / (n - 1)) * 100, y = 27 - ((v - lo) / span) * 24;
    d += (first ? "M" : "L") + x.toFixed(2) + " " + y.toFixed(2); first = false; });
  return `<svg viewBox="0 0 100 30" preserveAspectRatio="none"><defs><linearGradient id="g${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${color}" stop-opacity=".28"/><stop offset="1" stop-color="${color}" stop-opacity="0"/></linearGradient></defs>
    <path d="${d} L100 30 L0 30 Z" fill="url(#g${id})"/><path d="${d}" fill="none" stroke="${color}" stroke-width="1.6" vector-effect="non-scaling-stroke" stroke-linejoin="round"/></svg>`;
}
const recent = (k, secs = 3600) => { const c = Date.now() / 1000 - secs; return S.hist.filter(h => h.t >= c).map(h => h[k]); };
function pill(t, k) { return `<span class="pill ${k || ""}">${esc(t)}</span>`; }

function render() {
  const d = S.data, id = d.identity || {}, hr = d.hashrate || {}, tp = d.temps || {}, fan = d.fan || {}, pw = d.power || {},
    up = d.uptime || {}, sh = d.shares || {}, er = d.errors || {}, dev = d.device || {}, ch = d.chips || {};
  // headline
  const bits = [];
  if (id.model || id.product) bits.push(`<span>${esc([id.product === "Avalonnano" ? "Avalon Nano" : id.product, id.model ? id.model.replace(/^nano/i, "") : ""].filter(Boolean).join(" ").trim() || "Avalon")}</span>`);
  if (id.core) bits.push(`<span>${esc(id.core)} · ${esc(id.chips || "")} chips</span>`);
  if (id.firmware) bits.push(`<span>FW <span class="mono">${esc(id.firmware)}</span></span>`);
  if (d.miner_host && d.source !== "remote") bits.push(`<span class="mono">${esc(d.miner_host)}</span>`);
  $("#subline").innerHTML = bits.join("") || "<span>Avalon miner</span>";
  const hashing = er.hash_status == 1 || (er.hash_status == null && d.ok);
  const alive = (dev.status || "").toLowerCase() === "alive";
  $("#pills").innerHTML = d.ok
    ? pill(hashing ? "Hashing" : "Not hashing", hashing ? "good" : "bad") + pill(dev.status ? "Device " + dev.status.toLowerCase() : "API up", alive ? "good" : "warn") +
      ((d.warnings || []).length ? pill(d.warnings.length + (d.warnings.length > 1 ? " warnings" : " warning"), "warn") : "")
    : pill("Offline", "bad");

  // KPIs
  const hero = isNum(hr.ths_hero) ? +hr.ths_hero : isNum(hr.ths_5m) ? +hr.ths_5m : isNum(hr.ghs_spd) ? hr.ghs_spd / 1000 : null;
  setKpi("k-hash", hero, 3, "TH/s", `<span>5s <b class="num">${fmt(hr.ths_5s, 2)}</b></span><span>15m <b class="num">${fmt(hr.ths_15m, 2)}</b></span>`, sparkSVG(recent("h5m"), "#f7931a", "h"));
  const tgt = isNum(tp.target) ? +tp.target : null;
  setKpi("k-temp", tp.max, 0, "°C", `<span>avg <b class="num">${fmt(tp.avg)}°</b></span><span>target <b class="num">${fmt(tp.target)}°</b></span>`,
    `<div class="meter" style="margin-top:12px"><i style="width:${isNum(tp.max) ? Math.min(100, tp.max / 110 * 100) : 0}%;background:${tempColor(tp.max, tgt)}"></i>${tgt ? `<b style="left:${tgt / 110 * 100}%"></b>` : ""}</div>`,
    tempColor(tp.max, tgt));
  setKpi("k-power", pw.watts, 0, "W", `<span><b class="num">${fmt(pw.voltage_v, 2)}</b> V</span><span>MPO <b class="num">${fmt(pw.mpo_w)}</b> W</span>`, sparkSVG(recent("w"), "#6aa8ff", "p"));
  const eff = isNum(pw.watts) && hero ? pw.watts / hero : null;
  setKpi("k-eff", eff, 1, "J/TH", `<span>${pw.watts && hero ? `<b class="num">${fmt(hero * 1000 / pw.watts, 1)}</b> GH/W` : ""}</span><span class="f2">${d.control && d.control.work_name ? "mode <b>" + esc(cap(d.control.work_name)) + "</b>" : ""}</span>`, sparkSVG(S.hist.slice(-240).map(h => isNum(h.w) && h.h5m ? h.w / h.h5m : null), "#a78bfa", "e"));
  setKpi("k-fan", fan.rpm, 0, "RPM", `<span><b class="num">${fmt(fan.percent)}%</b> of max</span><span></span>`,
    `<div class="meter" style="margin-top:12px"><i style="width:${isNum(fan.percent) ? fan.percent : 0}%;background:var(--info)"></i></div>`);
  setKpi("k-up", up.human || "—", 0, "", `<span title="${up.seconds ? esc(absTime(Math.floor(Date.now() / 1000) - up.seconds)) : ""}">since <b>${up.seconds ? esc(new Date((Math.floor(Date.now() / 1000) - up.seconds) * 1000).toLocaleDateString(undefined, { month: "short", day: "numeric" })) : "—"}</b></span>`, null);

  // hashrate card
  const hv = $("#heroV"); if (hv.classList.contains("sk")) hv.classList.remove("sk");
  if (d.ok) tween(hv, hero, 3); else { hv.textContent = "Offline"; hv.dataset.v = ""; }
  const h1 = S.hist.find(h => h.t >= Date.now() / 1000 - 3600 && isNum(h.h5m));
  const dEl = $("#heroD");
  if (h1 && hero && S.hist.length > 20) { const p = (hero - h1.h5m) / h1.h5m * 100;
    dEl.className = "delta " + (p >= 0.05 ? "up" : p <= -0.05 ? "down" : ""); dEl.textContent = (p >= 0 ? "▲ " : "▼ ") + Math.abs(p).toFixed(1) + "% vs 1h"; }
  else { dEl.className = "delta"; dEl.textContent = "collecting history"; }
  $("#hrStats").innerHTML = [["5s", hr.ths_5s], ["1m", hr.ths_1m], ["15m", hr.ths_15m], ["Lifetime", hr.ths_avg]].map(([k, v]) => `<div><span>${k}</span><b>${fmt(v, 2)}</b></div>`).join("");
  $("#hrSub").innerHTML = [["GHS spd", fmt(hr.ghs_spd, 1)], ["GHS mm", fmt(hr.ghs_mm, 1)], ["GHS avg", fmt(hr.ghs_avg, 1)], ["Work utility", fmt(hr.wu, 0)]].map(([k, v]) => `<div><span>${k}</span><b>${v}</b></div>`).join("");

  // health
  const w = d.warnings || [];
  let al = "";
  if (!d.ok) al = `<div class="alert bad">${I.warn}<div>Can't read the miner. ${esc((d.query_errors || []).join("; ") || "No reply.")}</div></div>`;
  else if (w.length) al = w.map(x => `<div class="alert">${I.warn}<div>${esc(x)}</div></div>`).join("");
  else al = `<div class="alert ok">${I.ok}<div>All systems normal. No errors or warnings.</div></div>`;
  $("#alerts").innerHTML = al;
  const acc = $("#acc"); acc.classList.remove("sk"); tween(acc, sh.accepted, 0);
  const tot = (+sh.accepted || 0) + (+sh.rejected || 0) + (+sh.stale || 0);
  $("#accPct").textContent = tot ? (sh.accepted / tot * 100).toFixed(2) + "%" : "";
  $("#ratio").innerHTML = tot ? `<i style="flex-grow:${sh.accepted};background:var(--good)"></i><i style="flex-grow:${Math.max(sh.stale, tot * .004)};background:var(--warn)"></i><i style="flex-grow:${Math.max(sh.rejected, tot * .004)};background:var(--bad)"></i>` : "";
  const echu = (er.echu && er.echu.length) ? er.echu.join(" ") : "0";
  $("#shareKv").innerHTML = kv([
    ["Rejected", `${fmt(sh.rejected)} <span class="dim">· ${fmt(sh.reject_pct, 2)}%</span>`],
    ["Stale", `${fmt(sh.stale)} <span class="dim">· ${fmt(sh.stale_pct, 2)}%</span>`],
    ["Hardware errors", `${fmt(sh.hw_errors)} <span class="dim">· DH ${isNum(sh.dh_pct) ? fmt(sh.dh_pct, 3) + "%" : "—"}</span>`],
    ["Best share", `<span title="${fmt(sh.best_share)}">${si(sh.best_share)}</span>`],
    ["Pool ping", isNum(er.ping_ms) ? fmt(er.ping_ms) + " ms" : "—"],
    ["Get / remote failures", `${fmt(sh.get_failures)} / ${fmt(sh.remote_failures)}`],
    ["ECHU / ECMM · CRC", `${esc(echu)} / ${fmt(er.ecmm)} · ${fmt(er.crc)}`],
    ["Blocks found", fmt(sh.found_blocks)],
  ]);

  // temps legend
  $("#tHint").textContent = tgt ? `target ${tgt}°C` : "";
  $("#tLegend").innerHTML = [["Max", tp.max, "#ff6b6b"], ["Avg", tp.avg, "#f7931a"], ["Board", tp.ambient, "#3ddc97"], ["Outlet", tp.outlet, "#6aa8ff"]]
    .map(([k, v, c]) => `<span><i style="background:${c}"></i>${k} <b class="num" style="color:var(--text)">${fmt(v)}°</b></span>`).join("") + (tgt ? `<span><i style="background:repeating-linear-gradient(90deg,#a3a8b4 0 3px,transparent 3px 6px)"></i>Target</span>` : "");

  // chips
  const ct = ch.temps || [], cv = ch.volts_mv || [], cn = ch.nonces || [];
  const nC = Math.max(ct.length, cv.length, cn.length);
  if (nC) {
    const v = ct.filter(isNum), lo = Math.min(...v), hi = Math.max(...v);
    $("#chipHint").textContent = v.length ? `${fmt(lo)}–${fmt(hi)}°C · spread ${fmt(hi - lo)}°` : "";
    const hlo = Math.min(lo, (tgt || 90) - 20), hhi = Math.max(hi, (tgt || 90) + 4);
    $("#chips").innerHTML = Array.from({ length: nC }, (_, i) => `<div class="chip" style="--c:${heat(ct[i], hlo, hhi)}">
      <div class="n"><span>#${i + 1}</span>${ct[i] === hi ? '<span style="color:var(--bad)">hot</span>' : ""}</div>
      <div class="t" style="color:${heat(ct[i], hlo, hhi)}">${isNum(ct[i]) ? fmt(ct[i]) + "°" : "—"}</div>
      <div class="m">${isNum(cv[i]) ? fmt(cv[i]) + " mV" : ""}</div><div class="m mw">${isNum(cn[i]) ? "MW " + fmt(cn[i]) : ""}</div></div>`).join("");
  } else { $("#chipHint").textContent = ""; $("#chips").innerHTML = '<div class="dim">No per-chip data in this reply.</div>'; }

  // pools
  const pools = d.pools || [];
  $("#poolHint").textContent = pools.length ? `${pools.filter(p => (p.status || "").toLowerCase() === "alive").length} of ${pools.length} alive` : "";
  $("#pools").innerHTML = pools.length ? pools.map(p => {
    const alive = (p.status || "").toLowerCase() === "alive";
    const st = p.stratum_active ? pill("Active", "good") : alive ? pill("Standby", "info") : pill(p.status || "Unknown", "bad");
    const url = (p.url || "").replace(/^stratum\+tcp:\/\//, "");
    return `<div class="pool${p.stratum_active ? " active" : ""}"><div class="idx">${esc(p.id)}</div>
      <div style="min-width:0"><div class="url mono" title="${esc(p.url)}">${esc(url)}</div><div class="usr mono" title="${esc(p.user)}">${esc(p.user || "")}${p.block_height ? ` · block ${fmt(p.block_height)}` : ""}</div></div>
      <div class="col c2 num"><span>Shares acc / rej / stale</span><b>${fmt(p.accepted)} <span class="dim">/ ${fmt(p.rejected)} / ${fmt(p.stale)}</span></b></div>
      <div class="col c3 num"><span>Difficulty · last share</span><b>${fmt(p.difficulty)} <span class="dim" data-ls="${p.last_share_unix || 0}">· ${p.last_share_unix ? rel(p.last_share_unix) : "never"}</span></b></div>
      <div style="text-align:right">${st}</div></div>`; }).join("") : '<div class="dim">No pools reported.</div>';

  // device
  const c = d.control || {};
  const ps = (pw.ps_raw || []).join(" ");
  const nf = er.net_fail || [];
  $("#devKv").innerHTML = kv([
    ["Model", esc([id.product, id.model].filter(Boolean).join(" ") || "—")],
    ["Firmware", `<span class="mono">${esc(id.firmware || "—")}</span>`],
    ["Hardware", `<span class="mono">${esc(id.hardware || "—")}</span>`],
    ["Type", `<span class="mono">${esc([id.hwtype, id.swtype].filter(Boolean).join(" / ") || "—")}</span>`],
    ["cgminer · API", `${esc(id.cgminer || "—")} · ${esc(id.api || "—")}`],
    ["Work mode", c.work_name ? esc(cap(c.work_name)) + (isNum(c.work_level) ? ` <span class="dim">(level ${c.work_level})</span>` : "") : (isNum(pw.work_level) ? "Level " + pw.work_level : "—")],
    ["Light", c.led_hex ? `<span style="display:inline-block;width:10px;height:10px;border-radius:50%;background:${esc(c.led_hex)};box-shadow:0 0 8px ${esc(c.led_hex)};margin-right:6px"></span>${esc(cap(c.led_mode || ""))} · ${fmt(c.led_brightness)}%` : (er.led ? "LED " + esc(er.led) : "—")],
    ["Power supply", `${pw.ps_ok === false ? '<span style="color:var(--bad)">Fault</span>' : "OK"} <span class="dim mono">[${esc(ps || "—")}]</span>`],
    ["Soft-off", esc(er.soft_off == null ? "0" : er.soft_off)],
    ["Network drops", nf.length ? `${nf.length} <span class="dim">· last ${rel(nf[nf.length - 1].unix)}</span>` : "None"],
    ["Device", esc([dev.name, dev.enabled === "Y" ? "enabled" : dev.enabled].filter(Boolean).join(" · ") || "—")],
  ]);
  if (nf.length) $("#devKv").title = "Network drops:\n" + nf.map(n => absTime(n.unix)).join("\n");
  renderRaw();
  $("#foot2").textContent = d.source === "remote" ? "Preview: reading the miner through its existing dashboard" : (d.miner_host ? "Miner API " + d.miner_host : "");
}
const cap = (s) => String(s || "").replace(/^\w/, c => c.toUpperCase());
function kv(rows) { return rows.map(([k, v]) => `<div><span>${esc(k)}</span><b>${v}</b></div>`).join(""); }
function renderPoolTimes() { document.querySelectorAll("[data-ls]").forEach(el => { const u = +el.dataset.ls; el.textContent = "· " + (u ? rel(u) : "never"); }); }
function renderRaw() {
  if (!S.data) return;
  const mm = S.data.mm_fields || {}, q = ($("#rawQ").value || "").toLowerCase();
  const keys = Object.keys(mm).sort((a, b) => a.localeCompare(b));
  $("#rawN").textContent = keys.length ? `${keys.length} fields` : "";
  $("#rawGrid").innerHTML = keys.filter(k => !q || k.toLowerCase().includes(q) || String(mm[k]).toLowerCase().includes(q))
    .map(k => `<div><span>${esc(k)}</span><b>${esc(mm[k])}</b></div>`).join("") || '<div><span>No matches</span></div>';
}

/* ---------------- charts ---------------- */
function grad(u, c1, a1, a2) {
  const g = u.ctx.createLinearGradient(0, u.bbox.top, 0, u.bbox.top + u.bbox.height);
  g.addColorStop(0, c1.replace("A", a1)); g.addColorStop(1, c1.replace("A", a2)); return g;
}
function tooltipPlugin(tipId, fmtRow) {
  return { hooks: {
    setCursor: (u) => {
      const tip = document.getElementById(tipId); if (!tip) return;
      const i = u.cursor.idx;
      if (i == null || u.data[0][i] == null) { tip.classList.remove("on"); return; }
      tip.innerHTML = `<div class="t">${new Date(u.data[0][i] * 1000).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit", second: "2-digit" })}</div>` +
        u.series.slice(1).map((s, k) => s.show && s.label ? `<div class="r"><span><i style="background:${s._c}"></i>${s.label}</span><b class="num">${fmtRow(u.data[k + 1][i])}</b></div>` : "").join("");
      const L = u.over.offsetLeft + u.cursor.left, W = tip.offsetWidth, pw = u.over.parentElement.parentElement.clientWidth;
      tip.style.left = Math.max(0, Math.min(pw - W, L - W / 2)) + "px"; tip.style.top = (u.over.offsetTop + 6) + "px";
      tip.classList.add("on");
    } } };
}
const axis = (fmtv) => ({ stroke: "#6c717d", grid: { stroke: "rgba(255,255,255,.045)", width: 1 }, ticks: { show: false }, font: "11px Geist, sans-serif", size: 42, gap: 6, values: fmtv });
function timeAxis() {
  return { stroke: "#6c717d", grid: { show: false }, ticks: { show: false }, font: "11px Geist, sans-serif", size: 28, space: 70,
    values: (u, ts) => { const span = (u.scales.x.max || 0) - (u.scales.x.min || 0); let last = "";
      return ts.map(t => { const d = new Date(t * 1000);
        const s = span > 86400 * 1.5 ? d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
          : d.toLocaleTimeString(undefined, span < 900 ? { hour: "numeric", minute: "2-digit", second: "2-digit" } : { hour: "numeric", minute: "2-digit" });
        if (s === last) return ""; last = s; return s; }); } };
}
function mkChart(el, series, yfmt, tipId, rowFmt, yRange) {
  const spline = uPlot.paths && uPlot.paths.spline ? uPlot.paths.spline() : undefined;
  const opts = {
    width: el.clientWidth || 600, height: el.clientHeight || 260, padding: [10, 18, 0, 0], pxAlign: 0,
    cursor: { points: { size: 7, width: 2, fill: "#0f1115" }, drag: { x: false, y: false }, y: false },
    legend: { show: false }, scales: { x: { time: true }, y: { range: yRange } },
    axes: [timeAxis(), axis(yfmt)],
    series: [{}].concat(series.map(s => ({ label: s.label, stroke: s.c, width: s.w || 2, dash: s.dash, _c: s.c, paths: s.linear ? undefined : spline,
      fill: s.fill ? (u) => grad(u, s.fill, ".32", "0") : undefined, points: { show: false }, spanGaps: true }))),
    plugins: [tooltipPlugin(tipId, rowFmt)],
  };
  const u = new uPlot(opts, [[], ...series.map(() => [])], el);
  new ResizeObserver(() => { if (el.clientWidth) u.setSize({ width: el.clientWidth, height: el.clientHeight }); }).observe(el);
  el.addEventListener("mouseleave", () => { const t = document.getElementById(tipId); t && t.classList.remove("on"); });
  return u;
}
function initCharts() {
  if (!window.uPlot) return;
  S.charts.hash = mkChart($("#chHash"), [
    { label: "5s", c: "rgba(255,255,255,.28)", w: 1, linear: true },
    { label: "15m avg", c: "#ffd8a3", w: 1.4, dash: [4, 4] },
    { label: "5m avg", c: "#f7931a", w: 2.4, fill: "rgba(247,147,26,A)" },
  ], (u, v) => v.map(x => x.toFixed(1)), "tipHash", v => isNum(v) ? fmt(v, 3) + " TH/s" : "—",
    (u, lo, hi) => { if (lo == null) return [0, 5]; const p = Math.max(.15, (hi - lo) * .25); return [Math.max(0, lo - p), hi + p]; });
  S.charts.temp = mkChart($("#chTemp"), [
    { label: "Max", c: "#ff6b6b", w: 2 }, { label: "Avg", c: "#f7931a", w: 2, fill: "rgba(247,147,26,A)" },
    { label: "Board", c: "#3ddc97", w: 1.6 }, { label: "Outlet", c: "#6aa8ff", w: 1.6 }, { label: "Target", c: "rgba(163,168,180,.7)", w: 1, dash: [3, 4], linear: true },
  ], (u, v) => v.map(x => x + "°"), "tipTemp", v => isNum(v) ? fmt(v) + "°C" : "—",
    (u, lo, hi) => lo == null ? [20, 100] : [Math.max(0, Math.floor((lo - 6) / 5) * 5), Math.ceil((hi + 4) / 5) * 5]);
}
function drawCharts() {
  if (!S.charts.hash) return;
  const c = Date.now() / 1000 - S.range, h = S.hist.filter(x => x.t >= c);
  const tgt = S.data && S.data.temps ? S.data.temps.target : null;
  const col = (k) => h.map(x => isNum(x[k]) ? +x[k] : null), ts = h.map(x => x.t);
  S.charts.hash.setData([ts, col("h5s"), col("h15m"), col("h5m")]);
  S.charts.temp.setData([ts, col("tmax"), col("tavg"), col("tamb"), col("tout"), h.map(() => isNum(tgt) ? +tgt : null)]);
  const msg = h.length < 2 ? `Collecting history… charts fill in as ${esc(APP)} runs (a point every ${(S.state && S.state.poll_seconds) || 15}s).` : "";
  $("#chHashE").textContent = msg; $("#chTempE").textContent = msg;
}

/* ---------------- settings sheet ---------------- */
function openSettings() {
  const st = S.state, locked = st.locked;
  const scrim = document.createElement("div"); scrim.className = "scrim";
  const sh = document.createElement("aside"); sh.className = "sheet"; sh.setAttribute("role", "dialog"); sh.setAttribute("aria-label", "Settings");
  sh.innerHTML = `<div class="row"><h2 class="grow">Settings</h2><button class="icon-btn" id="sx" aria-label="Close">${I.x}</button></div>
    ${locked ? `<div class="alert" style="margin-top:16px">${I.warn}<div>This is a read-only preview. Settings are locked.</div></div>` : ""}
    <h3>Miners</h3><div class="mlist">${(st.miners || []).map(m => `<div class="mitem"><div class="grow"><b>${esc(m.name)}</b><span class="mono">${m.kind === "remote" ? "preview source" : esc(m.host + ":" + (m.port || 4028))}</span></div>
      ${locked ? "" : `<button class="btn danger sm" data-del="${esc(m.id)}">Remove</button>`}</div>`).join("")}</div>
    ${locked ? "" : `<h3>Add a miner</h3><form class="card" id="a-form" style="padding:16px" autocomplete="off">${minerForm("a")}</form>`}
    <h3>Password</h3>
    ${locked ? `<p class="muted" style="margin:0">Managed by the preview host.</p>` : `<form id="pwf" class="card" style="padding:16px">
      <input type="text" autocomplete="username" value="${esc(APP)}" class="sr" tabindex="-1" aria-hidden="true">
      ${st.auth_required ? `<label class="f" style="margin-top:0" for="pwc">Current password</label><input class="input" id="pwc" type="password" autocomplete="current-password">` : `<p class="muted" style="margin:0 0 4px">Optional. Anyone on your Wi-Fi can open ${esc(APP)} unless you set one.</p>`}
      <label class="f" for="pwn">${st.auth_required ? "New password (leave empty to remove)" : "New password"}</label><input class="input" id="pwn" type="password" autocomplete="new-password" minlength="6">
      <div class="err" id="pwe"></div><button class="btn ghost block">${st.auth_required ? "Update password" : "Set password"}</button></form>`}
    ${(st.lan_urls || []).length ? `<h3>Open on your phone</h3><div class="card" style="padding:16px"><p class="muted" style="margin:0 0 8px">On the same Wi-Fi, open:</p>${st.lan_urls.map(u => `<div class="mono" style="font-size:15px;padding:4px 0">${esc(u)}</div>`).join("")}<p class="fine" style="margin-top:10px">Then use Share → Add to Home Screen for an app icon.</p></div>` : ""}
    <h3>About</h3><p class="muted" style="margin:0">${esc(APP)} ${esc(st.version || "")} reads your miner over its local API (port 4028) and never changes miner settings. Your data stays on your network.</p>
    ${st.auth_required ? `<button class="btn ghost block" id="so" style="margin-top:22px">Sign out</button>` : ""}`;
  document.body.append(scrim, sh);
  requestAnimationFrame(() => { scrim.classList.add("on"); sh.classList.add("on"); });
  const close = () => { scrim.classList.remove("on"); sh.classList.remove("on"); setTimeout(() => { scrim.remove(); sh.remove(); }, 300); };
  scrim.onclick = close; $("#sx", sh).onclick = close;
  document.addEventListener("keydown", function k(e) { if (e.key === "Escape") { close(); document.removeEventListener("keydown", k); } });
  sh.querySelectorAll("[data-del]").forEach(b => b.onclick = async () => {
    if (!confirm("Remove this miner from " + APP + "? Its chart history is deleted too.")) return;
    try { await api("/api/miners/" + encodeURIComponent(b.dataset.del), { method: "DELETE", headers: { "Content-Type": "application/json" } }); close(); boot(); } catch (e) { toast(e.message); }
  });
  if (!locked) {
    wireMinerForm("a", async (r) => { localStorage.setItem("hw_miner", r.id); close(); boot(); });
    $("#pwf", sh).onsubmit = async (e) => { e.preventDefault();
      try { await api("/api/password", { method: "POST", body: { current: ($("#pwc", sh) || {}).value || "", new: $("#pwn", sh).value } });
        toast($("#pwn", sh).value ? "Password saved" : "Password removed"); close(); boot(); }
      catch (er) { $("#pwe", sh).textContent = er.message; } };
  }
  const so = $("#so", sh); if (so) so.onclick = async () => { await api("/api/logout", { method: "POST", body: {} }); close(); boot(); };
}

document.addEventListener("visibilitychange", () => { if (!document.hidden && S.miner && $("#live")) refresh(false); });
boot();
})();

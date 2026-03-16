package server

// dashboardHTML is the self-contained stats dashboard served at GET /v1/dashboard.
// Fetches /v1/stats and /v1/projects live; no external dependencies.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>pincherMCP · Dashboard</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#0d1117;--surface:#161b22;--border:#30363d;
  --text:#e6edf3;--muted:#8b949e;--accent:#58a6ff;
  --green:#3fb950;--purple:#a371f7;--orange:#f0883e;--red:#f85149;
}
body{background:var(--bg);color:var(--text);font-family:ui-sans-serif,system-ui,-apple-system,sans-serif;min-height:100vh;padding:0}
header{background:linear-gradient(135deg,#0d1117 0%,#1a1f2e 100%);border-bottom:1px solid var(--border);padding:24px 32px;display:flex;align-items:center;gap:16px}
header svg{flex-shrink:0}
header h1{font-size:22px;font-weight:700;letter-spacing:-.5px}
header h1 span{color:var(--accent)}
header p{color:var(--muted);font-size:13px;margin-top:3px}
.badge{display:inline-flex;align-items:center;gap:5px;padding:3px 10px;border-radius:20px;font-size:11px;font-weight:600;letter-spacing:.4px}
.badge-green{background:rgba(63,185,80,.15);color:var(--green);border:1px solid rgba(63,185,80,.3)}
.badge-blue{background:rgba(88,166,255,.12);color:var(--accent);border:1px solid rgba(88,166,255,.25)}
main{max-width:1200px;margin:0 auto;padding:32px}
.section-title{font-size:11px;font-weight:600;letter-spacing:1px;text-transform:uppercase;color:var(--muted);margin-bottom:14px}
.grid{display:grid;gap:16px;margin-bottom:32px}
.grid-4{grid-template-columns:repeat(auto-fit,minmax(220px,1fr))}
.grid-2{grid-template-columns:repeat(auto-fit,minmax(340px,1fr))}
.card{background:var(--surface);border:1px solid var(--border);border-radius:10px;padding:20px;position:relative;overflow:hidden}
.card::before{content:'';position:absolute;top:0;left:0;right:0;height:2px;background:linear-gradient(90deg,var(--accent),var(--purple))}
.card.green::before{background:linear-gradient(90deg,var(--green),var(--accent))}
.card.orange::before{background:linear-gradient(90deg,var(--orange),var(--red))}
.card.purple::before{background:linear-gradient(90deg,var(--purple),var(--accent))}
.card-label{font-size:11px;color:var(--muted);font-weight:500;letter-spacing:.3px;text-transform:uppercase;margin-bottom:8px}
.card-value{font-size:32px;font-weight:700;line-height:1;letter-spacing:-1px}
.card-value.blue{color:var(--accent)}
.card-value.green{color:var(--green)}
.card-value.orange{color:var(--orange)}
.card-value.purple{color:var(--purple)}
.card-sub{font-size:12px;color:var(--muted);margin-top:6px}
.proj-card{background:var(--surface);border:1px solid var(--border);border-radius:10px;padding:18px;transition:border-color .2s;position:relative}
.proj-card:hover{border-color:var(--accent)}
.proj-card.invalid{border-color:rgba(248,81,73,.4)}
.proj-card.invalid::before{content:'';position:absolute;top:0;left:0;right:0;height:2px;background:var(--red);border-radius:10px 10px 0 0}
.proj-card.stale{border-color:rgba(240,136,62,.4)}
.proj-card.stale::before{content:'';position:absolute;top:0;left:0;right:0;height:2px;background:var(--orange);border-radius:10px 10px 0 0}
.pill.stale-pill{background:rgba(240,136,62,.1);color:var(--orange);border-color:rgba(240,136,62,.3)}
.proj-header{display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:4px}
.proj-name{font-size:15px;font-weight:600}
.proj-actions{display:flex;gap:6px;flex-shrink:0}
.proj-btn{background:none;border:1px solid var(--border);border-radius:6px;color:var(--muted);cursor:pointer;font-size:11px;padding:3px 8px;transition:all .15s}
.proj-btn:hover{border-color:var(--accent);color:var(--accent)}
.proj-btn.danger:hover{border-color:var(--red);color:var(--red)}
.proj-btn:disabled{opacity:.4;cursor:default}
.proj-path{font-size:11px;color:var(--muted);margin-bottom:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.proj-path.missing{color:var(--red)}
.proj-stats{display:flex;gap:16px;flex-wrap:wrap}
.proj-stat{text-align:center}
.proj-stat-val{font-size:18px;font-weight:700;color:var(--accent)}
.proj-stat-label{font-size:10px;color:var(--muted);text-transform:uppercase;letter-spacing:.5px}
.pill{display:inline-block;padding:2px 8px;border-radius:12px;font-size:11px;background:rgba(88,166,255,.12);color:var(--accent);border:1px solid rgba(88,166,255,.2);font-family:ui-monospace,monospace}
.pill.warn{background:rgba(248,81,73,.1);color:var(--red);border-color:rgba(248,81,73,.3)}
.empty{color:var(--muted);font-size:13px;padding:24px;text-align:center}
.refresh{position:fixed;bottom:24px;right:24px;background:var(--accent);color:#0d1117;border:none;border-radius:8px;padding:10px 18px;font-size:13px;font-weight:600;cursor:pointer;transition:opacity .2s}
.refresh:hover{opacity:.85}
.error{background:rgba(248,81,73,.1);border:1px solid rgba(248,81,73,.3);border-radius:8px;padding:16px;color:var(--red);font-size:13px;margin-bottom:16px}
.loading{color:var(--muted);font-size:13px;padding:8px 0}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.4}}
.loading{animation:pulse 1.5s ease-in-out infinite}
.footer{text-align:center;padding:24px;color:var(--muted);font-size:12px;border-top:1px solid var(--border);margin-top:8px}
.footer a{color:var(--accent);text-decoration:none}
.toast{position:fixed;bottom:72px;right:24px;background:#161b22;border:1px solid var(--border);border-radius:8px;padding:10px 16px;font-size:13px;color:var(--text);opacity:0;transition:opacity .3s;pointer-events:none}
.toast.show{opacity:1}
</style>
</head>
<body>
<header>
  <svg width="36" height="36" viewBox="0 0 36 36" fill="none">
    <circle cx="18" cy="18" r="17" stroke="url(#hg)" stroke-width="2"/>
    <line x1="10" y1="10" x2="26" y2="26" stroke="#58a6ff" stroke-width="2.5" stroke-linecap="round"/>
    <line x1="26" y1="10" x2="10" y2="26" stroke="#a371f7" stroke-width="2.5" stroke-linecap="round"/>
    <circle cx="18" cy="18" r="4" fill="#58a6ff"/>
    <defs><linearGradient id="hg" x1="0" y1="0" x2="36" y2="36"><stop stop-color="#58a6ff"/><stop offset="1" stop-color="#a371f7"/></linearGradient></defs>
  </svg>
  <div>
    <h1>pincher<span>MCP</span> <span style="font-size:12px;font-weight:400" id="ver"></span></h1>
    <p>Codebase intelligence · Token savings dashboard</p>
  </div>
  <div style="margin-left:auto;display:flex;gap:8px;align-items:center">
    <span class="badge badge-green" id="health-badge">● checking…</span>
    <span class="badge badge-blue" id="last-refresh">—</span>
  </div>
</header>

<main>
  <div id="error-box"></div>

  <p class="section-title">This Session</p>
  <div class="grid grid-4" id="session-cards">
    <div class="loading">Loading session stats…</div>
  </div>

  <p class="section-title">All-Time ROI</p>
  <div class="grid grid-4" id="alltime-cards">
    <div class="loading">Loading all-time stats…</div>
  </div>

  <p class="section-title">Indexed Projects</p>
  <div class="grid grid-2" id="projects-grid">
    <div class="loading">Loading projects…</div>
  </div>
</main>

<div class="footer">pincherMCP · <a href="/v1/openapi.json" target="_blank">OpenAPI spec</a> · <a href="/v1/health" target="_blank">Health</a></div>

<button class="refresh" onclick="load()">↻ Refresh</button>
<div class="toast" id="toast"></div>

<script>
const fmt = n => n >= 1e6 ? (n/1e6).toFixed(1)+'M' : n >= 1e3 ? (n/1e3).toFixed(1)+'K' : String(n);
const fmtMs = ms => ms < 1 ? '<1ms' : ms+'ms';
function timeAgo(iso) {
  if (!iso) return '—';
  const secs = Math.floor((Date.now() - new Date(iso)) / 1000);
  if (secs < 60) return 'just now';
  if (secs < 3600) return Math.floor(secs/60) + 'm ago';
  if (secs < 86400) return Math.floor(secs/3600) + 'h ago';
  return Math.floor(secs/86400) + 'd ago';
}
const STALE_HOURS = 24;

function showToast(msg, ok=true) {
  const t = document.getElementById('toast');
  t.textContent = msg;
  t.style.borderColor = ok ? 'var(--green)' : 'var(--red)';
  t.classList.add('show');
  setTimeout(() => t.classList.remove('show'), 2500);
}

function statCard(label, value, cls, sub, cardCls) {
  return ` + "`" + `<div class="card ${cardCls||''}">
    <div class="card-label">${label}</div>
    <div class="card-value ${cls}">${value}</div>
    ${sub ? ` + "`" + `<div class="card-sub">${sub}</div>` + "`" + ` : ''}
  </div>` + "`" + `;
}

async function reindex(id, btn) {
  btn.disabled = true;
  btn.textContent = '…';
  try {
    const r = await fetch('/v1/index', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({path: id})});
    const d = await r.json();
    const res = d.result || d;
    showToast(` + "`" + `Re-indexed: ${res.symbols||0} symbols, ${res.edges||0} edges` + "`" + `);
    load();
  } catch(e) {
    showToast('Re-index failed: '+e.message, false);
    btn.disabled = false;
    btn.textContent = '⟳';
  }
}

async function deleteProject(id, name) {
  if (!confirm(` + "`" + `Remove project "${name}" from the index?\n\nThis deletes all symbols, edges, and graph data for this project. The source files are NOT deleted.` + "`" + `)) return;
  try {
    const r = await fetch('/v1/projects', {method:'DELETE', headers:{'Content-Type':'application/json'}, body: JSON.stringify({id})});
    if (!r.ok) throw new Error(await r.text());
    showToast(` + "`" + `Project "${name}" removed.` + "`" + `);
    load();
  } catch(e) {
    showToast('Delete failed: '+e.message, false);
  }
}

async function load() {
  document.getElementById('last-refresh').textContent = 'refreshing…';
  const errBox = document.getElementById('error-box');
  errBox.innerHTML = '';

  // Health
  try {
    const h = await fetch('/v1/health').then(r=>r.json());
    const hb = document.getElementById('health-badge');
    hb.textContent = '● online';
    hb.className = 'badge badge-green';
    document.getElementById('ver').textContent = h.version ? 'v'+h.version : '';
  } catch(e) {
    document.getElementById('health-badge').textContent = '● offline';
    document.getElementById('health-badge').style.color = 'var(--red)';
  }

  // Stats
  try {
    const r = await fetch('/v1/stats', {method:'POST', headers:{'Content-Type':'application/json'}, body:'{}'});
    const data = await r.json();
    const result = data.result || data;
    const s = result.session || {};
    const a = result.all_time || {};

    const sc = document.getElementById('session-cards');
    sc.innerHTML =
      statCard('Tool Calls', fmt(s.calls||0), 'blue', 'this session', '') +
      statCard('Tokens Saved', fmt(s.tokens_saved||0), 'green', 'vs reading full files', 'green') +
      statCard('Cost Avoided', s.total_cost_avoided||'$0.0000', 'orange', 'estimated savings', 'orange') +
      statCard('Avg Latency', fmtMs(s.avg_latency_ms||0), 'purple', 'per tool call', 'purple');

    const ac = document.getElementById('alltime-cards');
    if (a.calls) {
      ac.innerHTML =
        statCard('Total Calls', fmt(a.calls||0), 'blue', 'all sessions', '') +
        statCard('Total Tokens Saved', fmt(a.tokens_saved||0), 'green', 'cumulative', 'green') +
        statCard('Total Cost Avoided', a.total_cost_avoided||'$0.0000', 'orange', 'provable ROI', 'orange') +
        statCard('Tokens Used', fmt(a.tokens_used||0), 'purple', 'context consumed', 'purple');
    } else {
      ac.innerHTML = '<div class="empty">No previous sessions recorded yet — stats accumulate after tool calls.</div>';
    }
  } catch(e) {
    errBox.innerHTML = ` + "`" + `<div class="error">Failed to load stats: ${e.message}</div>` + "`" + `;
    document.getElementById('session-cards').innerHTML = '<div class="empty">—</div>';
    document.getElementById('alltime-cards').innerHTML = '<div class="empty">—</div>';
  }

  // Projects
  try {
    const r = await fetch('/v1/projects');
    const data = await r.json();
    const projects = data.projects || [];
    const grid = document.getElementById('projects-grid');
    if (!projects.length) {
      grid.innerHTML = '<div class="empty">No projects indexed yet. Use the <code>index</code> tool to add a project.</div>';
    } else {
      grid.innerHTML = projects.map(p => {
        const id    = p.ID   || p.id   || '';
        const name  = p.Name || p.name || '—';
        const path  = p.Path || p.path || '';
        const syms  = p.SymCount  || p.sym_count  || 0;
        const edges = p.EdgeCount || p.edge_count || 0;
        const files = p.FileCount || p.file_count || 0;
        const ts    = p.IndexedAt || p.indexed_at || '';
        const isEmpty  = syms === 0 && edges === 0;
        const ageHours = ts ? (Date.now() - new Date(ts)) / 3600000 : 0;
        const isStale  = !isEmpty && ageHours > STALE_HOURS;
        const cardCls  = isEmpty ? ' invalid' : isStale ? ' stale' : '';
        const pillCls  = isEmpty ? ' warn' : isStale ? ' stale-pill' : '';
        const statusMsg = isEmpty ? ' \u26a0 no data \u2014 may be a ghost project'
                        : isStale ? ' \u26a0 index is ' + Math.floor(ageHours) + 'h old \u2014 consider re-indexing' : '';
        return ` + "`" + `
        <div class="proj-card${cardCls}">
          <div class="proj-header">
            <div class="proj-name">${name}</div>
            <div class="proj-actions">
              <button class="proj-btn" title="Re-index this project" onclick="reindex(${JSON.stringify(id)}, this)">⟳ Re-index</button>
              <button class="proj-btn danger" title="Remove from index" onclick="deleteProject(${JSON.stringify(id)}, ${JSON.stringify(name)})">✕ Remove</button>
            </div>
          </div>
          <div class="proj-path${isEmpty||isStale?' missing':''}" title="${path}">${path}${statusMsg}</div>
          <div class="proj-stats">
            <div class="proj-stat"><div class="proj-stat-val">${fmt(files)}</div><div class="proj-stat-label">Files</div></div>
            <div class="proj-stat"><div class="proj-stat-val" style="color:var(--purple)">${fmt(syms)}</div><div class="proj-stat-label">Symbols</div></div>
            <div class="proj-stat"><div class="proj-stat-val" style="color:var(--green)">${fmt(edges)}</div><div class="proj-stat-label">Edges</div></div>
          </div>
          ${ts ? ` + "`" + `<div style="margin-top:10px" title="${new Date(ts).toLocaleString()}"><span class="pill${pillCls}">indexed ${timeAgo(ts)}</span></div>` + "`" + ` : ''}
        </div>` + "`" + `;
      }).join('');
    }
  } catch(e) {
    document.getElementById('projects-grid').innerHTML = ` + "`" + `<div class="error">Failed to load projects: ${e.message}</div>` + "`" + `;
  }

  document.getElementById('last-refresh').textContent = 'updated ' + new Date().toLocaleTimeString();
}

load();
setInterval(load, 30000); // auto-refresh every 30s
</script>
</body>
</html>`

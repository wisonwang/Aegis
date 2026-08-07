// === core.js ===
// Core: token/api helpers, boot, login, showApp, applyCapabilities, tab switch, clipboard
// (split from app.js by // ---- section markers; logic unchanged)

const TOKEN_KEY = 'datahub_token';
const ACTIVE_TAB_KEY = 'datahub_active_tab';
let token = localStorage.getItem(TOKEN_KEY) || '';
let me = null;
let pendingRequests = 0;
// Active workspace scope. Empty means "no explicit scope" -> the backend uses
// the caller's default (non-admin) or cross-workspace "*" (admin). Admins get
// an explicit value here after login (a concrete id, or "*" for all). The
// single source of truth for the X-Workspace-Id header (ADR-0007).
let currentWorkspace = '';

function setToken(t) { token = t; if (t) localStorage.setItem(TOKEN_KEY, t); else localStorage.removeItem(TOKEN_KEY); }

function setGlobalLoading(active) {
  const el = document.getElementById('topProgress');
  if (!el) return;
  el.classList.toggle('hidden', !active);
}

function toast(message, type = 'info', timeout = 2600) {
  const stack = document.getElementById('toastStack');
  if (!stack || !message) return;
  const item = document.createElement('div');
  item.className = `toast ${type}`;
  item.textContent = message;
  stack.appendChild(item);
  requestAnimationFrame(() => item.classList.add('show'));
  window.setTimeout(() => {
    item.classList.remove('show');
    window.setTimeout(() => item.remove(), 180);
  }, timeout);
}

async function withButtonBusy(btn, busyLabel, fn) {
  if (!btn) return fn();
  const previous = btn.textContent;
  btn.disabled = true;
  btn.classList.add('busy');
  if (busyLabel) btn.textContent = busyLabel;
  try {
    return await fn();
  } finally {
    btn.disabled = false;
    btn.classList.remove('busy');
    btn.textContent = previous;
  }
}

async function api(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) };
  if (token) headers['Authorization'] = 'Bearer ' + token;
  // Drive every admin read/write through the active workspace scope. Sent only
  // when set, so a non-admin (currentWorkspace stays "") never sends "*" and
  // trips the fail-closed 403 on the backend.
  if (currentWorkspace) headers['X-Workspace-Id'] = currentWorkspace;
  pendingRequests += 1;
  setGlobalLoading(true);
  try {
    const res = await fetch(path, { ...opts, headers });
    if (res.status === 401) {
      toast('登录已失效，请重新登录', 'warning');
      logout();
      throw new Error('未登录或登录已失效');
    }
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || ('HTTP ' + res.status));
    return data;
  } finally {
    pendingRequests = Math.max(0, pendingRequests - 1);
    setGlobalLoading(pendingRequests > 0);
  }
}

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function logout() { setToken(''); location.reload(); }

function visibleTabs() {
  return Array.from(document.querySelectorAll('.tab')).filter(t => t.style.display !== 'none');
}

function activateTab(tabName) {
  const target = document.querySelector(`.tab[data-tab="${tabName}"]`);
  if (!target || target.style.display === 'none') return false;
  document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
  document.querySelectorAll('.panel').forEach(x => x.classList.remove('active'));
  target.classList.add('active');
  const panel = document.getElementById('tab-' + tabName);
  if (panel) panel.classList.add('active');
  localStorage.setItem(ACTIVE_TAB_KEY, tabName);
  if (location.hash !== '#' + tabName) history.replaceState(null, '', '#' + tabName);
  return true;
}

function restoreActiveTab() {
  const preferred = (location.hash || '').replace(/^#/, '') || localStorage.getItem(ACTIVE_TAB_KEY) || 'query';
  if (activateTab(preferred)) return;
  const fallback = visibleTabs()[0];
  if (fallback) activateTab(fallback.dataset.tab);
}

// ---- workspace switcher (ADR-0007) ----
// The switcher is an admin affordance. Set the active workspace, persist it,
// keep the <select> in sync, and announce the change so every page can refresh
// its workspace-scoped lists. Selectors that don't depend on a specific table
// (query / dataset-create / approval / metrics datasource dropdowns) are
// refreshed here centrally; the per-page modules listen for the same event to
// refresh their own tables.
function setWorkspace(ws) {
  currentWorkspace = ws;
  if (ws) localStorage.setItem('datahub_ws', ws); else localStorage.removeItem('datahub_ws');
  const sel = document.getElementById('wsSwitcher');
  if (sel) sel.value = ws;
  window.dispatchEvent(new CustomEvent('workspace-changed', { detail: { workspace: ws } }));
}

async function loadWSForSwitcher() {
  const sel = document.getElementById('wsSwitcher');
  if (!sel) return;
  const isAdmin = !!(me && me.roles && me.roles.includes('admin'));
  if (!isAdmin) {
    // Non-admins are confined to their own workspace server-side; the switcher
    // would only let them pick a workspace that resolves to a 403.
    const wrap = sel.closest('.ws-switch');
    if (wrap) wrap.classList.add('hidden');
    currentWorkspace = '';
    return;
  }
  let saved = localStorage.getItem('datahub_ws') || '*';
  try {
    // No header yet (currentWorkspace is still ""), so admins see every
    // workspace to populate the picker.
    const data = await api('/admin/api/workspaces');
    const wss = data.workspaces || [];
    const valid = saved === '*' || wss.some(w => w.ID === saved);
    if (!valid) saved = '*';
    sel.innerHTML = '<option value="*">全部工作区</option>' +
      wss.map(w => `<option value="${esc(w.ID)}">${esc(w.Name)}</option>`).join('');
    sel.value = saved;
    currentWorkspace = saved;
  } catch (e) {
    // Workspace list unavailable -> default to the cross-workspace view; the
    // backend still resolves admins to "*" regardless.
    sel.innerHTML = '<option value="*">全部工作区</option>';
    sel.value = '*';
    currentWorkspace = '*';
  }
  sel.addEventListener('change', (e) => setWorkspace(e.target.value));
}

// Central refresh of the datasource dropdowns that are shared across pages and
// not owned by a single table module.
window.addEventListener('workspace-changed', () => {
  ['#qDs', '#dsDs', '#apDs', '#mtDs'].forEach(sel => loadDataSources(sel));
});

// ---- bootstrap ----
async function boot() {
  if (token) {
    try { me = await api('/api/v1/me'); showApp(); return; } catch (e) { /* fall through */ }
  }
  document.getElementById('login').classList.remove('hidden');
}

document.getElementById('loginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const username = document.getElementById('username').value;
  const password = document.getElementById('password').value;
  const errEl = document.getElementById('loginError');
  errEl.textContent = '';
  await withButtonBusy(e.submitter || document.querySelector('#loginForm button[type="submit"]'), '登录中...', async () => {
    try {
      const res = await fetch('/api/v1/login', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      });
      const data = await res.json();
      if (!data.token) throw new Error(data.error || '登录失败');
      setToken(data.token);
      me = data.user;
      toast('登录成功', 'success');
      showApp();
    } catch (err) { errEl.textContent = err.message; }
  });
});

async function showApp() {
  document.getElementById('login').classList.add('hidden');
  document.getElementById('app').classList.remove('hidden');
  document.getElementById('userbox').innerHTML =
    esc(me.display_name || me.username) + ' (' + esc(me.roles.join(',')) + ') <button class="sec" onclick="logout()">退出</button>';
  // Establish the active workspace scope BEFORE any scoped list loads, so the
  // datasource/datasets/governance reads below already carry X-Workspace-Id.
  await loadWSForSwitcher();
  await applyCapabilities();
  restoreActiveTab();
  loadDataSources('#qDs');
  loadDataSources('#gDs');
  loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole'); loadRolesInto('#gMaskRole');
  loadUsers(); loadRoles(); loadDataSourcesTable(); loadWorkspaces(); fillWSSelects(); loadUsersIntoWS();
  loadDataSources('#apDs'); loadRolesInto('#apRole'); loadMyApprovals();
  loadDataSourceMap(); loadDataSources('#dsDs'); loadDatasetsTable();
}

// Hide enterprise tabs/panels the current license does not entitle.
// The backend still enforces the gate (402); this is UX-only defense.
async function applyCapabilities() {
  let caps = [];
  try {
    const data = await fetch('/api/v1/capabilities').then(r => r.json());
    caps = data.capabilities || [];
  } catch (e) { caps = []; } // community default; nothing hidden
  document.querySelectorAll('.tab[data-cap]').forEach(t => {
    if (caps.includes(t.dataset.cap)) return;
    t.style.display = 'none';
    const panel = document.getElementById('tab-' + t.dataset.tab);
    if (panel) panel.style.display = 'none';
  });
}

// ---- tabs ----
document.querySelectorAll('.tab').forEach(t => {
  t.addEventListener('click', () => {
    activateTab(t.dataset.tab);
  });
});

// ---- datasources ----
async function loadDataSources(sel) {
  try {
    const data = await api('/api/v1/datasources');
    const el = document.querySelector(sel);
    el.innerHTML = data.datasources.map(d => `<option value="${d.id}">${esc(d.name)} (${esc(d.type)})</option>`).join('');
  } catch (e) { /* ignore */ }
}

// === core.js (clipboard) ===
// API docs copy-to-clipboard helper
// (split from app.js by // ---- section markers; logic unchanged)

// ---- API docs: copy code blocks to clipboard ----
document.addEventListener('click', (e) => {
  const btn = e.target.closest('.copy-btn');
  if (!btn) return;
  const code = btn.closest('.code').querySelector('code');
  if (!code) return;
  const text = code.innerText;
  const restore = () => { btn.textContent = '复制'; };
  const done = () => { btn.textContent = '已复制'; setTimeout(restore, 1200); };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done));
  } else {
    fallbackCopy(text, done);
  }
});
function fallbackCopy(text, done) {
  const ta = document.createElement('textarea');
  ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  try { document.execCommand('copy'); done(); } catch (e) { /* ignore */ }
  document.body.removeChild(ta);
}

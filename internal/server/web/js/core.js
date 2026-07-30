// === core.js ===
// Core: token/api helpers, boot, login, showApp, applyCapabilities, tab switch, clipboard
// (split from app.js by // ---- section markers; logic unchanged)

const TOKEN_KEY = 'datahub_token';
let token = localStorage.getItem(TOKEN_KEY) || '';
let me = null;

function setToken(t) { token = t; if (t) localStorage.setItem(TOKEN_KEY, t); else localStorage.removeItem(TOKEN_KEY); }

async function api(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers['Authorization'] = 'Bearer ' + token;
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401) { logout(); throw new Error('未登录或登录已失效'); }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || ('HTTP ' + res.status));
  return data;
}

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

function logout() { setToken(''); location.reload(); }

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
  try {
    const res = await fetch('/api/v1/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    });
    const data = await res.json();
    if (!data.token) throw new Error(data.error || '登录失败');
    setToken(data.token);
    me = data.user;
    showApp();
  } catch (err) { errEl.textContent = err.message; }
});

function showApp() {
  document.getElementById('login').classList.add('hidden');
  document.getElementById('app').classList.remove('hidden');
  document.getElementById('userbox').innerHTML =
    esc(me.display_name || me.username) + ' (' + esc(me.roles.join(',')) + ') <button class="sec" onclick="logout()">退出</button>';
  applyCapabilities();
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
    document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
    document.querySelectorAll('.panel').forEach(x => x.classList.remove('active'));
    t.classList.add('active');
    document.getElementById('tab-' + t.dataset.tab).classList.add('active');
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

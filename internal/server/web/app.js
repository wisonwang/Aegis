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
  loadDataSources('#qDs');
  loadDataSources('#gDs');
  loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole');
  loadUsers(); loadRoles(); loadDataSourcesTable();
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

// ---- query ----
document.getElementById('qTables').addEventListener('click', async () => {
  const id = document.getElementById('qDs').value;
  const box = document.getElementById('qResult');
  try {
    const data = await api('/api/v1/datasources/' + id + '/tables');
    box.innerHTML = '<pre>可访问表:\n' + data.tables.map(t => t.name + '  [' + t.ops.join(',') + ']').join('\n') + '</pre>';
  } catch (e) { box.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
});

document.getElementById('qRun').addEventListener('click', async () => {
  const id = document.getElementById('qDs').value;
  const sql = document.getElementById('qSql').value;
  const box = document.getElementById('qResult');
  box.innerHTML = '执行中...';
  try {
    const data = await api('/api/v1/query', { method: 'POST', body: JSON.stringify({ datasource: id, sql }) });
    let html = '<div>重写后的 SQL: <code>' + esc(data.rewritten_sql) + '</code></div>';
    if (data.rows) {
      html += renderRows(data.columns, data.rows);
    } else {
      html += '<div>影响行数: ' + data.affected_rows + '</div>';
    }
    box.innerHTML = html;
  } catch (e) { box.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
});

function renderRows(cols, rows) {
  if (!cols || cols.length === 0) return '<div>无结果列</div>';
  let h = '<table class="grid"><thead><tr>' + cols.map(c => '<th>' + esc(c) + '</th>').join('') + '</tr></thead><tbody>';
  for (const r of rows) {
    h += '<tr>' + cols.map(c => '<td>' + esc(r[c]) + '</td>').join('') + '</tr>';
  }
  h += '</tbody></table><div>共 ' + rows.length + ' 行</div>';
  return h;
}

// ---- users ----
async function loadUsers() {
  const data = await api('/admin/api/users');
  const t = document.getElementById('usersTable');
  t.innerHTML = '<thead><tr><th>用户名</th><th>显示名</th><th>状态</th><th>角色</th><th>属性</th><th>操作</th></tr></thead><tbody>' +
    data.users.map(u => `<tr>
      <td>${esc(u.username)}</td><td>${esc(u.display_name)}</td><td>${esc(u.status)}</td>
      <td>${u.roles.map(r => esc(r)).join(', ')}</td>
      <td>${esc(JSON.stringify(u.attributes))}</td>
      <td>
        <select data-uid="${u.id}" class="rolePick">${roleOptionsHTML(u.roles)}</select>
        <button class="sec" data-act="addrole" data-uid="${u.id}">加角色</button>
        <button class="danger" data-act="deluser" data-uid="${u.id}">删除</button>
      </td></tr>`).join('') + '</tbody>';
}

function roleOptionsHTML(exclude) {
  // populated lazily from cached roles
  return (window.__roles || []).map(r => `<option value="${esc(r.name)}">${esc(r.name)}</option>`).join('');
}

document.getElementById('usersTable').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  const uid = e.target.dataset.uid;
  try {
    if (act === 'deluser') {
      await api('/admin/api/users/' + uid, { method: 'DELETE' });
    } else if (act === 'addrole') {
      const role = e.target.parentElement.querySelector('.rolePick').value;
      await api('/admin/api/users/' + uid + '/roles', { method: 'POST', body: JSON.stringify({ role }) });
    }
    loadUsers();
  } catch (err) { alert(err.message); }
});

document.getElementById('uCreate').addEventListener('click', async () => {
  const body = {
    username: document.getElementById('uName').value,
    display_name: document.getElementById('uDisp').value,
    password: document.getElementById('uPass').value,
    attributes: JSON.parse(document.getElementById('uAttrs').value || '{}'),
  };
  try { await api('/admin/api/users', { method: 'POST', body: JSON.stringify(body) }); loadUsers(); }
  catch (e) { alert(e.message); }
});

// ---- roles ----
async function loadRoles() {
  const data = await api('/admin/api/roles');
  window.__roles = data.roles;
  const t = document.getElementById('rolesTable');
  t.innerHTML = '<thead><tr><th>名称</th><th>描述</th><th>操作</th></tr></thead><tbody>' +
    data.roles.map(r => `<tr><td>${esc(r.name)}</td><td>${esc(r.description)}</td>
      <td><button class="danger" data-act="delrole" data-id="${r.id}">删除</button></td></tr>`).join('') + '</tbody>';
}
document.getElementById('rolesTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'delrole') return;
  try { await api('/admin/api/roles/' + e.target.dataset.id, { method: 'DELETE' }); loadRoles(); loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole'); }
  catch (err) { alert(err.message); }
});
document.getElementById('rCreate').addEventListener('click', async () => {
  try { await api('/admin/api/roles', { method: 'POST', body: JSON.stringify({ name: document.getElementById('rName').value, description: document.getElementById('rDesc').value }) }); loadRoles(); loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole'); }
  catch (e) { alert(e.message); }
});
async function loadRolesInto(sel) {
  try {
    const data = await api('/admin/api/roles');
    window.__roles = data.roles;
    document.querySelector(sel).innerHTML = data.roles.map(r => `<option value="${esc(r.name)}">${esc(r.name)}</option>`).join('');
  } catch (e) { /* ignore */ }
}

// ---- datasources ----
async function loadDataSourcesTable() {
  const data = await api('/admin/api/datasources');
  const t = document.getElementById('dsTable');
  t.innerHTML = '<thead><tr><th>名称</th><th>类型</th><th>DSN</th><th>操作</th></tr></thead><tbody>' +
    data.datasources.map(d => `<tr><td>${esc(d.name)}</td><td>${esc(d.type)}</td><td>${esc(d.dsn)}</td>
      <td><button class="danger" data-act="delds" data-id="${d.id}">删除</button></td></tr>`).join('') + '</tbody>';
}
document.getElementById('dsTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'delds') return;
  try { await api('/admin/api/datasources/' + e.target.dataset.id, { method: 'DELETE' }); loadDataSourcesTable(); loadDataSources('#qDs'); loadDataSources('#gDs'); }
  catch (err) { alert(err.message); }
});
document.getElementById('dsCreate').addEventListener('click', async () => {
  const body = { name: document.getElementById('dsName').value, type: document.getElementById('dsType').value, dsn: document.getElementById('dsDSN').value };
  try { await api('/admin/api/datasources', { method: 'POST', body: JSON.stringify(body) }); loadDataSourcesTable(); loadDataSources('#qDs'); loadDataSources('#gDs'); }
  catch (e) { alert(e.message); }
});

// ---- governance ----
document.getElementById('gDs').addEventListener('change', async () => {
  const id = document.getElementById('gDs').value;
  const data = await api('/api/v1/datasources/' + id + '/tables');
  document.getElementById('gTable').innerHTML = data.tables.map(t => `<option value="${esc(t.name)}">${esc(t.name)}</option>`).join('');
});
document.getElementById('gLoad').addEventListener('click', loadGov);
async function loadGov() {
  const id = document.getElementById('gDs').value;
  const table = document.getElementById('gTable').value;
  const [p, pol] = await Promise.all([
    api('/admin/api/datasources/' + id + '/tables/' + table + '/permissions'),
    api('/admin/api/datasources/' + id + '/tables/' + table + '/policies'),
  ]);
  const pt = document.getElementById('permTable');
  pt.innerHTML = '<thead><tr><th>角色</th><th>操作</th><th>拒绝列</th><th>操作</th></tr></thead><tbody>' +
    p.permissions.map(x => `<tr><td>${esc(x.role)}</td><td>${esc(x.ops)}</td><td>${esc(JSON.stringify(x.denied_cols))}</td>
      <td><button class="danger" data-act="delperm" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
  const plt = document.getElementById('policyTable');
  plt.innerHTML = '<thead><tr><th>角色</th><th>谓词</th><th>优先级</th><th>操作</th></tr></thead><tbody>' +
    pol.policies.map(x => `<tr><td>${esc(x.role)}</td><td><code>${esc(x.predicate)}</code></td><td>${esc(x.priority)}</td>
      <td><button class="danger" data-act="delpol" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
}
document.getElementById('permTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'delperm') return;
  try { await api('/admin/api/datasources/' + document.getElementById('gDs').value + '/permissions/' + e.target.dataset.id, { method: 'DELETE' }); loadGov(); }
  catch (err) { alert(err.message); }
});
document.getElementById('policyTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'delpol') return;
  try { await api('/admin/api/datasources/' + document.getElementById('gDs').value + '/policies/' + e.target.dataset.id, { method: 'DELETE' }); loadGov(); }
  catch (err) { alert(err.message); }
});
document.getElementById('gAddPerm').addEventListener('click', async () => {
  const id = document.getElementById('gDs').value, table = document.getElementById('gTable').value;
  const body = { role: document.getElementById('gRole').value, ops: document.getElementById('gOps').value, denied_cols: JSON.parse(document.getElementById('gDenied').value || '[]') };
  try { await api('/admin/api/datasources/' + id + '/tables/' + table + '/permissions', { method: 'POST', body: JSON.stringify(body) }); loadGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('gAddPolicy').addEventListener('click', async () => {
  const id = document.getElementById('gDs').value, table = document.getElementById('gTable').value;
  const body = { role: document.getElementById('gPolicyRole').value, predicate: document.getElementById('gPred').value, priority: parseInt(document.getElementById('gPrio').value || '10', 10) };
  try { await api('/admin/api/datasources/' + id + '/tables/' + table + '/policies', { method: 'POST', body: JSON.stringify(body) }); loadGov(); }
  catch (e) { alert(e.message); }
});

// ---- audit logs ----
let auditOffset = 0;
const AUDIT_PAGE = 20;

async function loadAudit() {
  const params = new URLSearchParams();
  const u = document.getElementById('aUser').value.trim();
  const st = document.getElementById('aStatus').value;
  const ch = document.getElementById('aChannel').value;
  if (u) params.set('user', u);
  if (st) params.set('status', st);
  if (ch) params.set('channel', ch);
  params.set('limit', AUDIT_PAGE);
  params.set('offset', auditOffset);
  try {
    const [data, stats] = await Promise.all([
      api('/admin/api/audit?' + params.toString()),
      api('/admin/api/audit/stats'),
    ]);
    document.getElementById('auditStats').innerHTML =
      `<span class="stat">总计 <b>${stats.total}</b></span>` +
      `<span class="stat ok">成功 <b>${stats.ok || 0}</b></span>` +
      `<span class="stat denied">拒绝 <b>${stats.denied || 0}</b></span>` +
      `<span class="stat err">错误 <b>${stats.error || 0}</b></span>`;
    const t = document.getElementById('auditTable');
    t.innerHTML = '<thead><tr><th>时间</th><th>用户</th><th>渠道</th><th>数据源</th><th>SQL</th><th>重写后</th><th>状态</th><th>行数</th><th>耗时ms</th></tr></thead><tbody>' +
      data.logs.map(l => `<tr>
        <td>${esc(new Date(l.ts).toLocaleString())}</td>
        <td>${esc(l.username)}</td>
        <td>${esc(l.channel)}</td>
        <td>${esc(l.datasource)}</td>
        <td><code title="${esc(l.sql)}">${esc(l.sql.length > 60 ? l.sql.slice(0, 60) + '…' : l.sql)}</code></td>
        <td><code title="${esc(l.rewritten_sql)}">${esc((l.rewritten_sql || '').length > 60 ? l.rewritten_sql.slice(0, 60) + '…' : (l.rewritten_sql || ''))}</code></td>
        <td><span class="badge ${esc(l.status)}">${esc(l.status)}</span>${l.error ? ' <span class="error" title="' + esc(l.error) + '">!</span>' : ''}</td>
        <td>${esc(l.row_count)}</td>
        <td>${esc(l.duration_ms)}</td>
      </tr>`).join('') + '</tbody>';
    const page = Math.floor(auditOffset / AUDIT_PAGE) + 1;
    const pages = Math.max(1, Math.ceil(data.total / AUDIT_PAGE));
    document.getElementById('aPageInfo').textContent = `第 ${page}/${pages} 页 · 共 ${data.total} 条`;
  } catch (e) { alert(e.message); }
}
document.getElementById('aLoad').addEventListener('click', () => { auditOffset = 0; loadAudit(); });
document.getElementById('aPrev').addEventListener('click', () => { auditOffset = Math.max(0, auditOffset - AUDIT_PAGE); loadAudit(); });
document.getElementById('aNext').addEventListener('click', () => { auditOffset += AUDIT_PAGE; loadAudit(); });
document.querySelector('[data-tab="audit"]').addEventListener('click', () => { auditOffset = 0; loadAudit(); });

boot();

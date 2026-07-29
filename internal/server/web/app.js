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
  loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole');
  loadUsers(); loadRoles(); loadDataSourcesTable();
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

document.getElementById('qEstimate').addEventListener('click', async () => {
  const id = document.getElementById('qDs').value;
  const sql = document.getElementById('qSql').value;
  const box = document.getElementById('qEst');
  if (!sql.trim()) { box.innerHTML = '<div class="error">请先输入 SQL</div>'; return; }
  box.innerHTML = '评估中...';
  try {
    const data = await api('/api/v1/datasources/' + id + '/query/estimate', {
      method: 'POST', body: JSON.stringify({ datasource: id, sql }),
    });
    const rows = data.estimated_rows < 0 ? '未知' : data.estimated_rows.toLocaleString();
    let html = '<div>治理后 SQL: <code>' + esc(data.governed_sql) + '</code></div>';
    html += '<div>估算扫描行数: <b>' + esc(rows) + '</b> · 风险等级 ' + riskBadge(data.risk_level) + '</div>';
    html += '<div class="hint">表 [' + (data.tables || []).map(esc).join(', ') + '] · 最高敏感度 <b>' + esc(data.max_sensitivity || 'public') + '</b>' +
      (data.has_pii ? ' · <span class="badge error">含 PII</span>' : '') + '</div>';
    if (data.columns && data.columns.length) {
      html += '<div class="hint">敏感列: ' + data.columns.map(esc).join(', ') + '</div>';
    }
    if (data.warnings && data.warnings.length) {
      html += '<ul>' + data.warnings.map(w => '<li>' + esc(w) + '</li>').join('') + '</ul>';
    }
    if (data.note) html += '<div class="hint">' + esc(data.note) + '</div>';
    box.innerHTML = html;
  } catch (e) { box.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
});

function riskBadge(level) {
  const cls = { low: 'ok', medium: 'warning', high: 'error', unknown: 'denied' }[level] || 'denied';
  const label = { low: '低', medium: '中', high: '高', unknown: '未知' }[level] || level;
  return '<span class="badge ' + cls + '">' + label + '</span>';
}

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
  const sid = document.getElementById('aSession').value.trim();
  if (u) params.set('user', u);
  if (st) params.set('status', st);
  if (ch) params.set('channel', ch);
  if (sid) params.set('session_id', sid);
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
    t.innerHTML = '<thead><tr><th>时间</th><th>用户</th><th>渠道</th><th>数据源</th><th>会话</th><th>SQL</th><th>重写后</th><th>状态</th><th>行数</th><th>耗时ms</th></tr></thead><tbody>' +
      data.logs.map(l => `<tr>
        <td>${esc(new Date(l.ts).toLocaleString())}</td>
        <td>${esc(l.username)}</td>
        <td>${esc(l.channel)}</td>
        <td>${esc(l.datasource)}</td>
        <td><code title="${esc(l.session_id || '')}">${esc((l.session_id || '').slice(0, 8) || '—')}</code></td>
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

// ---- security alerts (anomaly detection) ----
let alertOffset = 0;
const ALERT_PAGE = 20;

async function loadAlerts() {
  const params = new URLSearchParams();
  const lv = document.getElementById('alLevel').value;
  const rs = document.getElementById('alResolved').value;
  if (lv) params.set('level', lv);
  if (rs) params.set('resolved', rs);
  params.set('limit', ALERT_PAGE);
  params.set('offset', alertOffset);
  try {
    const [data, stats] = await Promise.all([
      api('/admin/api/alerts?' + params.toString()),
      api('/admin/api/alerts/stats'),
    ]);
    document.getElementById('alertStats').innerHTML =
      `<span class="stat">总计 <b>${stats.total}</b></span>` +
      `<span class="stat denied">未处理 <b>${stats.open || 0}</b></span>` +
      `<span class="stat ok">已处理 <b>${stats.resolved || 0}</b></span>` +
      `<span class="stat err">严重 <b>${stats.critical || 0}</b></span>`;
    const t = document.getElementById('alertTable');
    t.innerHTML = '<thead><tr><th>时间</th><th>级别</th><th>规则</th><th>主体</th><th>渠道</th><th>说明</th><th>状态</th><th>操作</th></tr></thead><tbody>' +
      data.alerts.map(a => `<tr>
        <td>${esc(new Date(a.ts).toLocaleString())}</td>
        <td><span class="badge ${esc(a.level)}">${esc(a.level)}</span></td>
        <td><code>${esc(a.rule)}</code></td>
        <td>${esc(a.principal)}</td>
        <td>${esc(a.channel)}</td>
        <td>${esc(a.detail)}</td>
        <td>${a.resolved ? '<span class="badge ok">已处理</span>' : '<span class="badge denied">未处理</span>'}</td>
        <td>${a.resolved ? '' : `<button class="sec" data-act="resolve" data-id="${a.id}">标记处理</button>`}</td>
      </tr>`).join('') + '</tbody>';
  } catch (e) { alert(e.message); }
}
document.getElementById('alertTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'resolve') return;
  try { await api('/admin/api/alerts/' + e.target.dataset.id + '/resolve', { method: 'POST' }); loadAlerts(); }
  catch (err) { alert(err.message); }
});
document.getElementById('alLoad').addEventListener('click', () => { alertOffset = 0; loadAlerts(); });
document.querySelector('[data-tab="alerts"]').addEventListener('click', () => { alertOffset = 0; loadAlerts(); });

// ---- access approval workflow ----
function approvalBadge(s) {
  const cls = { pending: 'warning', approved: 'ok', rejected: 'error', revoked: 'critical' }[s] || 'warning';
  const label = { pending: '待审批', approved: '已批准', rejected: '已拒绝', revoked: '已撤回' }[s] || s;
  return `<span class="badge ${cls}">${esc(label)}</span>`;
}

async function loadMyApprovals() {
  try {
    const data = await api('/api/v1/me/approvals');
    const t = document.getElementById('apMine');
    if (!data.approvals || data.approvals.length === 0) {
      t.innerHTML = '<thead><tr><th>状态</th></tr></thead><tbody><tr><td>暂无申请</td></tr></tbody>';
      return;
    }
    t.innerHTML = '<thead><tr><th>数据源</th><th>表</th><th>角色</th><th>操作</th><th>理由</th><th>状态</th><th>审批人</th></tr></thead><tbody>' +
      data.approvals.map(a => `<tr>
        <td>${esc(a.datasource_name)}</td><td>${esc(a.table_name)}</td><td>${esc(a.role_name)}</td>
        <td>${esc(a.ops)}</td><td>${esc(a.justification)}</td>
        <td>${approvalBadge(a.status)}</td><td>${esc(a.approver_name || '—')}</td></tr>`).join('') + '</tbody>';
  } catch (e) { /* ignore */ }
}

async function loadAllApprovals() {
  const params = new URLSearchParams();
  const f = document.getElementById('apFilter').value;
  if (f) params.set('status', f);
  try {
    const data = await api('/admin/api/approvals?' + params.toString());
    const t = document.getElementById('apAll');
    if (!data.approvals || data.approvals.length === 0) {
      t.innerHTML = '<thead><tr><th>状态</th></tr></thead><tbody><tr><td>暂无申请</td></tr></tbody>';
      return;
    }
    t.innerHTML = '<thead><tr><th>申请人</th><th>数据源</th><th>表</th><th>角色</th><th>操作</th><th>理由</th><th>状态</th><th>审批人</th><th>操作</th></tr></thead><tbody>' +
      data.approvals.map(a => `<tr>
        <td>${esc(a.applicant_name)}</td><td>${esc(a.datasource_name)}</td><td>${esc(a.table_name)}</td>
        <td>${esc(a.role_name)}</td><td>${esc(a.ops)}</td><td>${esc(a.justification)}</td>
        <td>${approvalBadge(a.status)}</td><td>${esc(a.approver_name || '—')}</td>
        <td>${approvalActions(a)}</td></tr>`).join('') + '</tbody>';
  } catch (e) {
    document.getElementById('apAll').innerHTML = '<thead><tr><th>审批台</th></tr></thead><tbody><tr><td>需要管理员权限查看审批台</td></tr></tbody>';
  }
}

function approvalActions(a) {
  if (a.status === 'pending') {
    return `<button class="sec" data-act="approve" data-id="${a.id}">通过</button> <button class="danger" data-act="reject" data-id="${a.id}">拒绝</button>`;
  }
  if (a.status === 'approved') {
    return `<button class="danger" data-act="revoke" data-id="${a.id}">撤回</button>`;
  }
  return '';
}

document.getElementById('apSubmit').addEventListener('click', async () => {
  const msg = document.getElementById('apMsg');
  msg.textContent = '';
  const body = {
    datasource_id: document.getElementById('apDs').value,
    table_name: document.getElementById('apTable').value.trim(),
    role: document.getElementById('apRole').value,
    ops: document.getElementById('apOps').value.trim(),
    justification: document.getElementById('apJust').value.trim(),
  };
  if (!body.datasource_id || !body.table_name || !body.role || !body.ops) {
    msg.textContent = '请填写数据源、表名、角色与操作';
    return;
  }
  try {
    await api('/admin/api/approvals', { method: 'POST', body: JSON.stringify(body) });
    msg.textContent = '申请已提交，等待管理员审批';
    document.getElementById('apTable').value = '';
    document.getElementById('apJust').value = '';
    loadMyApprovals();
    loadAllApprovals();
  } catch (e) { msg.textContent = e.message; }
});

document.getElementById('apAll').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  const id = e.target.dataset.id;
  try {
    if (act === 'approve') await api('/admin/api/approvals/' + id + '/approve', { method: 'POST' });
    else if (act === 'reject') await api('/admin/api/approvals/' + id + '/reject', { method: 'POST' });
    else if (act === 'revoke') await api('/admin/api/approvals/' + id + '/revoke', { method: 'POST' });
    loadAllApprovals();
    loadMyApprovals();
  } catch (err) { alert(err.message); }
});

document.getElementById('apLoad').addEventListener('click', loadAllApprovals);
document.querySelector('[data-tab="approvals"]').addEventListener('click', () => { loadMyApprovals(); loadAllApprovals(); });

// ---- NL2SQL gateway ----
function loadNLDs() { loadDataSources('#nlDs'); }

document.getElementById('nlCatalog').addEventListener('click', async () => {
  const id = document.getElementById('nlDs').value;
  const box = document.getElementById('nlGen');
  try {
    const data = await api('/api/v1/datasources/' + id + '/catalog');
    let html = '<div class="hint">模型可见的受治理 Schema（脱敏列已标注）：</div>';
    for (const t of (data.tables || [])) {
      html += '<div><b>' + esc(t.name) + '</b>' + (t.description ? ' — ' + esc(t.description) : '') + '</div><ul>';
      for (const c of (t.columns || [])) {
        html += '<li><code>' + esc(c.name) + '</code> ' + esc(c.type) +
          (c.description ? ' · ' + esc(c.description) : '') +
          (c.masked ? ' · [脱敏:' + esc(c.masked) + ']' : '') + '</li>';
      }
      html += '</ul>';
    }
    box.innerHTML = html;
  } catch (e) { box.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
});

document.getElementById('nlRun').addEventListener('click', async () => {
  const id = document.getElementById('nlDs').value;
  const question = document.getElementById('nlQ').value.trim();
  const hint = document.getElementById('nlHint').value.trim();
  const genBox = document.getElementById('nlGen');
  const resBox = document.getElementById('nlResult');
  if (!question && !hint) { genBox.innerHTML = '<div class="error">请输入问题或 sql_hint</div>'; return; }
  genBox.innerHTML = '生成中...'; resBox.innerHTML = '';
  try {
    const data = await api('/api/v1/datasources/' + id + '/nl2sql', {
      method: 'POST',
      body: JSON.stringify({ datasource: id, question, sql_hint: hint }),
    });
    let html = '<div>生成 SQL: <code>' + esc(data.generated_sql) + '</code></div>';
    if (data.explanation) html += '<div class="hint">说明: ' + esc(data.explanation) + '</div>';
    if (data.query_result && data.query_result.rows) {
      html += renderRows(data.query_result.columns, data.query_result.rows);
    } else if (data.query_result) {
      html += '<div>影响行数: ' + (data.query_result.affected_rows || 0) + '</div>';
    }
    genBox.innerHTML = html;
  } catch (e) { genBox.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
});

document.querySelector('[data-tab="nl2sql"]').addEventListener('click', loadNLDs);

// ---- Semantic metric layer ----
function loadMTDs() { loadDataSources('#mtDs'); loadMTMetrics(); }
let mtMetrics = [];

async function loadMTMetrics() {
  const id = document.getElementById('mtDs').value;
  const sel = document.getElementById('mtName');
  sel.innerHTML = '<option value="">（选择指标）</option>';
  if (!id) return;
  try {
    const data = await api('/api/v1/datasources/' + id + '/metrics');
    mtMetrics = data.metrics || [];
    for (const m of mtMetrics) {
      const o = document.createElement('option');
      o.value = m.name;
      o.textContent = m.name + (m.description ? ' — ' + m.description : '') + (m.unit ? ' (' + m.unit + ')' : '');
      sel.appendChild(o);
    }
  } catch (e) { sel.innerHTML = '<option value="">加载失败: ' + esc(e.message) + '</option>'; }
}

document.getElementById('mtLoad').addEventListener('click', loadMTMetrics);

document.getElementById('mtName').addEventListener('change', () => {
  const box = document.getElementById('mtParams');
  const name = document.getElementById('mtName').value;
  const m = mtMetrics.find(x => x.name === name);
  if (!m) { box.innerHTML = ''; return; }
  let html = '<div class="hint">参数：</div>';
  for (const p of (m.params || [])) {
    const req = p.required ? ' <span class="badge error">必填</span>' : '';
    const en = (p.enum && p.enum.length) ? ' placeholder="' + p.enum.join('|') + '"' : '';
    html += '<label>' + esc(p.name) + ' <small>' + esc(p.type) + '</small>' + req +
      (p.description ? ' · ' + esc(p.description) : '') +
      '<input data-param="' + esc(p.name) + '"' + en + '></label>';
  }
  box.innerHTML = html || '<div class="hint">该指标无参数</div>';
});

document.getElementById('mtRun').addEventListener('click', async () => {
  const id = document.getElementById('mtDs').value;
  const name = document.getElementById('mtName').value;
  const linBox = document.getElementById('mtLineage');
  const resBox = document.getElementById('mtResult');
  if (!name) { resBox.innerHTML = '<div class="error">请先选择指标</div>'; return; }
  const params = {};
  document.querySelectorAll('#mtParams [data-param]').forEach(inp => {
    const v = inp.value.trim();
    if (v === '') return;
    const p = (mtMetrics.find(x => x.name === name).params || []).find(q => q.name === inp.dataset.param);
    params[inp.dataset.param] = p && p.type === 'number' ? Number(v) : v;
  });
  linBox.innerHTML = '运行中...'; resBox.innerHTML = '';
  try {
    const data = await api('/api/v1/datasources/' + id + '/metrics/' + encodeURIComponent(name) + '/run', {
      method: 'POST',
      body: JSON.stringify({ params }),
    });
    let html = '<div>执行 SQL: <code>' + esc(data.sql) + '</code></div>';
    if (data.lineage) {
      const l = data.lineage;
      html += '<div class="hint">血缘：表 [' + (l.tables || []).map(esc).join(', ') + '] · 敏感度 <b>' + esc(l.max_sensitivity || 'public') + '</b>' +
        (l.has_pii ? ' · <span class="badge error">含 PII</span>' : '') +
        (l.columns && l.columns.length ? ' · 敏感列: ' + l.columns.map(esc).join(', ') : '') + '</div>';
    }
    if (data.query_result && data.query_result.rows) {
      html += renderRows(data.query_result.columns, data.query_result.rows);
    } else if (data.query_result) {
      html += '<div>影响行数: ' + (data.query_result.affected_rows || 0) + '</div>';
    }
    linBox.innerHTML = html;
  } catch (e) { linBox.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
});

document.querySelector('[data-tab="metrics"]').addEventListener('click', loadMTDs);

// ---- dataset management (data products) ----
async function loadDataSourceMap() {
  try {
    const data = await api('/api/v1/datasources');
    window.__dsMap = {};
    for (const d of (data.datasources || [])) window.__dsMap[d.id] = d.name;
  } catch (e) { window.__dsMap = {}; }
}
function dsName(id) { return (window.__dsMap && window.__dsMap[id]) || id || '—'; }

async function loadDatasetsTable() {
  const t = document.getElementById('datasetTable');
  try {
    const data = await api('/admin/api/datasets');
    const sets = data.datasets || [];
    if (sets.length === 0) {
      t.innerHTML = '<thead><tr><th>数据集</th></tr></thead><tbody><tr><td>暂无数据集</td></tr></tbody>';
      return;
    }
    t.innerHTML = '<thead><tr><th>名称</th><th>显示名</th><th>数据源</th><th>状态</th><th>定义</th><th>操作</th></tr></thead><tbody>' +
      sets.map(d => `<tr>
        <td><code>${esc(d.name)}</code></td>
        <td>${esc(d.display_name || '')}</td>
        <td>${esc(dsName(d.datasource_id))}</td>
        <td><span class="badge ${d.status === 'published' ? 'ok' : 'warning'}">${d.status === 'published' ? '已发布' : '草稿'}</span></td>
        <td><code title="${esc(d.definition || '')}">${esc((d.definition || '').length > 40 ? d.definition.slice(0, 40) + '…' : (d.definition || ''))}</code></td>
        <td>
          <button class="sec" data-act="dsgov" data-id="${d.id}" data-name="${esc(d.name)}">治理</button>
          <button class="sec" data-act="dspub" data-id="${d.id}" data-status="${esc(d.status)}">${d.status === 'published' ? '取消发布' : '发布'}</button>
          <button class="sec" data-act="dsedit" data-id="${d.id}">编辑</button>
          <button class="danger" data-act="dsdel" data-id="${d.id}" data-name="${esc(d.name)}">删除</button>
        </td></tr>`).join('') + '</tbody>';
  } catch (e) {
    t.innerHTML = '<thead><tr><th>数据集</th></tr></thead><tbody><tr><td class="error">' + esc(e.message) + '</td></tr></tbody>';
  }
}

function dsResetForm() {
  window.__dsEditId = null;
  document.getElementById('datasetName').value = '';
  document.getElementById('datasetName').disabled = false;
  document.getElementById('dsDisp').value = '';
  document.getElementById('dsDef').value = '';
  document.getElementById('dsFields').value = '';
  document.getElementById('dsStatus').value = 'draft';
  document.getElementById('dsSave').textContent = '创建数据集';
  document.getElementById('dsCancel').classList.add('hidden');
  document.getElementById('dsMsg').textContent = '';
}

function isValidJSON(s) { try { JSON.parse(s); return true; } catch { return false; } }

document.getElementById('dsCancel').addEventListener('click', dsResetForm);

document.getElementById('dsSave').addEventListener('click', async () => {
  const msg = document.getElementById('dsMsg');
  msg.textContent = '';
  const name = document.getElementById('datasetName').value.trim();
  const dsId = document.getElementById('dsDs').value;
  const def = document.getElementById('dsDef').value;
  const fields = document.getElementById('dsFields').value.trim();
  const status = document.getElementById('dsStatus').value;
  const editId = window.__dsEditId;
  if (!editId && !name) { msg.textContent = '请填写名称'; return; }
  if (!dsId) { msg.textContent = '请选择数据源'; return; }
  if (!editId && !def.trim()) { msg.textContent = '请填写定义 SQL'; return; }
  if (fields && !isValidJSON(fields)) { msg.textContent = '字段契约需为合法 JSON'; return; }
  const body = editId
    ? { display_name: document.getElementById('dsDisp').value, definition: def, status, fields }
    : { name, display_name: document.getElementById('dsDisp').value, datasource_id: dsId, definition: def, status, fields };
  try {
    if (editId) await api('/admin/api/datasets/' + editId, { method: 'PUT', body: JSON.stringify(body) });
    else await api('/admin/api/datasets', { method: 'POST', body: JSON.stringify(body) });
    dsResetForm();
    loadDatasetsTable();
  } catch (e) { msg.textContent = e.message; }
});

document.getElementById('datasetTable').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  const id = e.target.dataset.id;
  const name = e.target.dataset.name || id;
  try {
    if (act === 'dsdel') {
      if (!confirm('删除数据集「' + name + '」将级联删除其下所有表权限 / 行策略 / 列脱敏 / 业务语义。确认？')) return;
      await api('/admin/api/datasets/' + id, { method: 'DELETE' });
      if (window.__dsGovId === id) { window.__dsGovId = null; document.getElementById('dsGov').classList.add('hidden'); }
      loadDatasetsTable();
    } else if (act === 'dspub') {
      const st = e.target.dataset.status;
      await api('/admin/api/datasets/' + id + (st === 'published' ? '/unpublish' : '/publish'), { method: 'POST' });
      loadDatasetsTable();
    } else if (act === 'dsedit') {
      const data = await api('/admin/api/datasets/' + id);
      window.__dsEditId = id;
      document.getElementById('datasetName').value = data.name;
      document.getElementById('datasetName').disabled = true;
      document.getElementById('dsDisp').value = data.display_name || '';
      document.getElementById('dsDs').value = data.datasource_id || '';
      document.getElementById('dsDef').value = data.definition || '';
      document.getElementById('dsFields').value = (data.fields && data.fields !== '[]') ? data.fields : '';
      document.getElementById('dsStatus').value = data.status || 'draft';
      document.getElementById('dsSave').textContent = '保存修改';
      document.getElementById('dsCancel').classList.remove('hidden');
      document.getElementById('dsMsg').textContent = '编辑模式：名称不可改';
    } else if (act === 'dsgov') {
      window.__dsGovId = id;
      document.getElementById('dsGovName').textContent = name;
      document.getElementById('dsGov').classList.remove('hidden');
      loadRolesInto('#dsgRole'); loadRolesInto('#dsgPolicyRole'); loadRolesInto('#dsgMaskRole');
      loadDatasetGov();
    }
  } catch (err) { alert(err.message); }
});

// ---- dataset governance (keyed on dataset name) ----
async function loadDatasetGov() {
  const id = window.__dsGovId;
  if (!id) return;
  try {
    const [p, pol, m, s] = await Promise.all([
      api('/admin/api/datasets/' + id + '/permissions'),
      api('/admin/api/datasets/' + id + '/policies'),
      api('/admin/api/datasets/' + id + '/masks'),
      api('/admin/api/datasets/' + id + '/semantics'),
    ]);
    const pt = document.getElementById('dsgPermTable');
    pt.innerHTML = '<thead><tr><th>角色</th><th>操作</th><th>允许列</th><th>拒绝列</th><th>操作</th></tr></thead><tbody>' +
      (p.permissions || []).map(x => `<tr><td>${esc(x.role)}</td><td>${esc(x.ops)}</td>
        <td>${esc(JSON.stringify(x.allowed_cols))}</td><td>${esc(JSON.stringify(x.denied_cols))}</td>
        <td><button class="danger" data-act="dsgdelperm" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
    const plt = document.getElementById('dsgPolicyTable');
    plt.innerHTML = '<thead><tr><th>角色</th><th>谓词</th><th>优先级</th><th>操作</th></tr></thead><tbody>' +
      (pol.policies || []).map(x => `<tr><td>${esc(x.role)}</td><td><code>${esc(x.predicate)}</code></td><td>${esc(x.priority)}</td>
        <td><button class="danger" data-act="dsgdelpol" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
    const mt = document.getElementById('dsgMaskTable');
    mt.innerHTML = '<thead><tr><th>角色</th><th>列</th><th>策略</th><th>操作</th></tr></thead><tbody>' +
      (m.masks || []).map(x => `<tr><td>${esc(x.role)}</td><td>${esc(x.column_name)}</td><td>${esc(x.strategy)}</td>
        <td><button class="danger" data-act="dsgdelmask" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
    const st = document.getElementById('dsgSemTable');
    st.innerHTML = '<thead><tr><th>列</th><th>描述</th><th>同义词</th><th>示例</th><th>操作</th></tr></thead><tbody>' +
      (s.semantics || []).map(x => `<tr><td>${esc(x.column_name || '表级')}</td><td>${esc(x.description || '')}</td>
        <td>${esc(x.synonyms || '')}</td><td>${esc(x.examples || '')}</td>
        <td><button class="danger" data-act="dsgdelsem" data-id="${esc(x.id)}">删除</button></td></tr>`).join('') + '</tbody>';
  } catch (e) { alert(e.message); }
}

document.getElementById('dsgAddPerm').addEventListener('click', async () => {
  const id = window.__dsGovId; if (!id) return;
  const body = {
    role: document.getElementById('dsgRole').value,
    ops: document.getElementById('dsgOps').value,
    allowed_cols: JSON.parse(document.getElementById('dsgAllowed').value || '[]'),
    denied_cols: JSON.parse(document.getElementById('dsgDenied').value || '[]'),
  };
  try { await api('/admin/api/datasets/' + id + '/permissions', { method: 'POST', body: JSON.stringify(body) }); loadDatasetGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('dsgAddPolicy').addEventListener('click', async () => {
  const id = window.__dsGovId; if (!id) return;
  const body = {
    role: document.getElementById('dsgPolicyRole').value,
    predicate: document.getElementById('dsgPred').value,
    priority: parseInt(document.getElementById('dsgPrio').value || '10', 10),
  };
  try { await api('/admin/api/datasets/' + id + '/policies', { method: 'POST', body: JSON.stringify(body) }); loadDatasetGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('dsgAddMask').addEventListener('click', async () => {
  const id = window.__dsGovId; if (!id) return;
  const body = {
    role: document.getElementById('dsgMaskRole').value,
    column: document.getElementById('dsgMaskCol').value,
    strategy: document.getElementById('dsgMaskStrat').value,
    keep: 0,
  };
  try { await api('/admin/api/datasets/' + id + '/masks', { method: 'POST', body: JSON.stringify(body) }); loadDatasetGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('dsgAddSem').addEventListener('click', async () => {
  const id = window.__dsGovId; if (!id) return;
  const body = {
    column_name: document.getElementById('dsgSemCol').value.trim(),
    description: document.getElementById('dsgSemDesc').value.trim(),
    synonyms: JSON.parse(document.getElementById('dsgSemSyn').value || '[]'),
    examples: JSON.parse(document.getElementById('dsgSemEx').value || '[]'),
  };
  try { await api('/admin/api/datasets/' + id + '/semantics', { method: 'POST', body: JSON.stringify(body) }); loadDatasetGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('dsGov').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  const id = window.__dsGovId; if (!id) return;
  try {
    if (act === 'dsgdelperm') await api('/admin/api/datasets/' + id + '/permissions/' + e.target.dataset.id, { method: 'DELETE' });
    else if (act === 'dsgdelpol') await api('/admin/api/datasets/' + id + '/policies/' + e.target.dataset.id, { method: 'DELETE' });
    else if (act === 'dsgdelmask') await api('/admin/api/datasets/' + id + '/masks/' + e.target.dataset.id, { method: 'DELETE' });
    else if (act === 'dsgdelsem') await api('/admin/api/datasets/' + id + '/semantics/' + e.target.dataset.id, { method: 'DELETE' });
    loadDatasetGov();
  } catch (err) { alert(err.message); }
});

document.querySelector('[data-tab="datasets"]').addEventListener('click', () => {
  loadDataSourceMap(); loadDataSources('#dsDs'); loadDatasetsTable();
});

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

boot();

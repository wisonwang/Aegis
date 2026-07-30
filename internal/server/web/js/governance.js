// === governance.js ===
// Base-table governance: permissions, row policies, masks, classification, semantics
// (split from app.js by // ---- section markers; logic unchanged)

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
  const [p, pol, mk, cf, sm] = await Promise.all([
    api('/admin/api/datasources/' + id + '/tables/' + table + '/permissions'),
    api('/admin/api/datasources/' + id + '/tables/' + table + '/policies'),
    api('/admin/api/datasources/' + id + '/masks?table=' + encodeURIComponent(table)),
    api('/admin/api/datasources/' + id + '/classifications?table=' + encodeURIComponent(table)),
    api('/admin/api/datasources/' + id + '/semantics?table=' + encodeURIComponent(table)),
  ]);
  const pt = document.getElementById('permTable');
  pt.innerHTML = '<thead><tr><th>角色</th><th>操作</th><th>拒绝列</th><th>操作</th></tr></thead><tbody>' +
    p.permissions.map(x => `<tr><td>${esc(x.role)}</td><td>${esc(x.ops)}</td><td>${esc(JSON.stringify(x.denied_cols))}</td>
      <td><button class="danger" data-act="delperm" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
  const plt = document.getElementById('policyTable');
  plt.innerHTML = '<thead><tr><th>角色</th><th>谓词</th><th>优先级</th><th>操作</th></tr></thead><tbody>' +
    pol.policies.map(x => `<tr><td>${esc(x.role)}</td><td><code>${esc(x.predicate)}</code></td><td>${esc(x.priority)}</td>
      <td><button class="danger" data-act="delpol" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
  document.getElementById('maskTable').innerHTML = '<thead><tr><th>角色</th><th>列</th><th>策略</th><th>keep</th><th>操作</th></tr></thead><tbody>' +
    (mk.masks || []).map(x => `<tr><td>${esc(x.role)}</td><td>${esc(x.column_name)}</td><td>${esc(x.strategy)}</td><td>${esc(x.keep ?? '')}</td>
      <td><button class="danger" data-act="gdelmask" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
  document.getElementById('clsTable').innerHTML = '<thead><tr><th>列</th><th>级别</th><th>标签</th><th>操作</th></tr></thead><tbody>' +
    (cf.classifications || []).map(x => `<tr><td>${esc(x.column_name || '表级')}</td><td>${esc(x.level)}</td><td>${esc(x.tags || '')}</td>
      <td><button class="danger" data-act="gdelcls" data-id="${x.id}">删除</button></td></tr>`).join('') + '</tbody>';
  document.getElementById('semTable').innerHTML = '<thead><tr><th>列</th><th>描述</th><th>同义词</th><th>示例</th><th>操作</th></tr></thead><tbody>' +
    (sm.semantics || []).map(x => `<tr><td>${esc(x.column_name || '表级')}</td><td>${esc(x.description || '')}</td>
      <td>${esc(x.synonyms || '')}</td><td>${esc(x.examples || '')}</td>
      <td><button class="danger" data-act="gdelsem" data-id="${esc(x.id)}">删除</button></td></tr>`).join('') + '</tbody>';
}

// ---- gov: column masks / classifications / semantics (base tables) ----
function govDSTable() {
  return { id: document.getElementById('gDs').value, table: document.getElementById('gTable').value };
}
document.getElementById('gAddMask').addEventListener('click', async () => {
  const { id, table } = govDSTable();
  const body = {
    role: document.getElementById('gMaskRole').value,
    table,
    column: document.getElementById('gMaskCol').value,
    strategy: document.getElementById('gMaskStrat').value,
    keep: parseInt(document.getElementById('gMaskKeep').value || '0', 10) || 0,
  };
  try { await api('/admin/api/datasources/' + id + '/masks', { method: 'POST', body: JSON.stringify(body) }); loadGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('gAddCls').addEventListener('click', async () => {
  const { id, table } = govDSTable();
  let tags = [];
  const raw = document.getElementById('gClsTags').value.trim();
  if (raw) { try { tags = JSON.parse(raw); } catch { alert('标签需为合法 JSON 数组'); return; } }
  const body = {
    table_name: table,
    column_name: document.getElementById('gClsCol').value.trim(),
    level: document.getElementById('gClsLevel').value,
    tags,
  };
  try { await api('/admin/api/datasources/' + id + '/classifications', { method: 'POST', body: JSON.stringify(body) }); loadGov(); }
  catch (e) { alert(e.message); }
});
document.getElementById('gAddSem').addEventListener('click', async () => {
  const { id, table } = govDSTable();
  const parseArr = (v) => { v = v.trim(); if (!v) return []; try { return JSON.parse(v); } catch { return null; } };
  const syn = parseArr(document.getElementById('gSemSyn').value);
  const ex = parseArr(document.getElementById('gSemEx').value);
  if (syn === null || ex === null) { alert('同义词/示例需为合法 JSON 数组'); return; }
  const body = {
    table_name: table,
    column_name: document.getElementById('gSemCol').value.trim(),
    description: document.getElementById('gSemDesc').value,
    synonyms: syn,
    examples: ex,
  };
  try { await api('/admin/api/datasources/' + id + '/semantics', { method: 'POST', body: JSON.stringify(body) }); loadGov(); }
  catch (e) { alert(e.message); }
});
['maskTable', 'clsTable', 'semTable'].forEach(tid => {
  document.getElementById(tid).addEventListener('click', async (e) => {
    const act = e.target.dataset.act;
    if (!act || !act.startsWith('gdel')) return;
    const { id } = govDSTable();
    const kind = { gdelmask: 'masks', gdelcls: 'classifications', gdelsem: 'semantics' }[act];
    if (!kind) return;
    try { await api('/admin/api/datasources/' + id + '/' + kind + '/' + encodeURIComponent(e.target.dataset.id), { method: 'DELETE' }); loadGov(); }
    catch (err) { alert(err.message); }
  });
});
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


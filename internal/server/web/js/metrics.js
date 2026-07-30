// === metrics.js ===
// Semantic metric layer + metric definitions admin
// (split from app.js by // ---- section markers; logic unchanged)

// ---- Semantic metric layer ----
function loadMTDs() { loadDataSources('#mtDs'); loadMTMetrics(); loadMTAdmin(); }
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

document.getElementById('mtLoad').addEventListener('click', () => { loadMTMetrics(); loadMTAdmin(); });

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

// ---- metric definitions admin (CRUD) ----
async function loadMTAdmin() {
  const id = document.getElementById('mtDs').value;
  const t = document.getElementById('mtAdminTable');
  if (!id) { t.innerHTML = '<thead><tr><th>选择数据源</th></tr></thead><tbody><tr><td>请先选择数据源</td></tr></tbody>'; return; }
  try {
    const data = await api('/admin/api/datasources/' + id + '/metrics');
    const list = data.metrics || [];
    t.innerHTML = '<thead><tr><th>名称</th><th>描述</th><th>单位</th><th>参数</th><th>操作</th></tr></thead><tbody>' +
      (list.length ? list.map(m => `<tr>
        <td><code>${esc(m.name)}</code></td>
        <td>${esc(m.description || '')}</td>
        <td>${esc(m.unit || '')}</td>
        <td>${esc((m.params || []).map(p => p.name + ':' + p.type).join(', '))}</td>
        <td>
          <button class="sec" data-act="mtedit" data-id="${esc(m.id)}">编辑</button>
          <button class="danger" data-act="mtdel" data-id="${esc(m.id)}">删除</button>
        </td></tr>`).join('') : '<tr><td colspan="5">暂无指标</td></tr>') + '</tbody>';
  } catch (e) { t.innerHTML = '<thead><tr><th>错误</th></tr></thead><tbody><tr><td class="error">' + esc(e.message) + '</td></tr></tbody>'; }
}
let mtEditId = null;
function resetMtDefForm() {
  mtEditId = null;
  document.getElementById('mtDefName').value = '';
  document.getElementById('mtDefName').disabled = false;
  document.getElementById('mtDefDesc').value = '';
  document.getElementById('mtDefUnit').value = '';
  document.getElementById('mtDefSQL').value = '';
  document.getElementById('mtDefParams').value = '';
  document.getElementById('mtDefSave').textContent = '保存指标';
  document.getElementById('mtDefCancel').classList.add('hidden');
  document.getElementById('mtDefMsg').textContent = '';
}
document.getElementById('mtDefCancel').addEventListener('click', resetMtDefForm);
document.getElementById('mtAdminTable').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  const mid = e.target.dataset.id;
  if (act === 'mtdel') {
    const id = document.getElementById('mtDs').value;
    try { await api('/admin/api/datasources/' + id + '/metrics/' + mid, { method: 'DELETE' }); loadMTAdmin(); loadMTMetrics(); }
    catch (err) { alert(err.message); }
  } else if (act === 'mtedit') {
    const m = mtMetrics.find(x => x.id === mid);
    if (!m) return;
    mtEditId = mid;
    document.getElementById('mtDefName').value = m.name;
    document.getElementById('mtDefName').disabled = true;
    document.getElementById('mtDefDesc').value = m.description || '';
    document.getElementById('mtDefUnit').value = m.unit || '';
    document.getElementById('mtDefSQL').value = m.sql_template || '';
    document.getElementById('mtDefParams').value = (m.params && m.params.length) ? JSON.stringify(m.params, null, 2) : '';
    document.getElementById('mtDefSave').textContent = '保存修改';
    document.getElementById('mtDefCancel').classList.remove('hidden');
  }
});
document.getElementById('mtDefSave').addEventListener('click', async () => {
  const id = document.getElementById('mtDs').value;
  const msg = document.getElementById('mtDefMsg');
  msg.textContent = '';
  const name = document.getElementById('mtDefName').value.trim();
  const sql = document.getElementById('mtDefSQL').value.trim();
  if (!name || !sql) { msg.textContent = '名称和 SQL 模板必填'; return; }
  let params = [];
  const rawP = document.getElementById('mtDefParams').value.trim();
  if (rawP) { try { params = JSON.parse(rawP); } catch { msg.textContent = '参数需为合法 JSON 数组'; return; } }
  const body = {
    name, description: document.getElementById('mtDefDesc').value,
    sql_template: sql, params, unit: document.getElementById('mtDefUnit').value,
  };
  try {
    await api('/admin/api/datasources/' + id + '/metrics', { method: 'POST', body: JSON.stringify(body) });
    resetMtDefForm(); loadMTAdmin(); loadMTMetrics();
  } catch (e) { msg.textContent = e.message; }
});


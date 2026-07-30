// === datasources.js ===
// Data sources admin CRUD
// (split from app.js by // ---- section markers; logic unchanged)

// ---- datasources ----
async function loadDataSourcesTable() {
  const data = await api('/admin/api/datasources');
  const t = document.getElementById('dsTable');
  t.innerHTML = '<thead><tr><th>名称</th><th>类型</th><th>DSN</th><th>操作</th></tr></thead><tbody>' +
    data.datasources.map(d => `<tr><td>${esc(d.name)}</td><td>${esc(d.type)}</td><td>${esc(d.dsn)}</td>
      <td><button class="sec" data-act="editds" data-id="${d.id}">编辑</button>
          <button class="danger" data-act="delds" data-id="${d.id}">删除</button></td></tr>`).join('') + '</tbody>';
}
// ---- datasource edit mode ----
let editingDsId = null;
function startDsEdit(d) {
  editingDsId = d.id;
  document.getElementById('dsName').value = d.name || '';
  document.getElementById('dsType').value = d.type || '';
  document.getElementById('dsDSN').value = d.dsn || '';
  document.getElementById('dsCreate').textContent = '保存修改';
  document.getElementById('dsCancelEdit').classList.remove('hidden');
}
function resetDsForm() {
  editingDsId = null;
  document.getElementById('dsName').value = '';
  document.getElementById('dsType').value = '';
  document.getElementById('dsDSN').value = '';
  document.getElementById('dsCreate').textContent = '创建数据源';
  document.getElementById('dsCancelEdit').classList.add('hidden');
}
document.getElementById('dsCancelEdit').addEventListener('click', resetDsForm);
async function reloadDsAll() {
  loadDataSourcesTable(); loadDataSources('#qDs'); loadDataSources('#gDs');
  loadDataSources('#dsDs'); loadDataSources('#mtDs'); loadDataSourceMap();
}
document.getElementById('dsTable').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  try {
    if (act === 'delds') {
      await api('/admin/api/datasources/' + e.target.dataset.id, { method: 'DELETE' });
      reloadDsAll();
    } else if (act === 'editds') {
      const data = await api('/admin/api/datasources');
      const d = (data.datasources || []).find(x => x.id === e.target.dataset.id);
      if (d) startDsEdit(d);
    }
  } catch (err) { alert(err.message); }
});
document.getElementById('dsCreate').addEventListener('click', async () => {
  const body = { name: document.getElementById('dsName').value, type: document.getElementById('dsType').value, dsn: document.getElementById('dsDSN').value };
  try {
    if (editingDsId) {
      await api('/admin/api/datasources/' + editingDsId, { method: 'PUT', body: JSON.stringify(body) });
      resetDsForm();
    } else {
      await api('/admin/api/datasources', { method: 'POST', body: JSON.stringify(body) });
    }
    reloadDsAll();
  }
  catch (e) { alert(e.message); }
});


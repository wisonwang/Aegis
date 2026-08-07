// === datasources.js ===
// Data sources admin CRUD
// (split from app.js by // ---- section markers; logic unchanged)

// ---- datasources ----
// Populate the "owning workspace" picker on the create/edit form.
async function loadWSForDSSelect() {
  const sel = document.getElementById('dsCreateWS');
  if (!sel) return;
  try {
    const data = await api('/admin/api/workspaces');
    const wss = data.workspaces || [];
    sel.innerHTML = '<option value="">工作区(必选)</option>' +
      wss.map(w => `<option value="${esc(w.ID)}">${esc(w.Name)}</option>`).join('');
    // Default to the active workspace when not in the all-workspaces view.
    if (currentWorkspace && currentWorkspace !== '*') sel.value = currentWorkspace;
  } catch (e) { /* ignore */ }
}

async function loadDataSourcesTable() {
  const t = document.getElementById('dsTable');
  try {
    const data = await api('/admin/api/datasources');
    t.innerHTML = '<thead><tr><th>名称</th><th>类型</th><th>DSN</th><th>工作区</th><th>操作</th></tr></thead><tbody>' +
      data.datasources.map(d => `<tr><td>${esc(d.name)}</td><td>${esc(d.type)}</td><td>${esc(d.dsn)}</td>
        <td>${esc(d.workspace_name || d.workspace_id || 'default')}</td>
        <td><button class="sec" data-act="editds" data-id="${d.id}">编辑</button>
            <button class="danger" data-act="delds" data-id="${d.id}">删除</button></td></tr>`).join('') + '</tbody>';
    loadWSForDSSelect();
  } catch (e) {
    t.innerHTML = '<thead><tr><th>数据源</th></tr></thead><tbody><tr><td class="error">' + esc(e.message) + '</td></tr></tbody>';
  }
}
// ---- datasource edit mode ----
let editingDsId = null;
function startDsEdit(d) {
  editingDsId = d.id;
  document.getElementById('dsName').value = d.name || '';
  document.getElementById('dsType').value = d.type || '';
  const dsWS = document.getElementById('dsCreateWS');
  if (dsWS) dsWS.value = d.workspace_id || '';
  // The list returns a *masked* DSN (password redacted). Never prefill the raw
  // secret field with the placeholder — leave it empty and let the backend keep
  // the existing connection string when the field is submitted blank.
  const dsDSN = document.getElementById('dsDSN');
  if (d.dsn_masked) {
    // Show the masked value (not blank) so the field stays usable and the
    // operator can see the connection is present. Saving the masked value
    // unchanged (or leaving it empty) keeps the existing secret server-side;
    // typing a full DSN replaces the connection.
    dsDSN.value = d.dsn || '';
    dsDSN.placeholder = '（密码已脱敏：保留 **** 或不填则不修改；填入完整 DSN 可更新连接）';
  } else {
    dsDSN.value = d.dsn || '';
    dsDSN.placeholder = '';
  }
  document.getElementById('dsCreate').textContent = '保存修改';
  document.getElementById('dsCancelEdit').classList.remove('hidden');
}
function resetDsForm() {
  editingDsId = null;
  document.getElementById('dsName').value = '';
  document.getElementById('dsType').value = '';
  document.getElementById('dsDSN').value = '';
  const dsWS = document.getElementById('dsCreateWS');
  if (dsWS) dsWS.value = (currentWorkspace && currentWorkspace !== '*') ? currentWorkspace : '';
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
      if (!confirm('确认删除该数据源？')) return;
      await withButtonBusy(e.target, '删除中...', async () => {
        await api('/admin/api/datasources/' + e.target.dataset.id, { method: 'DELETE' });
        reloadDsAll();
      });
      toast('数据源已删除', 'success');
    } else if (act === 'editds') {
      const data = await api('/admin/api/datasources');
      const d = (data.datasources || []).find(x => x.id === e.target.dataset.id);
      if (d) startDsEdit(d);
    }
  } catch (err) { toast(err.message, 'error'); }
});
document.getElementById('dsCreate').addEventListener('click', async (e) => {
  const name = document.getElementById('dsName').value;
  const type = document.getElementById('dsType').value;
  const dsn = document.getElementById('dsDSN').value;
  // On create, a DSN is required. On edit, an empty DSN means "keep the existing
  // (masked) connection string" — the backend ignores blank DSN on update.
  if (!name || !type) { toast('请填写名称和类型', 'warning'); return; }
  if (!editingDsId && !dsn) { toast('请填写 DSN', 'warning'); return; }
  // Resolve the owning workspace. From the all-workspaces view it is required
  // explicitly (the backend refuses to collapse into "default"); when scoped
  // to a concrete workspace it defaults to that workspace.
  const wsSel = document.getElementById('dsCreateWS');
  let wsID = wsSel ? wsSel.value : '';
  if (!wsID && currentWorkspace && currentWorkspace !== '*') wsID = currentWorkspace;
  if (!wsID) { toast('请选择数据源所属的工作区', 'warning'); return; }
  const body = { name, type };
  if (dsn) body.dsn = dsn;
  if (wsID) body.workspace_id = wsID;
  const isEdit = !!editingDsId;
  try {
    await withButtonBusy(e.currentTarget, isEdit ? '保存中...' : '创建中...', async () => {
      if (editingDsId) {
        await api('/admin/api/datasources/' + editingDsId, { method: 'PUT', body: JSON.stringify(body) });
        resetDsForm();
      } else {
        await api('/admin/api/datasources', { method: 'POST', body: JSON.stringify(body) });
      }
      reloadDsAll();
    });
    toast(isEdit ? '数据源已更新' : '数据源已创建', 'success');
  }
  catch (e) { toast(e.message, 'error'); }
});

// Switching the active workspace re-scopes the datasource list (the backend
// filters by X-Workspace-Id) and refreshes the owning-workspace picker.
window.addEventListener('workspace-changed', () => {
  loadDataSourcesTable();
});

// === datasets.js ===
// Dataset management (data products) + hierarchical catalog folders +
// consumption catalog tree + dataset governance
// (split from app.js by // ---- section markers; logic unchanged)

// ---- datasource map (shared) ----
async function loadDataSourceMap() {
  try {
    const data = await api('/api/v1/datasources');
    window.__dsMap = {};
    for (const d of (data.datasources || [])) window.__dsMap[d.id] = d.name;
  } catch (e) { window.__dsMap = {}; }
}
function dsName(id) { return (window.__dsMap && window.__dsMap[id]) || id || '—'; }

// ---- catalog folders (state + helpers) ----
window.__folders = [];
window.__datasets = [];
window.__curFolder = ''; // '' = all datasets

async function loadFolders() {
  try { const d = await api('/admin/api/dataset-folders'); window.__folders = d.folders || []; }
  catch (e) { window.__folders = []; }
}
function folderChildrenMap() {
  const map = {};
  for (const f of (window.__folders || [])) {
    (map[f.parent_id || ''] = map[f.parent_id || ''] || []).push(f);
  }
  return map;
}
function folderName(id) {
  if (!id) return '未分类';
  const f = (window.__folders || []).find(x => x.id === id);
  return f ? f.name : '未分类';
}
function fillFolderSelect(selId, selectedId) {
  const sel = document.getElementById(selId);
  const map = folderChildrenMap();
  function opts(id, depth, acc) {
    for (const f of (map[id] || [])) {
      acc.push(`<option value="${esc(f.id)}">${' '.repeat(depth * 2)}${esc(f.name)}</option>`);
      opts(f.id, depth + 1, acc);
    }
    return acc;
  }
  sel.innerHTML = '<option value="">（根目录 / 未分类）</option>' + opts('', 0, []).join('');
  if (selectedId !== undefined) sel.value = selectedId || '';
}

// ---- admin folder tree (left panel) ----
function renderFolderTree() {
  const tree = document.getElementById('dsFolderTree');
  const map = folderChildrenMap();
  const counts = {};
  for (const d of (window.__datasets || [])) counts[d.folder_id || ''] = (counts[d.folder_id || ''] || 0) + 1;
  function node(id) {
    const kids = map[id] || [];
    return kids.map(f => {
      const hasKids = map[f.id] && map[f.id].length;
      const childrenHtml = hasKids ? `<ul class="tchildren">${node(f.id)}</ul>` : '';
      const sel = window.__curFolder === f.id ? 'sel' : '';
      return `<li class="collapsed"><div class="tnode ${sel}" data-id="${esc(f.id)}">
        <span class="tarrow">${hasKids ? '▸' : '·'}</span>
        <span class="tlabel">${esc(f.name)}</span>
        <span class="tcount">${counts[f.id] || 0}</span>
      </div>${childrenHtml}</li>`;
    }).join('');
  }
  const rootCount = window.__datasets ? window.__datasets.length : '';
  tree.innerHTML = `<li><div class="tnode ${window.__curFolder === '' ? 'sel' : ''}" data-id="">
      <span class="tarrow"></span><span class="tlabel">全部数据集</span><span class="tcount">${rootCount}</span>
    </div></li>` + node('');
  tree.querySelectorAll('.tnode').forEach(el => {
    const arrow = el.querySelector('.tarrow');
    if (arrow && arrow.textContent === '▸') {
      arrow.addEventListener('click', (e) => {
        e.stopPropagation();
        const li = el.parentElement;
        li.classList.toggle('collapsed');
        arrow.textContent = li.classList.contains('collapsed') ? '▸' : '▾';
      });
    }
    el.addEventListener('click', () => selectFolder(el.dataset.id));
  });
}
function selectFolder(id) {
  window.__curFolder = id;
  try { document.getElementById('dsFolder').value = id || ''; } catch (e) {}
  renderFolderTree();
  renderFolderBar();
  loadDatasetsTable();
}
function renderFolderBar() {
  const bar = document.getElementById('dsFolderBar');
  if (!window.__curFolder) { bar.classList.add('hidden'); bar.innerHTML = ''; return; }
  const f = (window.__folders || []).find(x => x.id === window.__curFolder);
  bar.classList.remove('hidden');
  bar.innerHTML = `当前目录：<b>${esc(f ? f.name : '')}</b>
    <button class="sec" id="fbNewSub">在此新建子目录</button>
    <button class="sec" id="fbRename">重命名</button>
    <button class="danger" id="fbDelete">删除</button>`;
  document.getElementById('fbNewSub').onclick = () => openFolderDialog('create', window.__curFolder);
  document.getElementById('fbRename').onclick = () => openFolderDialog('rename', window.__curFolder);
  document.getElementById('fbDelete').onclick = async () => {
    if (!confirm('删除目录「' + (f ? f.name : '') + '」？该目录必须为空（无子目录、无数据集）。')) return;
    try {
      await api('/admin/api/dataset-folders/' + window.__curFolder, { method: 'DELETE' });
      window.__curFolder = '';
      await loadFolders(); renderFolderTree(); renderFolderBar(); loadDatasetsTable();
    } catch (e) { alert(e.message); }
  };
}

// ---- folder dialog (create / rename / move) ----
let __fdMode = 'create', __fdId = '', __moveDsId = '';
function openFolderDialog(mode, id) {
  __fdMode = mode; __fdId = id || '';
  const nameWrap = document.getElementById('fdNameWrap');
  const parentWrap = document.getElementById('fdParentWrap');
  const destWrap = document.getElementById('fdDestWrap');
  nameWrap.classList.toggle('hidden', mode === 'move');
  parentWrap.classList.toggle('hidden', mode !== 'create');
  destWrap.classList.toggle('hidden', mode !== 'move');
  document.getElementById('fdMsg').textContent = '';
  if (mode === 'create') {
    document.getElementById('fdTitle').textContent = '新建目录';
    document.getElementById('fdName').value = '';
    fillFolderSelect('fdParent', '');
    document.getElementById('fdParent').value = __fdId || '';
  } else if (mode === 'rename') {
    const f = (window.__folders || []).find(x => x.id === __fdId);
    document.getElementById('fdTitle').textContent = '重命名目录';
    document.getElementById('fdName').value = f ? f.name : '';
  } else if (mode === 'move') {
    __moveDsId = id;
    document.getElementById('fdTitle').textContent = '移动到目录';
    fillFolderSelect('fdDest', '');
    const ds = (window.__datasets || []).find(x => x.id === __moveDsId);
    document.getElementById('fdDest').value = ds ? (ds.folder_id || '') : '';
  }
  const dlg = document.getElementById('folderDialog');
  if (typeof dlg.showModal === 'function') dlg.showModal(); else dlg.setAttribute('open', '');
}
document.getElementById('fdSave').addEventListener('click', async (e) => {
  e.preventDefault();
  const msg = document.getElementById('fdMsg'); msg.textContent = '';
  try {
    if (__fdMode === 'create') {
      const name = document.getElementById('fdName').value.trim();
      if (!name) { msg.textContent = '请填写名称'; return; }
      const parent = document.getElementById('fdParent').value;
      await api('/admin/api/dataset-folders', { method: 'POST', body: JSON.stringify({ name, parent_id: parent }) });
    } else if (__fdMode === 'rename') {
      const name = document.getElementById('fdName').value.trim();
      if (!name) { msg.textContent = '请填写名称'; return; }
      await api('/admin/api/dataset-folders/' + __fdId, { method: 'PUT', body: JSON.stringify({ name }) });
    } else if (__fdMode === 'move') {
      const dest = document.getElementById('fdDest').value;
      await api('/admin/api/datasets/' + __moveDsId + '/move', { method: 'POST', body: JSON.stringify({ folder_id: dest }) });
    }
    document.getElementById('folderDialog').close();
    await loadFolders(); renderFolderTree(); renderFolderBar(); loadDatasetsTable();
  } catch (err) { msg.textContent = err.message; }
});
document.getElementById('fdCancel').addEventListener('click', () => { document.getElementById('folderDialog').close(); });
document.getElementById('dsNewRoot').addEventListener('click', () => openFolderDialog('create', ''));

// ---- dataset management (data products) ----
async function loadDatasetsTable() {
  const t = document.getElementById('datasetTable');
  try {
    const qs = window.__curFolder ? ('?folder_id=' + encodeURIComponent(window.__curFolder) + '&recursive=1') : '';
    const data = await api('/admin/api/datasets' + qs);
    const sets = data.datasets || [];
    window.__datasets = sets;
    if (!sets.length) {
      t.innerHTML = '<thead><tr><th>数据集</th></tr></thead><tbody><tr><td>该目录下暂无数据集</td></tr></tbody>';
      renderFolderTree();
      return;
    }
    t.innerHTML = '<thead><tr><th>名称</th><th>显示名</th><th>目录</th><th>数据源</th><th>状态</th><th>定义</th><th>操作</th></tr></thead><tbody>' +
      sets.map(d => `<tr>
        <td><code>${esc(d.name)}</code></td>
        <td>${esc(d.display_name || '')}</td>
        <td>${esc(folderName(d.folder_id))}</td>
        <td>${esc(dsName(d.datasource_id))}</td>
        <td><span class="badge ${d.status === 'published' ? 'ok' : 'warning'}">${d.status === 'published' ? '已发布' : '草稿'}</span></td>
        <td><code title="${esc(d.definition || '')}">${esc((d.definition || '').length > 40 ? (d.definition || '').slice(0, 40) + '…' : (d.definition || ''))}</code></td>
        <td>
          <button class="sec" data-act="dsgov" data-id="${d.id}" data-name="${esc(d.name)}">治理</button>
          <button class="sec" data-act="dspub" data-id="${d.id}" data-status="${esc(d.status)}">${d.status === 'published' ? '取消发布' : '发布'}</button>
          <button class="sec" data-act="dsmove" data-id="${d.id}" data-name="${esc(d.name)}">移动</button>
          <button class="sec" data-act="dsedit" data-id="${d.id}">编辑</button>
          <button class="danger" data-act="dsdel" data-id="${d.id}" data-name="${esc(d.name)}">删除</button>
        </td></tr>`).join('') + '</tbody>';
    renderFolderTree();
  } catch (e) {
    t.innerHTML = '<thead><tr><th>数据集</th></tr></thead><tbody><tr><td class="error">' + esc(e.message) + '</td></tr></tbody>';
  }
}

// ---- dataset field contract (visual editor) ----
function renderFields(jsonStr) {
  let fields = [];
  try { fields = JSON.parse(jsonStr || '[]'); } catch (e) { fields = []; }
  if (!Array.isArray(fields)) fields = [];
  const t = document.getElementById('dsFieldsTable');
  if (!fields.length) {
    t.innerHTML = '<thead><tr><th>列名</th><th>类型</th><th>描述</th><th></th></tr></thead><tbody><tr><td colspan="4" class="hint">暂无字段，点“+ 添加字段”</td></tr></tbody>';
    return;
  }
  t.innerHTML = '<thead><tr><th>列名</th><th>类型</th><th>描述</th><th></th></tr></thead><tbody>' +
    fields.map(f => `<tr>
      <td><input class="fname" value="${esc(f.name || '')}" placeholder="列名" /></td>
      <td><input class="ftype" value="${esc(f.type || '')}" placeholder="类型" /></td>
      <td><input class="fdesc" value="${esc(f.description || '')}" placeholder="描述" /></td>
      <td><button class="danger fdel" type="button">×</button></td>
    </tr>`).join('') + '</tbody>';
}
function collectFields() {
  const rows = document.querySelectorAll('#dsFieldsTable tbody tr');
  const out = [];
  rows.forEach(r => {
    const n = r.querySelector('.fname'); if (!n) return;
    const name = n.value.trim();
    if (!name) return;
    out.push({ name, type: r.querySelector('.ftype').value.trim(), description: r.querySelector('.fdesc').value.trim() });
  });
  return JSON.stringify(out);
}
document.getElementById('dsAddField').addEventListener('click', () => {
  const t = document.getElementById('dsFieldsTable');
  let tb = t.querySelector('tbody');
  if (!tb) { t.innerHTML = '<thead><tr><th>列名</th><th>类型</th><th>描述</th><th></th></tr></thead><tbody></tbody>'; tb = t.querySelector('tbody'); }
  const tr = document.createElement('tr');
  tr.innerHTML = '<td><input class="fname" placeholder="列名" /></td><td><input class="ftype" placeholder="类型" /></td><td><input class="fdesc" placeholder="描述" /></td><td><button class="danger fdel" type="button">×</button></td>';
  tb.appendChild(tr);
});
document.getElementById('dsFieldsTable').addEventListener('click', (e) => {
  if (e.target.classList.contains('fdel')) { const tr = e.target.closest('tr'); if (tr) tr.remove(); }
});

function dsResetForm() {
  window.__dsEditId = null;
  document.getElementById('datasetName').value = '';
  document.getElementById('datasetName').disabled = false;
  document.getElementById('dsDisp').value = '';
  document.getElementById('dsDef').value = '';
  document.getElementById('dsFolder').value = window.__curFolder || '';
  renderFields('[]');
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
  const fields = collectFields();
  const status = document.getElementById('dsStatus').value;
  const folderId = document.getElementById('dsFolder').value;
  const editId = window.__dsEditId;
  if (!editId && !name) { msg.textContent = '请填写名称'; return; }
  if (!dsId) { msg.textContent = '请选择数据源'; return; }
  if (!editId && !def.trim()) { msg.textContent = '请填写定义 SQL'; return; }
  if (fields && !isValidJSON(fields)) { msg.textContent = '字段契约需为合法 JSON'; return; }
  const body = editId
    ? { display_name: document.getElementById('dsDisp').value, definition: def, status, fields, folder_id: folderId }
    : { name, display_name: document.getElementById('dsDisp').value, datasource_id: dsId, definition: def, status, fields, folder_id: folderId };
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
    } else if (act === 'dsmove') {
      openFolderDialog('move', id);
    } else if (act === 'dsedit') {
      const data = await api('/admin/api/datasets/' + id);
      window.__dsEditId = id;
      document.getElementById('datasetName').value = data.name;
      document.getElementById('datasetName').disabled = true;
      document.getElementById('dsDisp').value = data.display_name || '';
      document.getElementById('dsDs').value = data.datasource_id || '';
      document.getElementById('dsDef').value = data.definition || '';
      document.getElementById('dsFolder').value = data.folder_id || '';
      renderFields((data.fields && data.fields !== '[]') ? data.fields : '[]');
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

// ---- dataset consumption catalog (collapsible tree) ----
async function loadCatalog() {
  const box = document.getElementById('catTree');
  try {
    const [fdata, ddata] = await Promise.all([api('/api/v1/dataset-folders'), api('/api/v1/datasets')]);
    const folders = fdata.folders || [];
    const sets = ddata.datasets || [];
    const q = (document.getElementById('catSearch').value || '').toLowerCase();
    if (q) {
      const filtered = sets.filter(s => !q || (s.name || '').toLowerCase().includes(q) || (s.display_name || '').toLowerCase().includes(q) || (s.description || '').toLowerCase().includes(q));
      if (!filtered.length) { box.innerHTML = '<p class="hint">无匹配数据集。</p>'; return; }
      box.innerHTML = filtered.map(catalogCard).join('');
      wireCatalogCopy(box);
      return;
    }
    const map = {};
    for (const f of folders) (map[f.parent_id || ''] = map[f.parent_id || ''] || []).push(f);
    function folderHtml(id) {
      const kids = map[id] || [];
      return kids.map(f => {
        const childFolders = folderHtml(f.id);
        const childSets = sets.filter(s => (s.folder_id || '') === f.id).map(catalogNode).join('');
        const has = (map[f.id] && map[f.id].length) || childSets;
        return `<div class="cnode folder" data-fid="${esc(f.id)}"><span class="carow">${has ? '▾' : '·'}</span><span>${esc(f.name)}</span></div>
          <div class="cchildren">${childFolders}${childSets}</div>`;
      }).join('');
    }
    const rootFolders = folderHtml('');
    const rootSets = sets.filter(s => !s.folder_id).map(catalogNode).join('');
    const rootHtml = rootSets ? `<div class="cnode folder"><span class="carow">▾</span><span>未分类</span></div><div class="cchildren">${rootSets}</div>` : '';
    if (!rootFolders && !rootHtml) { box.innerHTML = '<p class="hint">暂无可消费的数据集（需已发布且已授权）。</p>'; return; }
    box.innerHTML = rootFolders + rootHtml;
    box.querySelectorAll('.cnode.folder').forEach(el => {
      el.addEventListener('click', () => {
        const ch = el.nextElementSibling;
        if (ch && ch.classList.contains('cchildren')) {
          const hidden = ch.style.display === 'none';
          ch.style.display = hidden ? '' : 'none';
          const ar = el.querySelector('.carow'); if (ar) ar.textContent = hidden ? '▾' : '▸';
        }
      });
    });
    box.querySelectorAll('.cnode.dataset').forEach(el => el.addEventListener('click', () => toggleCatalogDetail(box, el)));
    wireCatalogCopy(box);
  } catch (e) { box.innerHTML = '<p class="error">' + esc(e.message) + '</p>'; }
}
function catalogNode(s) {
  return `<div class="cnode dataset" data-id="${esc(s.id)}" data-name="${esc(s.name)}"><span class="carow">●</span>
    <span><b>${esc(s.display_name || s.name)}</b> <span class="badge ${s.status === 'published' ? 'ok' : 'warning'}">${s.status === 'published' ? '已发布' : '草稿'}</span></span>
    <span class="hint">${esc(dsName(s.datasource_id))}</span></div>`;
}
function catalogCard(s) {
  let fields = []; try { fields = JSON.parse(s.fields || '[]'); } catch (e) {}
  const fieldRows = fields.length ? fields.map(f => `<li><code>${esc(f.name)}</code> <span class="hint">${esc(f.type || '')}</span> — ${esc(f.description || '')}</li>`).join('') : '<li class="hint">无字段契约</li>';
  return `<div class="card">
    <h4>${esc(s.display_name || s.name)} <span class="badge ${s.status === 'published' ? 'ok' : 'warning'}">${s.status === 'published' ? '已发布' : '草稿'}</span></h4>
    <div class="hint">消费句柄：<code>${esc(s.name)}</code> · 数据源：${esc(dsName(s.datasource_id))} · 目录：${esc(folderName(s.folder_id))}</div>
    <p>${esc(s.description || '—')}</p>
    <div><b>字段契约</b><ul class="fieldlist">${fieldRows}</ul></div>
    <div class="row"><button class="sec" data-act="copy" data-val="${esc(s.name)}">复制消费句柄</button></div>
  </div>`;
}
function toggleCatalogDetail(box, el) {
  const existing = box.querySelector('.cdetail[data-for="' + el.dataset.id + '"]');
  if (existing) { existing.remove(); return; }
  box.querySelectorAll('.cdetail').forEach(d => d.remove());
  const id = el.dataset.id;
  api('/api/v1/datasets/' + id).then(d => {
    let fields = []; try { fields = JSON.parse(d.fields || '[]'); } catch (e) {}
    const fieldRows = fields.length ? fields.map(f => `<li><code>${esc(f.name)}</code> <span class="hint">${esc(f.type || '')}</span> — ${esc(f.description || '')}</li>`).join('') : '<li class="hint">无字段契约</li>';
    const div = document.createElement('div');
    div.className = 'cdetail'; div.dataset.for = id;
    div.innerHTML = `<div class="hint">消费句柄：<code>${esc(d.name)}</code> · 数据源：${esc(dsName(d.datasource_id))} · 目录：${esc(folderName(d.folder_id))}</div>
      <p>${esc(d.description || '—')}</p>
      <div><b>字段契约</b><ul class="fieldlist">${fieldRows}</ul></div>
      <div class="row"><button class="sec" data-act="copy" data-val="${esc(d.name)}">复制消费句柄</button></div>`;
    el.insertAdjacentElement('afterend', div);
    wireCatalogCopy(box);
  }).catch(e => alert(e.message));
}
function wireCatalogCopy(box) {
  box.querySelectorAll('[data-act="copy"]').forEach(btn => {
    btn.onclick = async () => {
      try { await navigator.clipboard.writeText(btn.dataset.val); btn.textContent = '已复制!'; setTimeout(() => btn.textContent = '复制消费句柄', 1500); }
      catch (err) { alert('复制失败'); }
    };
  });
}
document.getElementById('catRefresh').addEventListener('click', loadCatalog);
document.getElementById('catSearch').addEventListener('input', loadCatalog);
document.querySelector('[data-tab="catalog"]').addEventListener('click', loadCatalog);

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

document.querySelector('[data-tab="datasets"]').addEventListener('click', async () => {
  await loadDataSourceMap(); loadDataSources('#dsDs');
  await loadFolders();
  fillFolderSelect('dsFolder', '');
  renderFolderTree(); renderFolderBar(); loadDatasetsTable();
});

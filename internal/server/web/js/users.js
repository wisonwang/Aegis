// === users.js ===
// Identity: users CRUD + roles + workspaces + API keys
// (split from app.js by // ---- section markers; logic unchanged)

// ---- users ----
async function loadUsers() {
  const wsSel = document.getElementById('uWsFilter');
  const ws = wsSel ? wsSel.value : '';
  const url = '/admin/api/users' + (ws ? ('?workspace=' + encodeURIComponent(ws)) : '');
  const data = await api(url);
  const t = document.getElementById('usersTable');
  t.innerHTML = '<thead><tr><th>用户名</th><th>显示名</th><th>邮箱</th><th>类型</th><th>来源</th><th>状态</th><th>角色</th><th>属性</th><th>最后登录</th><th>操作</th></tr></thead><tbody>' +
    data.users.map(u => {
      const typeBadge = u.type === 'service'
        ? '<span class="badge warning">服务账号</span>'
        : '<span class="badge">人类</span>';
      const srcBadge = u.source === 'sso'
        ? '<span class="badge sys">SSO</span>'
        : '<span class="badge">本地</span>';
      return `<tr>
      <td>${esc(u.username)}</td>
      <td>${esc(u.display_name || '')}</td>
      <td>${esc(u.email || '—')}</td>
      <td>${typeBadge}</td>
      <td>${srcBadge}</td>
      <td>${esc(u.status)}</td>
      <td>${u.roles.map(r => esc(r)).join(', ')}</td>
      <td>${esc(JSON.stringify(u.attributes))}</td>
      <td>${esc(u.last_login_at || '—')}</td>
      <td>
        <select data-uid="${u.id}" class="rolePick">${roleOptionsHTML(u.roles)}</select>
        <button class="sec" data-act="addrole" data-uid="${u.id}">加角色</button>
        <button class="sec" data-act="delrole" data-uid="${u.id}">减角色</button>
        <button class="sec" data-act="uedit" data-uid="${u.id}">编辑</button>
        <button class="sec" data-act="upwd" data-uid="${u.id}">改密</button>
        <button class="sec" data-act="akey" data-uid="${u.id}" data-name="${esc(u.username)}">API Key</button>
        <button class="danger" data-act="deluser" data-uid="${u.id}">删除</button>
      </td></tr>`;
    }).join('') + '</tbody>';
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
      if (!confirm('确认删除该用户？')) return;
      await withButtonBusy(e.target, '删除中...', async () => {
        await api('/admin/api/users/' + uid, { method: 'DELETE' });
      });
      toast('用户已删除', 'success');
    } else if (act === 'addrole') {
      const role = e.target.parentElement.querySelector('.rolePick').value;
      await withButtonBusy(e.target, '添加中...', async () => {
        await api('/admin/api/users/' + uid + '/roles', { method: 'POST', body: JSON.stringify({ role }) });
      });
      toast('角色已添加', 'success');
    } else if (act === 'delrole') {
      const role = e.target.parentElement.querySelector('.rolePick').value;
      await withButtonBusy(e.target, '移除中...', async () => {
        await api('/admin/api/users/' + uid + '/roles/' + encodeURIComponent(role), { method: 'DELETE' });
      });
      toast('角色已移除', 'success');
    } else if (act === 'upwd') {
      const pw = prompt('为用户设置新密码:');
      if (!pw) return;
      await withButtonBusy(e.target, '更新中...', async () => {
        await api('/admin/api/users/' + uid + '/password', { method: 'POST', body: JSON.stringify({ password: pw }) });
      });
      toast('密码已更新', 'success');
      return;
    } else if (act === 'akey') {
      openAPIKeyDialog(uid, e.target.dataset.name);
      return;
    } else if (act === 'uedit') {
      startUserEdit(uid);
      return;
    }
    loadUsers();
  } catch (err) { toast(err.message, 'error'); }
});

// ---- user edit mode ----
let editingUserId = null;
async function startUserEdit(uid) {
  try {
    const data = await api('/admin/api/users');
    const u = (data.users || []).find(x => x.id === uid);
    if (!u) return;
    editingUserId = uid;
    document.getElementById('uName').value = u.username;
    document.getElementById('uName').disabled = true;
    document.getElementById('uDisp').value = u.display_name || '';
    document.getElementById('uEmail').value = u.email || '';
    document.getElementById('uType').value = u.type || 'human';
    document.getElementById('uWS').value = '';
    document.getElementById('uPass').value = '';
    document.getElementById('uPass').classList.add('hidden'); // 密码走「改密」按钮
    document.getElementById('uAttrs').value = u.attributes ? JSON.stringify(u.attributes) : '';
    const st = document.getElementById('uStatus');
    st.value = u.status || 'active';
    st.classList.remove('hidden');
    document.getElementById('uCreate').textContent = '保存修改';
    document.getElementById('uCancel').classList.remove('hidden');
  } catch (err) { toast(err.message, 'error'); }
}
function resetUserForm() {
  editingUserId = null;
  document.getElementById('uName').value = '';
  document.getElementById('uName').disabled = false;
  document.getElementById('uDisp').value = '';
  document.getElementById('uEmail').value = '';
  document.getElementById('uType').value = 'human';
  document.getElementById('uWS').value = '';
  document.getElementById('uPass').value = '';
  document.getElementById('uPass').classList.remove('hidden');
  document.getElementById('uAttrs').value = '';
  document.getElementById('uStatus').classList.add('hidden');
  document.getElementById('uCreate').textContent = '创建用户';
  document.getElementById('uCancel').classList.add('hidden');
}
document.getElementById('uCancel').addEventListener('click', resetUserForm);

// ---- workspace selects for the user directory ----
async function fillWSSelects() {
  try {
    const data = await api('/admin/api/workspaces');
    const wss = data.workspaces || [];
    // NOTE: the workspaces API returns UPPERCASE keys (ID/Name/Slug) because
    // store.Workspace has no JSON tags — see project conventions. Use w.ID.
    const opts = '<option value="">工作区(可选)</option>' + wss.map(w => `<option value="${esc(w.ID)}">${esc(w.Name)}</option>`).join('');
    document.getElementById('uWS').innerHTML = opts;
    const fopts = '<option value="">全部</option>' + wss.map(w => `<option value="${esc(w.ID)}">${esc(w.Name)}</option>`).join('');
    document.getElementById('uWsFilter').innerHTML = fopts;
  } catch (e) { /* non-fatal */ }
}
document.getElementById('uWsFilter').addEventListener('change', loadUsers);

// ---- per-user API key management ----
let akUserID = null;
function openAPIKeyDialog(uid, name) {
  akUserID = uid;
  document.getElementById('akUser').textContent = name || uid;
  const nb = document.getElementById('akNewKey');
  nb.classList.add('hidden'); nb.textContent = '';
  loadAPIKeys(uid);
  document.getElementById('apiKeyDialog').showModal();
}
async function loadAPIKeys(uid) {
  const data = await api('/admin/api/users/' + uid + '/apikeys');
  const keys = data.api_keys || [];
  const t = document.getElementById('akTable');
  if (keys.length === 0) {
    t.innerHTML = '<thead><tr><th>名称</th><th>前缀</th><th>状态</th><th>最后使用</th><th>操作</th></tr></thead><tbody><tr><td colspan="5">暂无 Key</td></tr></tbody>';
    return;
  }
  t.innerHTML = '<thead><tr><th>名称</th><th>前缀</th><th>状态</th><th>最后使用</th><th>操作</th></tr></thead><tbody>' +
    keys.map(k => `<tr>
      <td>${esc(k.name)}</td>
      <td><code>${esc(k.prefix)}</code></td>
      <td>${esc(k.status)}</td>
      <td>${esc((k.last_used_at || '').startsWith('0001') ? '—' : (k.last_used_at || '—'))}</td>
      <td><button class="danger" data-akid="${k.id}">吊销</button></td>
    </tr>`).join('') + '</tbody>';
}
document.getElementById('akTable').addEventListener('click', async (e) => {
  const akid = e.target.dataset.akid;
  if (!akid) return;
  if (!confirm('确认吊销该 API Key？')) return;
  try {
    await withButtonBusy(e.target, '吊销中...', async () => {
      await api('/admin/api/users/' + akUserID + '/apikeys/' + akid, { method: 'DELETE' });
    });
    loadAPIKeys(akUserID);
    toast('API Key 已吊销', 'success');
  } catch (err) { toast(err.message, 'error'); }
});
document.getElementById('akCreate').addEventListener('click', async (e) => {
  const name = document.getElementById('akName').value || 'key';
  const exp = document.getElementById('akExpires').value;
  try {
    await withButtonBusy(e.currentTarget, '生成中...', async () => {
      const data = await api('/admin/api/users/' + akUserID + '/apikeys', { method: 'POST', body: JSON.stringify({ name, expires_in: exp }) });
      const box = document.getElementById('akNewKey');
      box.classList.remove('hidden');
      box.innerHTML = '新 Key（仅显示一次）：<code style="word-break:break-all">' + esc(data.key) + '</code>';
    });
    toast('API Key 已生成', 'success');
  } catch (err) { toast(err.message, 'error'); }
  document.getElementById('akName').value = '';
  document.getElementById('akExpires').value = '';
  loadAPIKeys(akUserID);
});
document.getElementById('akClose').addEventListener('click', () => document.getElementById('apiKeyDialog').close());

document.getElementById('uCreate').addEventListener('click', async (e) => {
  const isEdit = !!editingUserId;
  try {
    await withButtonBusy(e.currentTarget, isEdit ? '保存中...' : '创建中...', async () => {
      if (editingUserId) {
        const body = {
          display_name: document.getElementById('uDisp').value,
          email: document.getElementById('uEmail').value,
          type: document.getElementById('uType').value,
          status: document.getElementById('uStatus').value,
          attributes: JSON.parse(document.getElementById('uAttrs').value || '{}'),
        };
        await api('/admin/api/users/' + editingUserId, { method: 'PUT', body: JSON.stringify(body) });
        resetUserForm();
      } else {
        const utype = document.getElementById('uType').value;
        const ws = document.getElementById('uWS').value;
        const body = {
          username: document.getElementById('uName').value,
          display_name: document.getElementById('uDisp').value,
          email: document.getElementById('uEmail').value,
          type: utype,
          password: utype === 'service' ? '' : document.getElementById('uPass').value,
          workspace: ws || undefined,
          attributes: JSON.parse(document.getElementById('uAttrs').value || '{}'),
        };
        await api('/admin/api/users', { method: 'POST', body: JSON.stringify(body) });
      }
    });
    loadUsers();
    toast(isEdit ? '用户已更新' : '用户已创建', 'success');
  } catch (e) { toast(e.message, 'error'); }
});

// ---- roles ----
let editingRoleId = null;
async function loadRoles() {
  const data = await api('/admin/api/roles');
  window.__roles = data.roles;
  const t = document.getElementById('rolesTable');
  t.innerHTML = '<thead><tr><th>名称</th><th>描述</th><th>类型</th><th>操作</th></tr></thead><tbody>' +
    data.roles.map(r => {
      const badge = r.system ? '<span class="badge sys">内置</span>' : '<span class="badge">自定义</span>';
      const editBtn = r.system
        ? '<button class="sec" disabled>编辑</button>'
        : '<button class="sec" data-act="editrole" data-id="' + r.id + '">编辑</button>';
      const delBtn = r.system
        ? '<button class="danger" disabled>删除</button>'
        : '<button class="danger" data-act="delrole" data-id="' + r.id + '">删除</button>';
      return `<tr><td>${esc(r.name)}</td><td>${esc(r.description)}</td><td>${badge}</td><td>${editBtn}${delBtn}</td></tr>`;
    }).join('') + '</tbody>';
}
document.getElementById('rolesTable').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  const id = e.target.dataset.id;
  try {
    if (act === 'delrole') {
      if (!confirm('确认删除该角色？关联的用户授权、表/行/列权限将一并移除。')) return;
      await withButtonBusy(e.target, '删除中...', async () => {
        await api('/admin/api/roles/' + id, { method: 'DELETE' });
      });
      toast('角色已删除', 'success');
    } else if (act === 'editrole') {
      startRoleEdit(id);
      return;
    }
    loadRoles(); loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole'); loadRolesInto('#gMaskRole');
  } catch (err) { toast(err.message, 'error'); }
});
async function startRoleEdit(id) {
  const data = await api('/admin/api/roles');
  const r = (data.roles || []).find(x => x.id === id);
  if (!r) return;
  editingRoleId = id;
  document.getElementById('rName').value = r.name;
  document.getElementById('rName').disabled = true; // 名称可作为唯一键，编辑时锁定
  document.getElementById('rDesc').value = r.description || '';
  document.getElementById('rCreate').textContent = '保存修改';
  document.getElementById('rCancel').classList.remove('hidden');
}
function resetRoleForm() {
  editingRoleId = null;
  document.getElementById('rName').value = '';
  document.getElementById('rName').disabled = false;
  document.getElementById('rDesc').value = '';
  document.getElementById('rCreate').textContent = '创建角色';
  document.getElementById('rCancel').classList.add('hidden');
}
document.getElementById('rCancel').addEventListener('click', resetRoleForm);
document.getElementById('rCreate').addEventListener('click', async (e) => {
  const isEdit = !!editingRoleId;
  try {
    const body = { name: document.getElementById('rName').value, description: document.getElementById('rDesc').value };
    await withButtonBusy(e.currentTarget, isEdit ? '保存中...' : '创建中...', async () => {
      if (editingRoleId) {
        await api('/admin/api/roles/' + editingRoleId, { method: 'PUT', body: JSON.stringify(body) });
        resetRoleForm();
      } else {
        await api('/admin/api/roles', { method: 'POST', body: JSON.stringify(body) });
      }
    });
    loadRoles(); loadRolesInto('#gRole'); loadRolesInto('#gPolicyRole'); loadRolesInto('#gMaskRole');
    toast(isEdit ? '角色已更新' : '角色已创建', 'success');
  } catch (e) { toast(e.message, 'error'); }
});
async function loadRolesInto(sel) {
  try {
    const data = await api('/admin/api/roles');
    window.__roles = data.roles;
    document.querySelector(sel).innerHTML = data.roles.map(r => `<option value="${esc(r.name)}">${esc(r.name)}</option>`).join('');
  } catch (e) { /* ignore */ }
}

// ---- workspaces ----
let editingWsId = null;
async function loadWorkspaces() {
  try {
    const data = await api('/admin/api/workspaces');
    const t = document.getElementById('wsTable');
    const wsList = data.workspaces || [];
    t.innerHTML = '<thead><tr><th>名称</th><th>Slug</th><th>成员</th><th>操作</th></tr></thead><tbody>' +
      wsList.map(w => `<tr>
        <td>${esc(w.Name)}${w.ID === 'default' ? ' <span class="badge ok">默认</span>' : ''}</td>
        <td><code>${esc(w.Slug)}</code></td>
        <td><button class="sec" data-act="wsmem" data-id="${esc(w.ID)}" data-name="${esc(w.Name)}">管理成员</button></td>
        <td>${w.ID === 'default' ? '—' : `<button class="danger" data-act="wsdel" data-id="${esc(w.ID)}">删除</button>`}</td>
      </tr>`).join('') + '</tbody>';
  } catch (e) { /* ignore */ }
}
async function loadUsersIntoWS() {
  try {
    const data = await api('/admin/api/users');
    const sel = document.getElementById('wsMemUser');
    sel.innerHTML = (data.users || []).map(u => `<option value="${esc(u.id)}">${esc(u.username)} (${esc(u.display_name || '')})</option>`).join('');
  } catch (e) { /* ignore */ }
}
document.getElementById('wsCreate').addEventListener('click', async (e) => {
  const body = { name: document.getElementById('wsName').value, slug: document.getElementById('wsSlug').value };
  if (!body.name) { toast('请填写名称', 'warning'); return; }
  try {
    await withButtonBusy(e.currentTarget, '创建中...', async () => {
      await api('/admin/api/workspaces', { method: 'POST', body: JSON.stringify(body) });
    });
    document.getElementById('wsName').value = '';
    document.getElementById('wsSlug').value = '';
    loadWorkspaces();
    toast('工作区已创建', 'success');
  } catch (e) { toast(e.message, 'error'); }
});
document.getElementById('wsLoad').addEventListener('click', loadWorkspaces);
document.getElementById('wsTable').addEventListener('click', async (e) => {
  const act = e.target.dataset.act;
  if (!act) return;
  try {
    if (act === 'wsdel') {
      if (!confirm('删除工作区将移除其所有成员关联。确认？')) return;
      await withButtonBusy(e.target, '删除中...', async () => {
        await api('/admin/api/workspaces/' + e.target.dataset.id, { method: 'DELETE' });
      });
      loadWorkspaces();
      toast('工作区已删除', 'success');
    } else if (act === 'wsmem') {
      editingWsId = e.target.dataset.id;
      document.getElementById('wsMemName').textContent = e.target.dataset.name;
      document.getElementById('wsMembers').classList.remove('hidden');
      loadWsMembers();
      document.getElementById('wsMembers').scrollIntoView({ behavior: 'smooth', block: 'start' });
    }
  } catch (err) { toast(err.message, 'error'); }
});
async function loadWsMembers() {
  if (!editingWsId) return;
  try {
    const data = await api('/admin/api/workspaces/' + editingWsId + '/members');
    const t = document.getElementById('wsMemTable');
    t.innerHTML = '<thead><tr><th>用户 ID</th><th>角色</th><th>默认</th><th>操作</th></tr></thead><tbody>' +
      (data.members || []).map(m => `<tr>
        <td><code>${esc(m.UserID)}</code></td>
        <td>${esc(m.Role)}</td>
        <td>${m.IsDefault ? '是' : '否'}</td>
        <td><button class="danger" data-act="wsrmmem" data-uid="${esc(m.UserID)}">移除</button></td>
      </tr>`).join('') + '</tbody>';
  } catch (e) { /* ignore */ }
}
document.getElementById('wsAddMem').addEventListener('click', async (e) => {
  if (!editingWsId) return;
  const body = { user_id: document.getElementById('wsMemUser').value, role: document.getElementById('wsMemRole').value };
  try {
    await withButtonBusy(e.currentTarget, '添加中...', async () => {
      await api('/admin/api/workspaces/' + editingWsId + '/members', { method: 'POST', body: JSON.stringify(body) });
    });
    loadWsMembers();
    toast('成员已添加', 'success');
  } catch (e) { toast(e.message, 'error'); }
});
document.getElementById('wsMemTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'wsrmmem') return;
  try {
    await withButtonBusy(e.target, '移除中...', async () => {
      await api('/admin/api/workspaces/' + editingWsId + '/members/' + encodeURIComponent(e.target.dataset.uid), { method: 'DELETE' });
    });
    loadWsMembers();
    toast('成员已移除', 'success');
  } catch (err) { toast(err.message, 'error'); }
});
document.querySelector('[data-tab="ws"]').addEventListener('click', () => { loadWorkspaces(); loadUsersIntoWS(); });

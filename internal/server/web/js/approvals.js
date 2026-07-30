// === approvals.js ===
// Access approval workflow (submit / approve / reject / revoke)
// (split from app.js by // ---- section markers; logic unchanged)

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


// === audit.js ===
// Audit logs + security alerts
// (split from app.js by // ---- section markers; logic unchanged)

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
  } catch (e) { toast(e.message, 'error'); }
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
  } catch (e) { toast(e.message, 'error'); }
}
document.getElementById('alertTable').addEventListener('click', async (e) => {
  if (e.target.dataset.act !== 'resolve') return;
  try { await api('/admin/api/alerts/' + e.target.dataset.id + '/resolve', { method: 'POST' }); loadAlerts(); toast('告警已标记处理', 'success'); }
  catch (err) { toast(err.message, 'error'); }
});
document.getElementById('alLoad').addEventListener('click', () => { alertOffset = 0; loadAlerts(); });
document.querySelector('[data-tab="alerts"]').addEventListener('click', () => { alertOffset = 0; loadAlerts(); });

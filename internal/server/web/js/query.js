// === query.js ===
// Query tab: datasource tables, run query, estimate cost, renderRows
// (split from app.js by // ---- section markers; logic unchanged)

// ---- query ----
document.getElementById('qTables').addEventListener('click', async (e) => {
  const id = document.getElementById('qDs').value;
  const box = document.getElementById('qResult');
  if (!id) { box.innerHTML = '<div class="error">请先选择数据源</div>'; return; }
  await withButtonBusy(e.currentTarget, '加载中...', async () => {
    try {
      const data = await api('/api/v1/datasources/' + id + '/tables');
      box.innerHTML = '<pre>可访问表:\n' + data.tables.map(t => t.name + '  [' + t.ops.join(',') + ']').join('\n') + '</pre>';
    } catch (e) { box.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
  });
});

document.getElementById('qRun').addEventListener('click', async (e) => {
  const id = document.getElementById('qDs').value;
  const sql = document.getElementById('qSql').value;
  const box = document.getElementById('qResult');
  if (!id) { box.innerHTML = '<div class="error">请先选择数据源</div>'; return; }
  if (!sql.trim()) { box.innerHTML = '<div class="error">请先输入 SQL</div>'; return; }
  box.innerHTML = '执行中...';
  await withButtonBusy(e.currentTarget, '执行中...', async () => {
    try {
      const data = await api('/api/v1/query', { method: 'POST', body: JSON.stringify({ datasource: id, sql }) });
      let html = '<div>重写后的 SQL: <code>' + esc(data.rewritten_sql) + '</code></div>';
      if (data.rows) {
        html += renderRows(data.columns, data.rows);
        toast('查询执行完成', 'success');
      } else {
        html += '<div>影响行数: ' + data.affected_rows + '</div>';
        toast('语句执行完成', 'success');
      }
      box.innerHTML = html;
    } catch (e) { box.innerHTML = '<div class="error">' + esc(e.message) + '</div>'; }
  });
});

document.getElementById('qEstimate').addEventListener('click', async (e) => {
  const id = document.getElementById('qDs').value;
  const sql = document.getElementById('qSql').value;
  const box = document.getElementById('qEst');
  if (!id) { box.innerHTML = '<div class="error">请先选择数据源</div>'; return; }
  if (!sql.trim()) { box.innerHTML = '<div class="error">请先输入 SQL</div>'; return; }
  box.innerHTML = '评估中...';
  await withButtonBusy(e.currentTarget, '评估中...', async () => {
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
});

document.getElementById('qSql').addEventListener('keydown', (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault();
    document.getElementById('qRun').click();
  }
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

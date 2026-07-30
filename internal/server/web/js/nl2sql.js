// === nl2sql.js ===
// NL2SQL safe gateway
// (split from app.js by // ---- section markers; logic unchanged)

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


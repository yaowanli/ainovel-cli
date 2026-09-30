'use strict';

// 前端只依赖两个契约：REST（命令）+ SSE（事实流）。所有状态都从 SSE 的 snapshot
// 重建，不做本地乐观更新——引擎是唯一事实源。

const $ = (s) => document.querySelector(s);
const el = (t, cls, txt) => {
  const n = document.createElement(t);
  if (cls) n.className = cls;
  if (txt !== undefined) n.textContent = txt;
  return n;
};

const state = {
  projects: new Map(),
  selected: null,
  events: new Map(),   // id -> 行元素（进行中的调用原地更新）
  order: [],
  stream: '',
  chapter: 0,
};

function toast(msg) {
  const t = $('#toast');
  t.textContent = msg;
  t.style.display = 'block';
  clearTimeout(toast._t);
  toast._t = setTimeout(() => (t.style.display = 'none'), 4000);
}

async function api(path, body) {
  const opt = body === undefined
    ? {}
    : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) };
  const res = await fetch(path, opt);
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch (_) { data = { error: text }; }
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

// ── 渲染：项目列表 ──

// holderLabel 说明是谁占着这本书。服务端只能确定是 ainovel 家族的哪个二进制，
// 区分不出 TUI 与 headless（同一个可执行文件），所以文案落在"CLI/TUI"这一档。
function holderLabel(p) {
  const h = p.holder || {};
  if (h.kind === 'server') return '被另一个服务端占用';
  if (h.kind === 'cli') return '被 TUI / CLI 占用';
  return '被其他进程占用';
}

function isLocked(p) { return !!p && p.state === 'locked'; }

// 占用期间禁用全部控制按钮：服务端本来就驱动不了这本书（上游跨进程 flock），
// 与其让用户点下去吃一个 409，不如直接置灰并说明原因。
const CONTROLS = ['#b-resume', '#b-abort', '#b-review', '#b-auto', '#b-next', '#b-steer', '#b-continue'];

function renderLockBanner() {
  let box = document.getElementById('lockbox');
  const p = state.projects.get(state.selected);
  if (!isLocked(p)) { if (box) box.remove(); return; }
  if (!box) {
    box = el('div');
    box.id = 'lockbox';
    box.style.cssText = 'background:#13233d;border:1px solid #2b4a7d;color:#cfe3ff;padding:7px 9px;border-radius:6px;margin-bottom:8px;font-size:12px';
    $('#stat').after(box);
  }
  const h = p.holder || {};
  box.textContent = `本书正被 ${holderLabel(p)}（PID ${h.pid || '?'}${h.name ? ' · ' + h.name : ''}）驱动，控制已禁用。关闭那个终端后本页会自动恢复可操作；数据仍可阅读。`;
  for (const sel of CONTROLS) { const b = $(sel); if (b) b.disabled = true; }
}

function unlockControls() {
  for (const sel of CONTROLS) { const b = $(sel); if (b) b.disabled = false; }
}

function renderList() {
  const box = $('#list');
  box.textContent = '';
  if (state.projects.size === 0) {
    box.appendChild(el('div', 'hint', '还没有项目。左下角写一句话需求即可开新书。'));
    return;
  }
  for (const p of [...state.projects.values()].sort((a, b) => a.id.localeCompare(b.id))) {
    const card = el('div', 'card' + (p.id === state.selected ? ' sel' : ''));
    const head = el('div', 'row');
    head.appendChild(el('div', 't', p.title || p.id));
    head.appendChild(el('span', 'grow'));
    const st = el('span', 'tag ' + (p.error ? 'err' : p.state), stateLabel(p));
    head.appendChild(st);
    card.appendChild(head);
    const sub = el('div', 'sub', `${p.id} · ${p.completed || 0} 章` + (p.total_chapters ? ` / ${p.total_chapters}` : ''));
    if (p.state === 'locked') {
    const h = p.holder || {};
    card.appendChild(el('div', 'sub', `${holderLabel(p)} · PID ${h.pid || '?'}`));
  }
  if (p.dir && p.dir.indexOf('/workspace/') < 0) {
      sub.textContent += ' · 挂载 · ' + p.dir;
      sub.title = p.dir;
    }
    card.appendChild(sub);
    if (p.cost_usd) card.appendChild(el('div', 'sub', `花费 $${p.cost_usd.toFixed(2)}`));
    if (p.total_chapters && p.completed < p.total_chapters) {
      const bar = el('div', 'bar');
      const i = el('i');
      i.style.width = Math.round((p.completed / p.total_chapters) * 100) + '%';
      bar.appendChild(i);
      card.appendChild(bar);
    }
    card.onclick = () => select(p.id);
    box.appendChild(card);
  }
}

function stateLabel(p) {
  if (p.error) return '异常';
  if (p.state === 'running') return '运行中';
  if (p.state === 'done') return p.phase === 'complete' ? '完本' : '已停';
  if (p.state === 'paused') return '暂停';
  if (p.state === 'idle') return '待命';
  // locked = 同一本书正被另一个进程驱动：数据照常显示，但服务端不能接管。
  if (p.state === 'locked') return holderLabel(p);
  return '未在服务端打开';
}

function renderStat() {
  const box = $('#stat');
  box.textContent = '';
  const p = state.projects.get(state.selected);
  if (!p) {
    $('#cur-title').textContent = '未选择项目';
    $('#cur-state').textContent = '-';
    box.appendChild(el('div', 'hint', '从左侧选择一个项目。'));
    return;
  }
  $('#cur-title').textContent = p.title || p.id;
  $('#cur-state').textContent = stateLabel(p);
  $('#cur-state').className = 'tag ' + (p.error ? 'err' : p.state);
  if (isLocked(p)) renderLockBanner(); else { unlockControls(); const b = document.getElementById('lockbox'); if (b) b.remove(); }

  const kv = el('div', 'kv');
  const L = p.live || {};
  const put = (k, v) => { if (v !== undefined && v !== '' && v !== 0) { kv.appendChild(el('b', null, k)); kv.appendChild(el('span', null, String(v))); } };
  put('目录', p.dir);
  put('模型', L.model || (L.provider ? '' : ''));
  put('provider', L.provider);
  put('推理', L.thinking);
  put('风格', L.style);
  put('阶段', `${L.phase || p.phase || '-'}${L.flow ? ' / ' + L.flow : ''}`);
  put('进度', `${L.completed || p.completed || 0} 章 · ${L.words || 0} 字`);
  put('上下文', L.context_window ? `${L.context_window}` : '');
  put('Token', L.total_input_tokens ? `in ${L.total_input_tokens} / out ${L.total_output_tokens}` : '');
  put('缓存', L.cache_capable && L.total_cache_read_tokens ? `read ${L.total_cache_read_tokens}` : '');
  put('花费', L.total_cost_usd ? `$${L.total_cost_usd.toFixed(3)}${L.budget_limit_usd ? ' / $' + L.budget_limit_usd : ''}` : '');
  put('推进模式', L.advance_mode === 'review' ? '逐章验收' : '自动');
  if (L.advance_hold) put('待放行', L.advance_hold_reason);
  if (L.pending_rewrites && L.pending_rewrites.length) put('待重写', L.pending_rewrites.join(','));
  if (L.pending_steer) put('待处理干预', L.pending_steer);
  put('状态', L.status_label);
  box.appendChild(kv);
  if (p.synopsis) {
    const s = el('div', 'sub', p.synopsis);
    s.style.marginTop = '8px';
    s.style.whiteSpace = 'pre-wrap';
    box.appendChild(s);
  }

  // agent 面板
  for (const a of L.agents || []) {
    const n = el('div', 'agent');
    n.appendChild(el('div', 'row')).appendChild(el('div', 't', a.Name));
    n.appendChild(el('div', 'st', `${a.State}${a.Summary ? ' · ' + a.Summary : ''}`));
    if (a.Context && a.Context.Percent > 0) {
      n.appendChild(el('div', 'st', `上下文 ${(a.Context.Percent * 100).toFixed(0)}% · ${a.Context.Scope} · ${a.Context.Strategy}`));
    }
    box.appendChild(n);
  }
  $('#ctx').textContent = L.context_window ? `ctx ${L.context_window}` : '';
}

// ── 渲染：事件流 ──

function pushEvent(ev) {
  const log = $('#log');
  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  let node = ev.id ? state.events.get(ev.id) : null;
  if (!node) {
    node = el('div', 'ev');
    node.appendChild(el('span', 'c'));
    node.appendChild(el('span', 'txt'));
    node.appendChild(el('span', 'ms'));
    log.appendChild(node);
    if (ev.id) state.events.set(ev.id, node);
    if (state.order.length > 400) {
      const old = state.order.shift();
      if (old && state.events.get(old)) state.events.get(old).remove();
      state.events.delete(old);
    }
  }
  if (ev.id) state.order.push(ev.id);
  node.className = 'ev ' + ev.category + (ev.running ? ' running' : '');
  node.children[0].textContent = `${fmtTime(ev.time)} ${ev.category}${ev.agent ? '/' + ev.agent : ''}`;
  node.children[1].textContent = ev.summary || ev.detail || '';
  node.children[2].textContent = ev.duration_ms ? `${(ev.duration_ms / 1000).toFixed(1)}s` : '';
  if (atBottom) log.scrollTop = log.scrollHeight;
}

function fmtTime(t) {
  if (!t) return '--:--:--';
  const d = new Date(t);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// ── 渲染：输出流 / 章节 ──

function pushDelta(text) {
  const out = $('#out');
  if (out.dataset.mode !== 'stream') { out.textContent = ''; out.dataset.mode = 'stream'; }
  state.stream += text;
  out.textContent = state.stream;
  out.scrollTop = out.scrollHeight;
}

function clearStream() {
  state.stream = '';
  const out = $('#out');
  out.dataset.mode = 'stream';
  out.textContent = '';
}

function showChapter(n, content) {
  const out = $('#out');
  out.dataset.mode = 'chapter';
  out.textContent = '';
  out.appendChild(el('div', 'hint', `第 ${n} 章`));
  out.appendChild(el('pre', 'ch', content));
}

// ── SSE ──

function connectGlobal() {
  const es = new EventSource('/api/events');
  es.addEventListener('open', () => { $('#conn').textContent = '已连接'; $('#conn').className = 'tag done'; });
  es.addEventListener('error', () => { $('#conn').textContent = '重连中'; $('#conn').className = 'tag err'; });
  es.addEventListener('snapshot', (m) => {
    const s = JSON.parse(m.data).data;
    state.projects.set(s.id, s);
    renderList();
    if (s.id === state.selected) renderStat();
  });
  es.addEventListener('error_ev', () => {});
  es.addEventListener('error', (m) => {
    if (m.data) { try { toast(JSON.parse(m.data).data.error); } catch (_) {} }
  });
}

let projES = null;
function select(id) {
  state.selected = id;
  $('#log').textContent = '';
  state.events.clear();
  state.order = [];
  clearStream();
  renderList();
  renderStat();
  unlockControls();
  if (projES) projES.close();
  projES = new EventSource(`/api/projects/${id}/events`);
  projES.addEventListener('snapshot', (m) => {
    const s = JSON.parse(m.data).data;
    state.projects.set(s.id, s);
    renderList();
    if (s.id === state.selected) renderStat();
  });
  projES.addEventListener('event', (m) => pushEvent(JSON.parse(m.data).data));
  projES.addEventListener('delta', (m) => pushDelta(JSON.parse(m.data).data));
  projES.addEventListener('clear', () => clearStream());
  projES.addEventListener('error', (m) => {
    if (m.data) { try { toast(JSON.parse(m.data).data.error); } catch (_) {} }
  });
}

// ── 交互 ──

function act(fn) {
  return async () => {
    if (!state.selected) { toast('先选一个项目'); return; }
    const p = state.projects.get(state.selected);
    if (isLocked(p)) { toast(holderLabel(p) + '：请先关闭那个进程'); return; }
    try { await fn(state.selected); } catch (e) { toast(e.message); }
  };
}

$('#np-go').onclick = async () => {
  const prompt = $('#np-prompt').value.trim();
  if (!prompt) { toast('写一句话需求'); return; }
  try {
    const r = await api('/api/projects', { id: $('#np-id').value.trim(), prompt });
    $('#np-prompt').value = '';
    $('#np-id').value = '';
    select(r.id);
    if (r.error) toast(r.error);
  } catch (e) { toast(e.message); }
};
$('#np-scan').onclick = async () => { await api('/api/projects'); connectGlobal(); };
$('#mt-go').onclick = async () => {
  const dir = $('#mt-dir').value.trim();
  if (!dir) { toast('填书目录绝对路径'); return; }
  try {
    const r = await api('/api/projects/mount', { dir });
    $('#mt-dir').value = '';
    select(r.id);
  } catch (e) { toast(e.message); }
};

$('#b-resume').onclick = act((id) => api(`/api/projects/${id}/resume`, {}));
$('#b-abort').onclick = act((id) => api(`/api/projects/${id}/abort`, {}));
$('#b-next').onclick = act((id) => api(`/api/projects/${id}/next`, {}));
$('#b-review').onclick = act((id) => api(`/api/projects/${id}/advance`, { mode: 'review' }));
$('#b-auto').onclick = act((id) => api(`/api/projects/${id}/advance`, { mode: 'auto' }));
$('#b-steer').onclick = act(async (id) => {
  const text = $('#steer').value.trim();
  if (!text) { toast('干预内容为空'); throw new Error('干预内容为空'); }
  await api(`/api/projects/${id}/steer`, { text });
  $('#steer').value = '';
});
$('#b-continue').onclick = act(async (id) => {
  const text = $('#steer').value.trim();
  await api(`/api/projects/${id}/continue`, { text });
  $('#steer').value = '';
});
$('#b-chapter').onclick = act(async (id) => {
  const p = state.projects.get(id);
  const n = (p && p.completed) || 0;
  if (!n) { toast('还没有已完成章节'); return; }
  const r = await api(`/api/projects/${id}/chapters/${n}`);
  showChapter(r.chapter, r.content);
});

$('#steer').addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); $('#b-steer').click(); }
});

(async function init() {
  try {
    const list = await api('/api/projects');
    for (const p of list) state.projects.set(p.id, p);
    renderList();
    if (list.length) select(list[0].id);
  } catch (e) { toast(e.message); }
  connectGlobal();
  setInterval(() => { if (state.selected) renderStat(); }, 5000);
})();

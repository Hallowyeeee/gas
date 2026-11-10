'use strict';
// 瓦斯行管理系統 前端（電腦版 + 師傅手機版）
// 原則：畫面先用手上的資料馬上顯示，背景再跟後端同步；送出的動作先排隊，網路斷了也不會掉。

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const money = n => { n = Number(n) || 0; return (n < 0 ? '-$' : '$') + Math.abs(n).toLocaleString('zh-TW'); };
const today = () => new Date(Date.now() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 10);
const hhmm = s => (s || '').slice(11, 16);
const store = {
  get(k, d) { try { const v = localStorage.getItem(k); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch { } },
  del(k) { try { localStorage.removeItem(k); } catch { } },
};
const CAT = { gas: '瓦斯', empty_cylinder: '空瓶', stove: '爐具', consumable: '耗材', service: '服務' };
const STATUS = { new: '新單', dispatched: '已派單', delivered: '已送達', cancelled: '已取消' };
const METHOD = { cash: '現金', transfer: '轉帳', check: '支票', other: '其他' };

const S = { me: null, live: true, online: true };

// ---------------------------------------------------------------------
// 與後端溝通
// ---------------------------------------------------------------------
async function api(method, path, body) {
  let r;
  try {
    r = await fetch(path, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch (e) {
    setOnline(false);
    throw Object.assign(new Error('網路連不上'), { network: true });
  }
  setOnline(true);
  const d = await r.json().catch(() => ({}));
  if (r.status === 401 && path !== '/api/login' && S.me) { S.me = null; store.del('me'); route(); }
  if (!r.ok) throw Object.assign(new Error(d.error || ('錯誤 ' + r.status)), { status: r.status });
  return d;
}

function setOnline(v) {
  if (S.online === v) return;
  S.online = v;
  const el = $('#net');
  if (el) { el.textContent = v ? '' : '⚠ 離線中，資料會在連上後自動送出'; el.classList.toggle('off', !v); }
  if (v) flush();
}

// 讀資料：先回傳上次存的，背景再更新；有變就重畫
async function data(path) {
  const key = 'c:' + path;
  const cached = store.get(key);
  const fresh = api('GET', path).then(d => {
    const changed = JSON.stringify(d) !== JSON.stringify(store.get(key));
    store.set(key, d);
    if (changed && cached != null) rerender();
    return d;
  });
  if (cached != null) { fresh.catch(() => { }); return cached; }
  return fresh;
}
function patch(path, fn) { const key = 'c:' + path; const v = store.get(key); if (v != null) store.set(key, fn(v)); }

// 送出的動作排隊，依序送；網路斷了就等連上再送
let outbox = store.get('outbox', []);
let flushing = false;
function queue(method, path, body, label) {
  outbox.push({ id: Date.now() + Math.random(), method, path, body, label });
  store.set('outbox', outbox);
  flush();
}
async function flush() {
  if (flushing) return;
  flushing = true;
  try {
    while (outbox.length) {
      const job = outbox[0];
      try {
        await api(job.method, job.path, job.body);
      } catch (e) {
        if (e.network) break;
        toast((job.label || '動作') + '失敗：' + e.message, 'bad');
      }
      outbox.shift();
      store.set('outbox', outbox);
    }
  } finally {
    flushing = false;
    updatePending();
    rerender();
  }
}
function updatePending() {
  const el = $('#pending');
  if (el) el.textContent = outbox.length ? `傳送中 ${outbox.length} 筆…` : '';
}
setInterval(() => { if (outbox.length) flush(); }, 15000);

let rerenderTimer;
function rerender() {
  if (!S.live) return;
  clearTimeout(rerenderTimer);
  rerenderTimer = setTimeout(() => route(true), 80);
}

function toast(msg, kind = '', action) {
  const t = document.createElement('div');
  t.className = 'toast ' + kind;
  t.textContent = msg;
  if (action) {
    const b = document.createElement('button');
    b.className = 'toast-btn'; b.textContent = action.label;
    b.onclick = () => { t.remove(); action.fn(); };
    t.appendChild(b);
  }
  $('#toast').appendChild(t);
  setTimeout(() => t.remove(), action ? 10000 : kind === 'bad' ? 6000 : 3000);
}

// ---------------------------------------------------------------------
// 一鍵傳 LINE：把派單內容整理好，複製並打開 LINE，貼到師傅群組送出
// ---------------------------------------------------------------------
function lineText(o) {
  const items = (o.items && o.items.length) ? o.items.map(i => `${i.product_name}×${i.qty}`).join('、') : (o.summary || '');
  const where = [o.address, o.floor, o.has_elevator === 0 ? '無電梯' : ''].filter(Boolean).join('・');
  return [
    `【派單】${o.driver_name || ''}`.trim(),
    `單號：${o.order_no || ''}`,
    `客戶：${o.customer_name || ''}`,
    o.delivery_date && (o.delivery_date !== today() || o.delivery_slot) ? `送貨：${dayText(o.delivery_date)} ${slotText(o.delivery_slot)}` : '',
    o.phone ? `電話：${o.phone}` : '',
    `地址：${where}`,
    items ? `品項：${items}` : '',
    o.billing_type === 'monthly' ? `金額：${money(o.total_amount)}（月結，不用收錢）` : `收款：${money(o.total_amount)}${o.pay_method === 'transfer' ? '（轉帳，不用收現金）' : ''}`,
    o.note ? `備註：${o.note}` : '',
    o.address ? `地圖：https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(o.address)}` : '',
  ].filter(Boolean).join('\n');
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); return true; } catch (e) { /* 改用舊方法 */ }
  const ta = document.createElement('textarea');
  ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  let ok = false; try { ok = document.execCommand('copy'); } catch (e) { /* 不支援 */ }
  ta.remove();
  return ok;
}

async function sendLine(order) {
  let o = order;
  if (o.id && !(o.items && o.items.length)) {
    try { o = { ...o, ...(await api('GET', '/api/orders/' + o.id)) }; } catch (e) { /* 離線就用手上的資料 */ }
  }
  const text = lineText(o);
  if (/Android|iPhone|iPad|iPod/i.test(navigator.userAgent)) {
    location.href = 'https://line.me/R/share?text=' + encodeURIComponent(text);
    return;
  }
  const copied = await copyText(text);
  let opened = false;
  try { opened = (await api('POST', '/api/local/open-line', {})).ok; } catch (e) { /* 不是在店裡電腦 */ }
  if (!copied) { prompt('請複製下面的文字，貼到 LINE：', text); return; }
  toast(opened ? '訊息已複製，LINE 打開後選師傅群組，按 Ctrl+V 貼上送出' : '訊息已複製，到 LINE 師傅群組按 Ctrl+V 貼上送出', 'ok');
}
const lineAction = o => ({ label: '傳 LINE', fn: () => sendLine(o) });

function beep() {
  try {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    [0, 0.25].forEach(at => {
      const o = ctx.createOscillator(), g = ctx.createGain();
      o.frequency.value = 880; o.connect(g); g.connect(ctx.destination);
      g.gain.setValueAtTime(0.25, ctx.currentTime + at);
      o.start(ctx.currentTime + at); o.stop(ctx.currentTime + at + 0.18);
    });
  } catch { }
}

// 即時更新
let es;
function connectEvents() {
  if (es || !window.EventSource) return;
  es = new EventSource('/api/events');
  es.onmessage = ev => {
    let e; try { e = JSON.parse(ev.data); } catch { return; }
    if (e.type === 'call' && S.me && S.me.role !== 'driver') { showCall(e.call); return; }
    if (e.type === 'capture' && S.me && S.me.role !== 'driver') { beep(); showCapture(e.capture.id); return; }
    if (e.type === 'dispatch') {
      beep();
      toast('🔔 新派單：' + e.message, 'ok');
      if (document.hidden && window.Notification && Notification.permission === 'granted') {
        navigator.serviceWorker?.ready.then(r => r.showNotification('新派單', { body: e.message, tag: 'order-' + (e.order && e.order.id) }));
      }
    }
    rerender();
  };
  es.onopen = () => { setOnline(true); checkCaptures(); };
}
setInterval(() => rerender(), 60000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) { flush(); rerender(); } });
window.addEventListener('online', () => flush());

// ---------------------------------------------------------------------
// 來電顯示：電話一響，右下角跳出客人資料
// ---------------------------------------------------------------------
const callSoundOn = () => store.get('callSound', true);
function ring() {
  if (!callSoundOn()) return;
  try {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    [0, 0.12, 0.24, 0.7, 0.82, 0.94].forEach(at => {
      const o = ctx.createOscillator(), g = ctx.createGain();
      o.frequency.value = at < 0.5 ? 988 : 784; o.connect(g); g.connect(ctx.destination);
      g.gain.setValueAtTime(0.22, ctx.currentTime + at);
      o.start(ctx.currentTime + at); o.stop(ctx.currentTime + at + 0.09);
    });
  } catch { }
}
const fmtPhone = p => { p = String(p || ''); return /^09\d{8}$/.test(p) ? p.replace(/(\d{4})(\d{3})(\d{3})/, '$1-$2-$3') : /^0\d{9}$/.test(p) ? p.replace(/(\d{2})(\d{4})(\d{4})/, '($1) $2-$3') : p; };

function callCustomerHTML(c) {
  const lo = c.last_order;
  const days = lo ? (lo.days_ago <= 0 ? '今天' : lo.days_ago + ' 天前') : '';
  const late = lo && c.refill_days && lo.days_ago >= c.refill_days;
  return `<div class="call-name">${esc(c.name)}</div>
    <div class="call-addr">${esc(c.address)}${c.floor ? '・' + esc(c.floor) : ''}${c.has_elevator === 0 ? '・<b>無電梯</b>' : c.has_elevator === 1 ? '・有電梯' : ''}</div>
    <div class="row" style="gap:6px;margin:6px 0">
      ${c.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : '<span class="pill">現結</span>'}
      ${c.balance > 0 ? `<span class="pill bad">欠 ${money(c.balance)}</span>` : ''}
      ${c.needs_invoice ? `<span class="pill">開發票${c.tax_id ? ' ' + esc(c.tax_id) : ''}</span>` : ''}
      ${c.default_driver_name ? `<span class="pill">固定師傅 ${esc(c.default_driver_name)}</span>` : ''}
    </div>
    ${c.open_orders > 0 ? `<div class="call-warn">⚠ 已經有 ${c.open_orders} 張單還沒送到，可能是來催的</div>` : ''}
    <div class="call-last">${lo
      ? `上次 <b>${days}</b>（${esc((lo.delivered_at || lo.ordered_at || '').slice(5, 10).replace('-', '/'))}）${late ? ' <span class="pill accent">該換瓶了</span>' : ''}<br>
         ${esc(lo.items || '')}・${money(lo.total_amount)}${lo.driver_name ? '・' + esc(lo.driver_name) + ' 送' : ''}`
      : '還沒有訂過'}</div>
    ${c.note ? `<div class="muted">📝 ${esc(c.note)}</div>` : ''}`;
}

// 好幾線同時打來：來電排在記單頁左邊，一通一通處理
S.calls = (store.get('calls', []) || []).filter(c => c.day === today());
const saveCalls = () => store.set('calls', S.calls);
function showCall(call) {
  S.calls = [{ ...call, day: today(), ts: Date.now(), at: new Date().toTimeString().slice(0, 5), state: 'wait' }, ...S.calls.filter(x => x.id !== call.id)].slice(0, 30);
  saveCalls();
  ring();
  const c = (call.matches || [])[0];
  if (document.hidden && window.Notification && Notification.permission === 'granted') {
    navigator.serviceWorker?.ready.then(r => r.showNotification('來電 ' + fmtPhone(call.phone), { body: c ? c.name + '｜' + c.address : '新客人', tag: 'call-' + call.id }));
  }
  if (S.newPage) S.newPage.onCall(call);
  else location.hash = '#/home';
}
function callRowHTML(k, active) {
  const c = (k.matches || [])[0];
  const lo = c && c.last_order;
  return `<div class="callrow ${k.state} ${active ? 'on' : ''}" data-call="${k.id}">
    <div class="row between"><span><b class="tel">${esc(fmtPhone(k.phone))}</b> <span class="muted">${esc(k.at)}</span>${k.line ? ` <span class="pill accent">第 ${esc(k.line)} 線</span>` : ''}${k.source === 'test' ? ' <span class="pill">模擬</span>' : ''}</span>
      <button class="ghost x" data-dismiss="${k.id}" title="移除">✕</button></div>
    ${c ? `<div><b>${esc(c.name)}</b>${(k.matches || []).length > 1 ? ` <span class="muted">等 ${k.matches.length} 位</span>` : ''}・<span class="muted">${esc(c.address)}${c.floor ? ' ' + esc(c.floor) : ''}</span></div>
      <div class="row" style="gap:4px;margin-top:4px">
        ${k.state === 'done' ? `<span class="pill ok">✓ ${esc(k.order_no || '已處理')}</span>` : ''}
        ${c.open_orders > 0 && k.state !== 'done' ? `<span class="pill bad">有 ${c.open_orders} 張還沒送</span>` : ''}
        ${c.balance > 0 ? `<span class="pill bad">欠 ${money(c.balance)}</span>` : ''}
        ${c.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : ''}
        ${lo ? `<span class="pill">上次 ${lo.days_ago <= 0 ? '今天' : lo.days_ago + ' 天前'}</span>` : ''}</div>`
      : `<div><b>新客人</b> <span class="muted">按一下建檔</span></div>${k.state === 'done' ? `<span class="pill ok">✓ ${esc(k.order_no || '已處理')}</span>` : ''}`}
  </div>`;
}

async function callSettingsCard() {
  const [calls, box] = await Promise.all([api('GET', '/api/calls').catch(() => []), api('GET', '/api/callerid').catch(() => null)]);
  return `<div class="card" id="callcard"><h3 style="margin-top:0">☎ 來電顯示</h3>
    <div>來電顯示盒：<b>${esc(box ? box.status : '讀不到狀態')}</b>${box && box.port ? ` <span class="muted">（${esc(box.port)}，${box.baud}）</span>` : ''}</div>
    ${S.me.role === 'admin' ? `<details id="detect" ${box && box.detecting ? 'open' : ''}><summary>偵測來電盒（找出盒子接在哪裡、送什麼資料）</summary>
      <ol class="muted" style="padding-left:20px;margin:6px 0"><li>確認盒子插在這台電腦，而且盒子的原廠軟體已經關掉</li><li>按「開始偵測」，接著用手機打一通到店裡電話，響兩三聲就可以掛掉</li><li>把這個畫面截圖傳給 Claude</li></ol>
      <div class="row"><select id="baud" style="width:auto">${[9600, 1200, 2400, 4800, 19200, 38400, 115200].map(b => `<option ${box && box.detect_baud === b ? 'selected' : ''}>${b}</option>`).join('')}</select>
        <button class="primary" id="detectGo">開始偵測（90 秒）</button><span id="detectLeft" class="muted"></span></div>
      <div id="detectOut"></div></details>` : ''}
    <label style="color:var(--text)"><input type="checkbox" id="callSound" ${callSoundOn() ? 'checked' : ''}>來電時響提示音（這台電腦）</label>
    <div class="row" style="margin-top:8px"><input id="testPhone" placeholder="輸入電話號碼，例如 0912345678" style="max-width:280px" inputmode="tel"><button id="testCall">模擬來電</button></div>
    <h3>今天的來電 <span class="pill">${calls.length}</span></h3>
    ${calls.length ? `<div class="table-wrap"><table><tr><th>時間</th><th>號碼</th><th>客戶</th><th>有沒有下單</th></tr>
      ${calls.map(l => `<tr><td>${hhmm(l.received_at)}</td><td>${esc(fmtPhone(l.phone))}${l.source === 'test' ? ' <span class="pill">模擬</span>' : ''}</td>
        <td>${l.customer_id ? `<a href="#/customers/${l.customer_id}">${esc(l.customer_name)}</a>` : '<span class="muted">新客人</span>'}</td>
        <td>${l.order_no ? '<span class="pill ok">' + esc(l.order_no) + '</span>' : '<span class="pill">沒有</span>'}</td></tr>`).join('')}</table></div>`
      : '<div class="muted">今天還沒有來電</div>'}</div>`;
}
function detectHTML(b) {
  const ports = b.detect_ports || [];
  return `<h3>找到的 COM 埠</h3>${ports.length ? ports.map(p => `<div>${esc(p.Port || p.port)}：${p.error ? '<span style="color:var(--bad)">打不開（' + esc(p.error) + '）</span>' : `收到 ${p.bytes} 個位元組`}</div>`).join('')
      : (b.ports || []).length ? (b.ports || []).map(esc).join('、') + '<div class="muted">按「開始偵測」開始聽</div>' : '<div class="muted">這台電腦沒有 COM 埠。盒子可能是另一種（USB 裝置），請把下面的裝置清單截圖。</div>'}
    <h3>收到的資料</h3>${(b.recent || []).length ? `<div class="table-wrap"><table><tr><th>時間</th><th>埠</th><th>內容</th><th>抓到號碼</th><th></th></tr>
      ${b.recent.map(r => `<tr><td>${esc(r.at)}</td><td>${esc(r.port)}</td><td><div style="word-break:break-all">${esc(r.text)}</div><div class="muted" style="font-family:monospace;font-size:12px;word-break:break-all">${esc(r.hex)}</div></td>
        <td>${r.phone ? '<b>' + esc(r.phone) + '</b>' : '<span class="muted">沒有</span>'}</td>
        <td>${r.phone ? `<button data-use="${esc(r.port)}">就用這個</button>` : ''}</td></tr>`).join('')}</table></div>` : '<div class="muted">還沒收到，打一通電話進來試試看</div>'}
    ${(b.devices || []).length ? `<h3>電腦上的裝置</h3><div class="muted" style="font-size:12px;font-family:monospace;white-space:pre-wrap">${b.devices.map(esc).join('\n')}</div>` : ''}`;
}
let detectTimer;
function bindCallSettings(view) {
  const cb = $('#callSound', view); if (!cb) return;
  const out = $('#detectOut');
  const poll = async () => {
    clearTimeout(detectTimer);
    if (!document.body.contains(out)) return;
    const b = await api('GET', '/api/callerid').catch(() => null);
    if (!b) return;
    out.innerHTML = detectHTML(b);
    $('#detectLeft').textContent = b.detecting ? `偵測中，還剩 ${b.detect_left} 秒…` : '';
    $('#detectGo').disabled = b.detecting;
    if (b.detecting) detectTimer = setTimeout(poll, 1000);
  };
  if (out) {
    poll();
    $('#detectGo').onclick = async () => {
      try { await api('POST', '/api/callerid/detect', { baud: +$('#baud').value }); poll(); } catch (e) { toast(e.message, 'bad'); }
    };
    out.onclick = async e => {
      const b = e.target.closest('[data-use]'); if (!b) return;
      if (!confirm(`以後都從 ${b.dataset.use} 讀來電？`)) return;
      try { await api('POST', '/api/callerid/use', { port: b.dataset.use, baud: +$('#baud').value }); toast('已設定，接下來的來電會自動跳出', 'ok'); setTimeout(() => settings(view), 800); } catch (err) { toast(err.message, 'bad'); }
    };
  }
  cb.onchange = () => store.set('callSound', cb.checked);
  const go = async () => {
    const v = $('#testPhone').value.trim();
    if (!v) return toast('請輸入電話號碼', 'bad');
    try {
      await api('POST', '/api/calls/test', { phone: v });
      $('#callcard').outerHTML = await callSettingsCard(); bindCallSettings(view);
    } catch (e) { toast(e.message, 'bad'); }
  };
  $('#testCall').onclick = go;
  $('#testPhone').onkeydown = e => { if (e.key === 'Enter') go(); };
}

// ---------------------------------------------------------------------
// 路由
// ---------------------------------------------------------------------
// 電腦版只有 5 大塊，點進去是卡片；設定放右上角齒輪
const OFFICE_NAV = [['#/home', '🏠 首頁'], ['#/dispatch', '🚚 派送'], ['#/customers', '👥 客戶'], ['#/money', '💰 帳務'], ['#/stock', '📦 商品庫存']];
// 每個頁面屬於哪一大塊（上面選單要亮哪一個）
const SECTION = {
  home: 'home', new: 'home', calls: 'home',
  dispatch: 'dispatch', today: 'dispatch', shifts: 'dispatch', orders: 'dispatch', order: 'dispatch',
  customers: 'customers',
  money: 'money', dashboard: 'money', reconcile: 'money',
  stock: 'stock', products: 'stock', stocklist: 'stock',
};
const SLOT = { am: '上午', pm: '下午', eve: '晚上' };
const SLOTS = ['am', 'pm', 'eve'];
const slotText = s => SLOT[s] || '不指定';
const addDays = (d, n) => { const x = new Date(d + 'T12:00:00'); x.setDate(x.getDate() + n); return x.getFullYear() + '-' + String(x.getMonth() + 1).padStart(2, '0') + '-' + String(x.getDate()).padStart(2, '0'); };
const WEEK = '日一二三四五六';
const dayText = d => { if (!d) return ''; const t = today(); const w = WEEK[new Date(d + 'T12:00:00').getDay()];
  return `${+d.slice(5, 7)}/${+d.slice(8, 10)}（${w}）${d === t ? ' 今天' : d === addDays(t, 1) ? ' 明天' : d === addDays(t, -1) ? ' 昨天' : ''}`; };

function shell(nav) {
  document.body.className = S.me.role === 'driver' ? 'driver' : 'office';
  document.body.innerHTML = `
    <header class="top"><div class="top-inner">
      <div class="brand"><img src="/favicon.png" alt="" style="width:28px;height:28px;border-radius:6px;vertical-align:middle;margin-right:6px">瓦斯行<b>系統</b></div>
      <nav class="nav">${nav.map(([h, t]) => `<a href="${h}">${t}</a>`).join('')}</nav>
      <div class="spacer"></div>
      <span class="net" id="pending"></span><span class="net" id="net"></span>
      <span class="muted" style="white-space:nowrap">${esc(S.me.name)}</span>
      ${S.me.role === 'driver' ? '' : '<a class="gear" href="#/settings" title="設定">⚙️</a>'}
      <button class="ghost" id="logout">登出</button>
    </div></header>
    <main id="view"></main><div id="toast"></div><dialog id="dlg"></dialog>`;
  $('#logout').onclick = async () => {
    if (outbox.length && !confirm('還有資料沒送出，確定登出？')) return;
    await api('POST', '/api/logout').catch(() => { });
    localStorage.clear(); location.hash = ''; location.reload();
  };
  setOnline(S.online); updatePending();
}

let lastShell = '';
async function route(soft) {
  if (!S.me) return showLogin();
  const driver = S.me.role === 'driver';
  const nav = driver ? [['#/my', '我的單'], ['#/my-done', '今天已送']] : OFFICE_NAV;
  if (lastShell !== S.me.role + S.me.id) {
    shell(nav); lastShell = S.me.role + S.me.id; connectEvents();
    if (S.me.role === 'admin') api('GET', '/api/system').then(sys => {
      if (sys.update && sys.update.available) $('.nav').insertAdjacentHTML('beforeend', '<a href="#/settings" class="upd" style="color:var(--accent);font-weight:600">⬆ 有新版</a>');
    }).catch(() => { });
  }
  let h = location.hash || (driver ? '#/my' : '#/home');
  const [, page, arg] = h.split('/');
  if (driver) $$('.nav a').forEach(a => a.classList.toggle('on', h === a.getAttribute('href') || h.startsWith(a.getAttribute('href') + '/')));
  else $$('.nav a:not(.upd)').forEach(a => a.classList.toggle('on', a.getAttribute('href') === '#/' + (SECTION[page] || '')));
  const gear = $('.gear'); if (gear) gear.classList.toggle('on', page === 'settings');
  const view = $('#view');
  const views = driver
    ? { my: arg ? driverDeliver : driverJobs, 'my-done': driverDone }
    : {
      home: officeNew, new: officeNew, calls: callHistory,
      dispatch: hubDispatch, today: todayBoard, shifts: shiftsPage, orders: ordersQuery, order: orderDetail,
      customers: arg ? customerDetail : customerList,
      money: hubMoney, dashboard: dashboardPage, reconcile,
      stock: hubStock, products: productList, stocklist: stockList,
      settings,
    };
  const fn = views[page] || (driver ? driverJobs : officeNew);
  if (soft && !S.live) return;
  S.live = true;
  try { await fn(view, arg); }
  catch (e) { if (!soft) view.innerHTML = `<div class="card">讀取失敗：${esc(e.message)}　<button onclick="route()">重試</button></div>`; }
}
window.addEventListener('hashchange', () => { S.live = true; route(); });

// ---------------------------------------------------------------------
// 登入 / 第一次設定
// ---------------------------------------------------------------------
async function showLogin() {
  lastShell = '';
  document.body.className = '';
  let setup = { needed: false };
  try { setup = await api('GET', '/api/setup'); } catch { }
  document.body.innerHTML = `<main class="center"><h2><img src="/icon-192.png" alt="" style="width:56px;height:56px;border-radius:12px;vertical-align:middle;margin-right:8px">瓦斯行管理系統</h2>
    <div class="card" id="box"></div></main><div id="toast"></div>`;
  const box = $('#box');
  if (setup.needed && !setup.local) { box.innerHTML = '<p>系統還沒設定，請先在店裡電腦打開程式建立老闆帳號。</p>'; return; }
  box.innerHTML = setup.needed ? `
    <h3 style="margin-top:0">第一次使用：建立老闆帳號</h3>
    <form><label>你的名字</label><input name="name" required>
    <label>登入帳號（英文或數字）</label><input name="login_name" required autocomplete="username">
    <label>密碼（至少 6 個字）</label><input name="password" type="password" minlength="6" required autocomplete="new-password">
    <p><button class="primary lg">建立並登入</button></p></form>` : `
    <form><label>帳號</label><input name="login_name" required autocomplete="username" autocapitalize="none">
    <label>密碼</label><input name="password" type="password" required autocomplete="current-password">
    <p><button class="primary lg">登入</button></p></form>`;
  $('form', box).onsubmit = async e => {
    e.preventDefault();
    const body = Object.fromEntries(new FormData(e.target));
    try {
      const r = await api('POST', setup.needed ? '/api/setup' : '/api/login', body);
      S.me = r.user; store.set('me', r.user); location.hash = ''; route();
    } catch (err) { toast(err.message, 'bad'); }
  };
}

// ---------------------------------------------------------------------
// 大塊首頁：卡片
// ---------------------------------------------------------------------
function cardGrid(title, sub, cards) {
  return `<h2>${title}</h2>${sub ? `<p class="muted" style="margin:-8px 0 14px">${sub}</p>` : ''}<div class="hub">${cards.map(c => `
    <a class="hubcard${c.hl ? ' hl' : ''}" href="${c.href}">${c.count != null && c.count !== '' ? `<span class="n">${c.count}</span>` : ''}
      <div class="ic">${c.icon}</div><h3>${c.title}</h3><p>${c.desc}</p></a>`).join('')}</div>`;
}

async function hubDispatch(view) {
  const t = today(), tm = addDays(t, 1);
  const [open, tomorrow, done] = await Promise.all([
    data('/api/orders?open_until=' + t), data('/api/orders?status=new&delivery_date=' + tm), data('/api/orders?status=delivered&date=' + t)]);
  view.innerHTML = cardGrid('派送', '點一張卡片進去', [
    { href: '#/today', icon: '📋', title: '今日待辦', desc: '還沒派的單、每位師傅的單；看板拖拉派單，或切成表格看金額合計', count: open.length || '', hl: open.length > 0 },
    { href: '#/today/' + tm, icon: '📅', title: '明日預約', desc: '明天要送的單，可以先排好', count: tomorrow.length || '' },
    { href: '#/shifts', icon: '🗓️', title: '排班', desc: '每位師傅上午／下午／晚上有沒有班' },
    { href: '#/orders', icon: '🔎', title: '訂單查詢', desc: '依配送日期查訂單，看新單、已派、已送達、已取消' },
    { href: '#/orders/delivered', icon: '✅', title: '今天已送', desc: '送達、收款回報', count: done.length || '' },
  ]);
}

async function hubMoney(view) {
  const [debt, bal] = await Promise.all([data('/api/orders?debt=cash'), data('/api/reports/customer-balance')]);
  const monthly = bal.filter(b => b.billing_type === 'monthly' && b.balance > 0);
  view.innerHTML = cardGrid('帳務', '', [
    { href: '#/dashboard', icon: '📊', title: '儀表板', desc: '今天完成多少、接單派送量、本週本月營業額、每月長條圖' },
    { href: '#/reconcile', icon: '🧾', title: '每日對帳', desc: '每位師傅收了多少、該交多少、交了沒' },
    { href: '#/orders/cashdebt', icon: '⚠️', title: '現金欠款', desc: '送到了但錢沒收齊的現結單', count: debt.length || '', hl: debt.length > 0 },
    { href: '#/orders/monthlydebt', icon: '📒', title: '月結欠款', desc: '月結客戶目前還沒付的錢', count: monthly.length ? money(monthly.reduce((s, b) => s + b.balance, 0)) : '' },
  ]);
}

async function hubStock(view) {
  const [products, stock] = await Promise.all([data('/api/products?all=1'), data('/api/stock')]);
  const low = stock.filter(x => x.is_low).length;
  view.innerHTML = cardGrid('商品庫存', '', [
    { href: '#/products', icon: '🏷️', title: '商品', desc: '瓦斯、空瓶、爐具、耗材的價格和上架', count: products.filter(p => p.active).length },
    { href: '#/stocklist', icon: '🛢️', title: '庫存', desc: '每樣東西還剩多少，低於提醒數量會標紅', count: low ? '低 ' + low : '', hl: low > 0 },
  ]);
}

async function stockList(view) {
  const list = await data('/api/stock');
  view.innerHTML = `<p><a href="#/stock">← 商品庫存</a></p><h2>庫存</h2>
    <div class="card table-wrap"><table><tr><th>名稱</th><th>分類</th><th class="num">庫存</th><th class="num">低於幾個提醒</th></tr>
    ${list.map(x => `<tr class="${x.is_low ? 'bad' : ''}"><td>${esc(x.name)}</td><td>${CAT[x.category] || ''}</td><td class="num">${x.qty} ${esc(x.unit || '')}</td><td class="num">${x.low_stock_alert ?? ''}</td></tr>`).join('')
    || '<tr><td class="muted" colspan="4">還沒有要算庫存的商品</td></tr>'}</table></div>
    <p class="muted">要調整庫存（盤點），到「商品」點那樣東西。</p>`;
}

// ---------------------------------------------------------------------
// 今日待辦：左邊待派、右邊每位師傅（看板拖拉派單，或表格看合計）
// ---------------------------------------------------------------------
async function todayBoard(view, arg) {
  const t = today();
  const date = /^\d{4}-\d{2}-\d{2}$/.test(arg || '') ? arg : t;
  const isToday = date === t;
  const mode = store.get('boardMode', 'board');
  const lists = await Promise.all([
    data('/api/drivers'), data('/api/shifts?date=' + date), data('/api/orders?delivery_date=' + date),
    isToday ? data('/api/orders?open_until=' + t) : [], isToday ? data('/api/orders?status=dispatched') : []]);
  const [drivers, shifts] = lists;
  const seen = new Set(); const orders = [];
  for (const o of [...lists[2], ...lists[3], ...lists[4]]) if (!seen.has(o.id) && o.status !== 'cancelled') { seen.add(o.id); orders.push(o); }
  const st = { orders, shifts: shifts.map(x => x.driver_id + ':' + x.slot) };
  const onShift = (d, s) => st.shifts.includes(d + ':' + s);
  const late = o => o.delivery_date && o.delivery_date < date ? `<span class="pill bad">逾期 ${esc(o.delivery_date.slice(5).replace('-', '/'))}</span>` : '';
  const card = o => `<div class="tcard ${o.status}${o._pending ? ' pending' : ''}" draggable="${o.status === 'delivered' ? 'false' : 'true'}" data-o="${o.id}">
      <div class="row between" style="flex-wrap:nowrap"><b class="grow" style="overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${esc(o.address)}</b><span class="money">${money(o.total_amount)}</span></div>
      <div>${esc(o.summary || '')}</div>
      <div class="row" style="gap:4px"><span class="muted grow">${esc(o.customer_name)}</span>${late(o)}
        ${o.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : o.pay_method === 'transfer' ? '<span class="pill blue">轉帳</span>' : ''}
        ${o.status === 'delivered' ? '<span class="pill ok">✓ 已送</span>' : ''}</div>
      ${o.note ? `<div class="muted" style="font-size:13px">📝 ${esc(o.note)}</div>` : ''}
      ${o.status === 'new' ? `<select class="pick" data-pick="${o.id}"><option value="">派給…</option>${drivers.map(d => `<option value="${d.id}">${esc(d.name)}${onShift(d.id, o.delivery_slot || 'am') || onShift(d.id, 'pm') || onShift(d.id, 'eve') ? '' : '（沒排班）'}</option>`).join('')}</select>` : ''}
    </div>`;
  const draw = () => {
    const pend = st.orders.filter(o => o.status === 'new');
    const groups = [...SLOTS, ''].map(s => [s, pend.filter(o => (o.delivery_slot || '') === s)]).filter(([, l]) => l.length);
    const work = drivers.map(d => ({ d, list: st.orders.filter(o => o.driver_id === d.id && o.status !== 'new'), on: SLOTS.filter(s => onShift(d.id, s)) }))
      .sort((a, b) => (b.on.length > 0) - (a.on.length > 0));
    const sum = l => ({ n: l.length, qty: l.reduce((s, o) => s + (o.gas_qty || 0), 0), amt: l.reduce((s, o) => s + o.total_amount, 0), paid: l.reduce((s, o) => s + (o.paid_amount || 0), 0) });
    const right = mode === 'table'
      ? `<div class="card table-wrap"><table class="ttable"><tr><th>單號</th><th>時段</th><th>客戶</th><th>電話</th><th>地址</th><th>品項</th><th class="num">瓦斯</th><th class="num">金額</th><th class="num">實收</th><th>狀態</th><th>備註</th></tr>
        ${work.filter(w => w.list.length || w.on.length).map(w => { const x = sum(w.list); return `<tr class="drv"><td colspan="11"><b>${esc(w.d.name)}</b> <span class="muted">${w.on.length ? w.on.map(slotText).join('、') + '有班' : '沒排班'}・${w.list.filter(o => o.status === 'delivered').length}/${w.list.length} 完成</span></td></tr>
          ${w.list.map(o => `<tr class="click" data-open="${o.id}"><td>${esc(o.order_no)}</td><td>${slotText(o.delivery_slot)}</td><td>${esc(o.customer_name)}</td><td>${esc(o.phone || '')}</td><td>${esc(o.address)}</td><td>${esc(o.summary || '')}</td>
            <td class="num">${o.gas_qty || ''}</td><td class="num">${money(o.total_amount)}</td><td class="num">${o.status === 'delivered' ? money(o.paid_amount) : ''}</td><td>${STATUS[o.status]}</td><td class="muted">${esc(o.note || '')}</td></tr>`).join('') || '<tr><td colspan="11" class="muted">沒有單</td></tr>'}
          <tr class="sub"><td colspan="6">小計 ${x.n} 單</td><td class="num">${x.qty}</td><td class="num">${money(x.amt)}</td><td class="num">${money(x.paid)}</td><td colspan="2"></td></tr>`; }).join('')}
        ${pend.length ? `<tr class="drv"><td colspan="11"><b>還沒派</b> <span class="muted">${pend.length} 單</span></td></tr>` : ''}
        ${(() => { const x = sum(st.orders); return `<tr class="total"><td colspan="6">全日合計 ${x.n} 單</td><td class="num">${x.qty}</td><td class="num">${money(x.amt)}</td><td class="num">${money(x.paid)}</td><td colspan="2"></td></tr>`; })()}</table></div>`
      : `<div class="lanes">${work.map(w => `<section class="lane${w.on.length ? '' : ' off'}">
          <div class="lane-head"><b>${esc(w.d.name)}</b><span class="muted">${w.list.filter(o => o.status === 'delivered').length}/${w.list.length} 完成</span></div>
          <div class="shiftchips">${SLOTS.map(s => `<button class="chip${onShift(w.d.id, s) ? ' on' : ''}" data-shift="${w.d.id}" data-slot="${s}">${SLOT[s]}${onShift(w.d.id, s) ? '有班' : ''}</button>`).join('')}</div>
          ${[...SLOTS, ''].map(s => { const l = w.list.filter(o => (o.delivery_slot || '') === s); if (s === '' && !l.length) return '';
            return `<div class="slot-h">${slotText(s)} <span class="muted">${l.length}</span></div><div class="drop" data-drop="${w.d.id}" data-slot="${s}">${l.map(card).join('') || '<div class="muted hint">把單拖到這裡</div>'}</div>`; }).join('')}
        </section>`).join('') || '<div class="card">還沒有師傅帳號，請到右上角 ⚙️ 設定新增師傅。</div>'}</div>`;
    view.innerHTML = `<div class="row between" style="margin-bottom:10px"><div class="row">
        <button class="ghost" data-day="${addDays(date, -1)}">‹</button><h2 style="margin:0">${dayText(date)}</h2><button class="ghost" data-day="${addDays(date, 1)}">›</button>
        <div class="seg2"><a class="${isToday ? 'on' : ''}" href="#/today">今日</a><a class="${date === addDays(t, 1) ? 'on' : ''}" href="#/today/${addDays(t, 1)}">明日預約</a></div></div>
      <div class="row"><div class="seg2"><a class="${mode === 'board' ? 'on' : ''}" data-mode="board">看板</a><a class="${mode === 'table' ? 'on' : ''}" data-mode="table">表格</a></div>
        <a class="btn" href="#/shifts/${date}">排班</a><a class="btn primary" href="#/home">＋ 記單</a></div></div>
      <div class="today-grid"><aside class="card pendcol drop" data-drop="" data-slot="">
        <div class="row between"><b>待派 <span class="pill accent">${pend.length}</span></b></div>
        ${groups.map(([s, l]) => `<div class="slot-h">${slotText(s)} <span class="muted">${l.length}</span></div>${l.map(card).join('')}`).join('') || '<div class="muted hint">全部派完了 👍</div>'}
      </aside><div>${right}</div></div>`;
  };
  const dispatch = (oid, driverId, slot) => {
    const o = st.orders.find(x => x.id === oid); if (!o) return;
    const d = drivers.find(x => x.id === driverId);
    if (!driverId) {
      if (o.status !== 'dispatched') return;
      Object.assign(o, { status: 'new', driver_id: null, driver_name: null, _pending: true });
      queue('POST', `/api/orders/${oid}/undispatch`, {}, '收回派單');
      toast('已收回到待派');
    } else {
      if (o.driver_id === driverId && (o.delivery_slot || '') === slot && o.status === 'dispatched') return;
      const body = { driver_id: driverId }; if (slot !== undefined) body.delivery_slot = slot;
      Object.assign(o, { status: 'dispatched', driver_id: driverId, driver_name: d.name, _pending: true }, slot !== undefined ? { delivery_slot: slot || null } : {});
      queue('POST', `/api/orders/${oid}/dispatch`, body, '派單');
      toast(`已派給 ${d.name}${slot !== undefined ? '（' + slotText(slot) + '）' : ''}`, 'ok', lineAction({ ...o }));
    }
    draw();
  };
  draw();
  S.live = true;
  view.ondragstart = e => { const c = e.target.closest('.tcard'); if (!c) return; S.live = false; e.dataTransfer.setData('text/plain', c.dataset.o); e.dataTransfer.effectAllowed = 'move'; c.classList.add('dragging'); };
  view.ondragend = e => { S.live = true; $$('.over', view).forEach(x => x.classList.remove('over')); const c = e.target.closest('.tcard'); if (c) c.classList.remove('dragging'); };
  view.ondragover = e => { const z = e.target.closest('[data-drop]'); if (!z) return; e.preventDefault(); $$('.over', view).forEach(x => x !== z && x.classList.remove('over')); z.classList.add('over'); };
  view.ondrop = e => {
    const z = e.target.closest('[data-drop]'); if (!z) return; e.preventDefault();
    const oid = +e.dataTransfer.getData('text/plain');
    dispatch(oid, z.dataset.drop ? +z.dataset.drop : null, z.dataset.drop ? z.dataset.slot : undefined);
  };
  view.onchange = e => { const p = e.target.closest('[data-pick]'); if (p && p.value) dispatch(+p.dataset.pick, +p.value); };
  view.onclick = e => {
    const day = e.target.closest('[data-day]'); if (day) { location.hash = '#/today/' + day.dataset.day; return; }
    const m = e.target.closest('[data-mode]'); if (m) { store.set('boardMode', m.dataset.mode); todayBoard(view, arg); return; }
    const sh = e.target.closest('[data-shift]');
    if (sh) {
      const key = sh.dataset.shift + ':' + sh.dataset.slot, on = !st.shifts.includes(key);
      st.shifts = on ? [...st.shifts, key] : st.shifts.filter(k => k !== key);
      queue('PUT', '/api/shifts', { driver_id: +sh.dataset.shift, work_date: date, slot: sh.dataset.slot, on }, '排班');
      draw(); return;
    }
    if (e.target.closest('select')) return;
    const c = e.target.closest('[data-o],[data-open]'); if (c) location.hash = '#/order/' + (c.dataset.o || c.dataset.open);
  };
}

// 排班：一天一張表，師傅 × 上午／下午／晚上
async function shiftsPage(view, arg) {
  const date = /^\d{4}-\d{2}-\d{2}$/.test(arg || '') ? arg : today();
  const [drivers, shifts] = await Promise.all([data('/api/drivers'), data('/api/shifts?date=' + date)]);
  const on = new Set(shifts.map(x => x.driver_id + ':' + x.slot));
  const draw = () => {
    view.innerHTML = `<p><a href="#/dispatch">← 派送</a></p><div class="row between"><div class="row">
      <button class="ghost" data-day="${addDays(date, -1)}">‹</button><h2 style="margin:0">排班　${dayText(date)}</h2><button class="ghost" data-day="${addDays(date, 1)}">›</button></div>
      <div class="row"><button id="copy">照前一天排</button><a class="btn" href="#/today/${date}">看這天的單</a></div></div>
      <p class="muted">點一下切換有班／沒班。沒排班的師傅在「今日待辦」會排到最後面、顏色變淡。</p>
      <div class="card table-wrap"><table class="shift-table"><tr><th>師傅</th>${SLOTS.map(s => `<th>${SLOT[s]}</th>`).join('')}</tr>
      ${drivers.map(d => `<tr><td><b>${esc(d.name)}</b></td>${SLOTS.map(s => `<td><button class="chip big${on.has(d.id + ':' + s) ? ' on' : ''}" data-d="${d.id}" data-s="${s}">${on.has(d.id + ':' + s) ? '有班' : '未排'}</button></td>`).join('')}</tr>`).join('')
      || '<tr><td colspan="4" class="muted">還沒有師傅帳號</td></tr>'}</table></div>`;
  };
  draw();
  view.onclick = async e => {
    const day = e.target.closest('[data-day]'); if (day) { location.hash = '#/shifts/' + day.dataset.day; return; }
    if (e.target.id === 'copy') {
      if (!confirm(`把 ${dayText(addDays(date, -1))} 的班表整份套到 ${dayText(date)}？`)) return;
      try { const l = await api('POST', '/api/shifts/copy', { from: addDays(date, -1), to: date }); on.clear(); l.forEach(x => on.add(x.driver_id + ':' + x.slot)); store.del('c:/api/shifts?date=' + date); toast('已照前一天排好', 'ok'); draw(); }
      catch (err) { toast(err.message, 'bad'); }
      return;
    }
    const b = e.target.closest('[data-d]'); if (!b) return;
    const key = b.dataset.d + ':' + b.dataset.s, v = !on.has(key);
    v ? on.add(key) : on.delete(key);
    queue('PUT', '/api/shifts', { driver_id: +b.dataset.d, work_date: date, slot: b.dataset.s, on: v }, '排班');
    draw();
  };
}

// ---------------------------------------------------------------------
// 訂單查詢：依配送日期區間；分頁看狀態、現金欠款、月結欠款
// ---------------------------------------------------------------------
const ORDER_TABS = [['all', '全部'], ['new', '還沒派'], ['dispatched', '配送中'], ['delivered', '已送達'], ['cancelled', '已取消'], ['cashdebt', '現金欠款'], ['monthlydebt', '月結欠款']];
async function ordersQuery(view, arg) {
  const tab = ORDER_TABS.some(([k]) => k === arg) ? arg : 'all';
  const r = store.get('orderRange', null);
  const range = r && r.from ? r : { from: today(), to: today() };
  const head = `<p><a href="#/dispatch">← 派送</a></p><h2>訂單查詢</h2>
    <div class="tabs">${ORDER_TABS.map(([k, t]) => `<a class="${k === tab ? 'on' : ''}" href="#/orders/${k}">${t}</a>`).join('')}</div>`;
  if (tab === 'monthlydebt') {
    const bal = (await data('/api/reports/customer-balance')).filter(b => b.billing_type === 'monthly' && b.balance > 0);
    view.innerHTML = head + `<div class="card table-wrap"><table><tr><th>客戶</th><th class="num">買了</th><th class="num">付了</th><th class="num">還欠</th></tr>
      ${bal.map(b => `<tr class="click" data-c="${b.customer_id}"><td>${esc(b.name)}</td><td class="num">${money(b.total_sales)}</td><td class="num">${money(b.total_paid)}</td><td class="num"><b>${money(b.balance)}</b></td></tr>`).join('')
      || '<tr><td class="muted" colspan="4">月結客戶都付清了 👍</td></tr>'}
      ${bal.length ? `<tr class="total"><td>合計 ${bal.length} 位</td><td></td><td></td><td class="num">${money(bal.reduce((s, b) => s + b.balance, 0))}</td></tr>` : ''}</table></div>`;
    view.onclick = e => { const tr = e.target.closest('[data-c]'); if (tr) location.hash = '#/customers/' + tr.dataset.c; };
    return;
  }
  const q = tab === 'cashdebt' ? '/api/orders?debt=cash&limit=1000'
    : `/api/orders?limit=1000&from=${range.from}&to=${range.to}${tab !== 'all' ? '&status=' + tab : ''}`;
  const list = await data(q);
  const t = today(), m0 = t.slice(0, 8) + '01', lm = addDays(m0, -1);
  view.innerHTML = head + (tab === 'cashdebt' ? '<p class="muted">送到了，但收的錢跟金額對不上的現結單（全部日期）。</p>' : `
    <div class="row" style="margin-bottom:10px"><span class="muted">配送日期</span><input type="date" id="from" value="${range.from}" style="width:auto"> 到 <input type="date" id="to" value="${range.to}" style="width:auto">
      <button data-r="${t},${t}">今天</button><button data-r="${addDays(t, 1)},${addDays(t, 1)}">明天</button><button data-r="${m0},${t}">本月</button><button data-r="${lm.slice(0, 8)}01,${lm}">上月</button></div>`) + `
    <div class="card table-wrap"><table><tr><th>單號</th><th>客戶</th><th>狀態</th><th>配送日</th><th>品項</th><th class="num">金額</th><th class="num">已收</th><th>師傅</th></tr>
    ${list.map(o => `<tr class="click" data-o="${o.id}"><td>${esc(o.order_no)}</td><td>${esc(o.customer_name)}</td><td><span class="pill ${o.status === 'delivered' ? 'ok' : o.status === 'cancelled' ? 'bad' : o.status === 'new' ? 'accent' : 'blue'}">${STATUS[o.status]}</span></td>
      <td>${esc((o.delivery_date || '').slice(5).replace('-', '/'))} ${o.delivery_slot ? SLOT[o.delivery_slot] : ''}</td><td>${esc(o.summary || '')}</td><td class="num">${money(o.total_amount)}</td>
      <td class="num">${o.status === 'delivered' ? money(o.paid_amount) : ''}</td><td>${esc(o.driver_name || '')}</td></tr>`).join('') || '<tr><td colspan="8" class="muted">沒有訂單</td></tr>'}
    ${list.length ? `<tr class="total"><td colspan="5">合計 ${list.length} 單</td><td class="num">${money(list.reduce((s, o) => s + o.total_amount, 0))}</td><td class="num">${money(list.reduce((s, o) => s + (o.paid_amount || 0), 0))}</td><td></td></tr>` : ''}</table></div>`;
  const setRange = (from, to) => { store.set('orderRange', { from, to }); ordersQuery(view, arg); };
  view.onchange = e => { if (e.target.id === 'from' || e.target.id === 'to') setRange($('#from').value, $('#to').value || $('#from').value); };
  view.onclick = e => {
    const b = e.target.closest('[data-r]'); if (b) { const [f, to] = b.dataset.r.split(','); setRange(f, to); return; }
    const tr = e.target.closest('[data-o]'); if (tr) location.hash = '#/order/' + tr.dataset.o;
  };
}

// ---------------------------------------------------------------------
// 來電歷史：誰打來、有沒有下單；未處理的可以按「已處理」
// ---------------------------------------------------------------------
async function callHistory(view, arg) {
  const date = /^\d{4}-\d{2}-\d{2}$/.test(arg || '') ? arg : today();
  const [list, un] = await Promise.all([data('/api/calls/history?from=' + date), data('/api/calls/history?unhandled=1')]);
  view.innerHTML = `<p><a href="#/home">← 首頁</a></p><div class="row between"><div class="row">
      <button class="ghost" data-day="${addDays(date, -1)}">‹</button><h2 style="margin:0">來電歷史　${dayText(date)}</h2><button class="ghost" data-day="${addDays(date, 1)}">›</button></div>
      <input type="date" id="d" value="${date}" style="width:auto"></div>
    ${un.length ? `<div class="card" style="border-color:var(--accent)"><h3 style="margin-top:0">未處理來電 <span class="pill accent">${un.length}</span> <span class="muted">（最近 3 天打來、還沒下單的）</span></h3>
      ${un.map(callLine).join('')}</div>` : ''}
    <div class="card table-wrap"><table><tr><th>時間</th><th>來電號碼</th><th>客戶</th><th>下訂狀態</th></tr>
    ${list.map(l => `<tr><td>${hhmm(l.received_at)}</td><td>${esc(fmtPhone(l.phone))}${l.source === 'test' ? ' <span class="pill">模擬</span>' : ''}</td>
      <td>${l.customer_id ? `<a href="#/customers/${l.customer_id}">${esc(l.customer_name)}</a>` : '<span class="muted">—</span>'}</td>
      <td>${l.order_no ? `<span class="pill ok">${esc(l.order_no)}</span>` : l.handled_at ? '<span class="pill">已處理</span>' : l.customer_id ? `<button class="ghost" data-take="${l.id}">記單</button>` : `<button class="ghost" data-newc="${esc(l.phone)}">新增顧客</button>`}</td></tr>`).join('')
    || '<tr><td colspan="4" class="muted">這天沒有來電</td></tr>'}</table></div>`;
  bindCallLines(view, () => callHistory(view, arg), list.concat(un));
  view.addEventListener('click', e => { const d = e.target.closest('[data-day]'); if (d) location.hash = '#/calls/' + d.dataset.day; });
  $('#d').onchange = e => { location.hash = '#/calls/' + e.target.value; };
}
// 一通未處理來電（首頁左邊、來電歷史共用）
function callLine(l) {
  return `<div class="unrow"><div class="grow"><b class="tel">${esc(fmtPhone(l.phone))}</b> <span class="muted">${esc((l.received_at || '').slice(5, 16).replace('-', '/'))}</span><br>
    ${l.customer_id ? `${esc(l.customer_name)}・<span class="muted">${esc(l.address || '')}</span>` : '<span class="pill bad">陌生號碼</span>'}</div>
    <div class="row" style="gap:4px">${l.customer_id ? `<button data-take="${l.id}">記單</button>` : `<button data-newc="${esc(l.phone)}">＋新增客戶</button>`}<button class="ghost" data-handled="${l.id}">已處理</button></div></div>`;
}
function bindCallLines(root, reload, calls) {
  root.onclick = async e => {
    const h = e.target.closest('[data-handled]');
    if (h) { try { await api('POST', `/api/calls/${h.dataset.handled}/handled`, {}); store.del('c:/api/calls/history?unhandled=1'); toast('已標記處理'); reload(); } catch (err) { toast(err.message, 'bad'); } return; }
    const tk = e.target.closest('[data-take]');
    if (tk) { const l = calls.find(x => String(x.id) === tk.dataset.take); S.takeCall = { id: l.id, phone: l.phone, customer_id: l.customer_id }; location.hash = '#/home'; return; }
    const nc = e.target.closest('[data-newc]');
    if (nc) { const k = calls.find(x => x.phone === nc.dataset.newc); customerDialog({ phone: nc.dataset.newc }, c => { S.takeCall = { id: k && k.id, phone: nc.dataset.newc, customer_id: c.id }; location.hash = '#/home'; }); }
  };
}

// ---------------------------------------------------------------------
// 儀表板
// ---------------------------------------------------------------------
const pct = (now, before) => before ? Math.round((now - before) / before * 100) : null;
const trend = (now, before, label) => { const p = pct(now, before); return p == null ? `<span class="muted">${label}沒有資料</span>` : `<span class="trend ${p >= 0 ? 'up' : 'down'}">${p >= 0 ? '▲' : '▼'} ${Math.abs(p)}%</span> <span class="muted">比${label}</span>`; };
// 單一數列的長條圖（SVG）：滑過去看數字
function bars(values, labels, opts = {}) {
  const w = opts.w || 640, h = opts.h || 180, pad = 22, max = Math.max(1, ...values), bw = (w - pad) / values.length;
  return `<svg class="bars" viewBox="0 0 ${w} ${h + 22}" role="img" aria-label="${esc(opts.title || '')}">
    <line x1="${pad}" y1="${h}" x2="${w}" y2="${h}" class="axis"/>
    ${values.map((v, i) => { const bh = v ? Math.max(3, v / max * (h - 18)) : 0, x = pad + i * bw + bw * 0.18, ww = bw * 0.64;
      return `<g class="bar"><title>${esc(labels[i])}：${opts.money ? money(v) : v}</title><rect class="hit" x="${pad + i * bw}" y="0" width="${bw}" height="${h}"/>
        ${bh ? `<path d="M${x},${h} v${-(bh - 4)} q0,-4 4,-4 h${ww - 8} q4,0 4,4 v${bh - 4} z"/>` : ''}
        ${opts.showValues && v ? `<text x="${x + ww / 2}" y="${h - bh - 5}" class="val">${opts.money ? (v >= 10000 ? (v / 10000).toFixed(1) + '萬' : v.toLocaleString()) : v}</text>` : ''}
        ${i % (opts.every || 1) === 0 ? `<text x="${pad + i * bw + bw / 2}" y="${h + 16}" class="lab">${esc(labels[i])}</text>` : ''}</g>`; }).join('')}</svg>`;
}
async function dashboardPage(view) {
  const period = store.get('dashPeriod', 'day');
  const d = await data('/api/reports/dashboard?period=' + period);
  const avg = Math.round(d.sales_30d / 30);
  const rate = d.due_today ? Math.round(d.done_today / d.due_today * 100) : 0;
  const gs = d.gas_sales || [], gmax = Math.max(1, ...gs.map(g => g.qty));
  view.innerHTML = `<p><a href="#/money">← 帳務</a></p><h2>儀表板 <span class="muted">${dayText(d.today)}</span></h2>
    <div class="kpis">
      <div class="card kpi"><div class="muted">今天已送達的金額</div><div class="kv">${money(d.sales_today)}</div>
        <div>${trend(d.sales_today, d.sales_yesterday, '昨天')}</div><div class="muted">近 30 天平均每天 ${money(avg)}</div></div>
      <div class="card kpi"><div class="muted">今天接單</div><div class="kv">${d.orders_today} <small>張</small></div>
        <div>${trend(d.orders_today, d.orders_yesterday, '昨天')}</div>${bars(d.hourly, d.hourly.map((_, i) => i + '點'), { h: 60, w: 300, every: 6, title: '每小時接單' })}</div>
      <div class="card kpi"><div class="muted">今天派出去</div><div class="kv">${d.dispatched_today} <small>張</small></div></div>
      <div class="card kpi"><div class="muted">今天要送的完成率</div><div class="kv">${rate}%</div>
        <div class="meter"><span style="width:${rate}%"></span></div><div class="muted">${d.done_today}/${d.due_today} 張，還剩 ${d.due_today - d.done_today} 張${d.overdue ? `；<b style="color:var(--bad)">逾期 ${d.overdue} 張</b>` : ''}</div></div>
    </div>
    <div class="dash2">
      <div class="card"><div class="row between"><h3 style="margin:0">瓦斯品項銷售</h3>
        <div class="seg2">${[['day', '當日'], ['week', '當週'], ['month', '當月']].map(([k, t]) => `<a class="${k === period ? 'on' : ''}" data-p="${k}">${t}</a>`).join('')}</div></div>
        ${gs.length ? `<table class="gs">${gs.map(g => `<tr><td>${esc(g.name)}</td><td style="width:45%"><div class="hbar"><span style="width:${g.qty / gmax * 100}%"></span></div></td><td class="num">${g.qty} 支</td><td class="num">${money(g.amount)}</td></tr>`).join('')}
          <tr class="total"><td>合計</td><td></td><td class="num">${gs.reduce((s, g) => s + g.qty, 0)} 支</td><td class="num">${money(gs.reduce((s, g) => s + g.amount, 0))}</td></tr></table>`
          : '<div class="muted" style="padding:30px;text-align:center">這段期間還沒有送達的瓦斯</div>'}</div>
      <div class="card"><h3 style="margin-top:0">營業額</h3>
        <div class="kpis two"><div><div class="muted">本週</div><div class="kv">${money(d.sales_week)}</div><div>${trend(d.sales_week, d.sales_last_week, '上週同期')}</div></div>
          <div><div class="muted">本月</div><div class="kv">${money(d.sales_month)}</div><div>${trend(d.sales_month, d.sales_last_month, '上月同期')}</div></div></div>
        <h3>今年每個月</h3>${bars(d.months, d.months.map((_, i) => (i + 1) + '月'), { money: true, showValues: true, title: '每月營業額' })}</div>
    </div>
    <p class="muted">營業額＝已送達訂單的金額，日期看送達那天。</p>`;
  view.onclick = e => { const p = e.target.closest('[data-p]'); if (p) { store.set('dashPeriod', p.dataset.p); dashboardPage(view); } };
}

// 訂單詳情
async function orderDetail(view, id) {
  S.live = false;
  const [o, drivers] = await Promise.all([api('GET', '/api/orders/' + id), data('/api/drivers')]);
  const editable = o.status === 'new' || o.status === 'dispatched';
  view.innerHTML = `<p><a href="javascript:history.back()">← 回上一頁</a></p>
    <div class="card"><div class="row between"><h2 style="margin:0">${esc(o.order_no)}　${esc(o.customer_name)}</h2>
      <span class="pill ${o.status === 'delivered' ? 'ok' : o.status === 'cancelled' ? 'bad' : 'accent'}">${STATUS[o.status]}</span></div>
      <p>配送 <b>${dayText(o.delivery_date)} ${slotText(o.delivery_slot)}</b>${editable ? ' <button class="ghost" id="resched">改日期／時段</button>' : ''}</p>
      <p>${esc(o.address)}　${esc(o.phone || '')}　${o.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : '現結・' + (o.pay_method === 'transfer' ? '<span class="pill blue">轉帳</span>' : '現金')}
      ${o.needs_invoice ? `<span class="pill">開發票 ${esc(o.tax_id || '')}</span>` : ''}</p>
      <div class="table-wrap"><table><tr><th>商品</th><th class="num">數量</th><th class="num">單價</th><th class="num">折扣</th><th class="num">金額</th><th class="num">回收空瓶</th></tr>
      ${o.items.map(i => `<tr><td>${esc(i.product_name)}</td><td class="num">${i.qty}</td><td class="num">${money(i.unit_price)}</td><td class="num">${i.discount ? money(i.discount) : ''}</td><td class="num">${money(i.amount)}</td><td class="num">${i.empties_returned || ''}</td></tr>`).join('')}
      <tr><th colspan="4">合計</th><th class="num">${money(o.total_amount)}</th><th></th></tr></table></div>
      <h3>收款</h3>${o.payments.length ? o.payments.map(p => `<div>${esc(p.received_at)}　${money(p.amount)}　${METHOD[p.method]}　${esc(p.collected_by_name || '')}</div>`).join('') : '<div class="muted">沒有</div>'}
      ${o.deposits.length ? '<h3>押瓶</h3>' + o.deposits.map(x => `<div>${x.qty_change > 0 ? '借出' : '歸還'} ${Math.abs(x.qty_change)} 支　押金 ${money(x.amount)}</div>`).join('') : ''}
      <label>備註</label><textarea id="note">${esc(o.note || '')}</textarea>
      <div class="row" style="margin-top:10px">
        <button id="saveNote">儲存備註</button>
        ${editable ? `<select id="drv" style="width:auto"><option value="">派給…</option>${drivers.map(d => `<option value="${d.id}" ${d.id === o.driver_id ? 'selected' : ''}>${esc(d.name)}</option>`).join('')}</select>
          ${o.status === 'dispatched' ? '<button id="sendLine">傳 LINE</button>' : ''}
          <button id="deliverHere">店裡代按送達</button><button class="danger" id="cancel">取消訂單</button>` : ''}
        ${o.status === 'delivered' && S.me.role === 'admin' ? '<button class="danger" id="undeliver">改回未送達</button>' : ''}
      </div></div>`;
  const reload = () => orderDetail(view, id);
  const rs = $('#resched');
  if (rs) rs.onclick = () => {
    const dlg = $('#dlg');
    dlg.innerHTML = `<form><h2>改配送日期／時段</h2><label>配送日</label><input type="date" name="d" value="${o.delivery_date || today()}">
      <label>時段</label><select name="s">${[['', '不指定'], ...SLOTS.map(x => [x, SLOT[x]])].map(([k, v]) => `<option value="${k}" ${(o.delivery_slot || '') === k ? 'selected' : ''}>${v}</option>`).join('')}</select>
      <p class="row"><button class="primary">儲存</button><button type="button" onclick="this.closest('dialog').close()">取消</button></p></form>`;
    $('form', dlg).onsubmit = async e => {
      e.preventDefault();
      try { await api('PUT', '/api/orders/' + id, { delivery_date: e.target.d.value, delivery_slot: e.target.s.value }); dlg.close(); toast('已改', 'ok'); reload(); } catch (err) { toast(err.message, 'bad'); }
    };
    dlg.showModal();
  };
  $('#saveNote').onclick = async () => { await api('PUT', '/api/orders/' + id, { note: $('#note').value }); toast('已儲存', 'ok'); };
  if (editable) {
    $('#drv').onchange = async e => { if (!e.target.value) return; const d = drivers.find(x => x.id === +e.target.value);
      await api('POST', `/api/orders/${id}/dispatch`, { driver_id: d.id }); toast(`已派給 ${d.name}`, 'ok', lineAction({ ...o, driver_id: d.id, driver_name: d.name })); reload(); };
    const sl = $('#sendLine'); if (sl) sl.onclick = () => sendLine(o);
    $('#cancel').onclick = async () => { if (!confirm('確定取消這張單？')) return; await api('POST', `/api/orders/${id}/cancel`, {}); toast('已取消'); reload(); };
    $('#deliverHere').onclick = () => {
      if (o.status !== 'dispatched') return toast('要先派給師傅', 'bad');
      deliverDialog(o, async body => { await api('POST', `/api/orders/${id}/deliver`, body); toast('已送達', 'ok'); reload(); });
    };
  }
  const u = $('#undeliver');
  if (u) u.onclick = async () => { if (!confirm('改回未送達？庫存會沖回，已收的錢不會自動刪除。')) return; await api('POST', `/api/orders/${id}/undeliver`, {}); reload(); };
}

// ---------------------------------------------------------------------
// 首頁（接單）：左邊來電和找客戶、中間挑品項、右邊下單
// ---------------------------------------------------------------------
async function officeNew(view) {
  S.live = false;
  const [products, drivers] = await Promise.all([data('/api/products'), data('/api/drivers')]);
  const t = today();
  const st = { customer: null, qty: {}, results: [], date: t, slot: '', driver: null, shifts: [], callId: null, pay: 'cash' };
  const sellable = products.filter(p => p.category !== 'empty_cylinder');
  view.innerHTML = `<div class="home-grid"><div class="home-left">
      <section class="card callstack" id="calls" hidden></section>
      <section class="card" id="unhandled" hidden></section>
      <section class="card"><label style="margin-top:0">找客戶（電話、名字、地址、編號）</label>
        <input id="q" placeholder="輸入後按 Enter 選第一個" autocomplete="off">
        <div class="results" id="results"></div>
        <div class="row between" style="margin-top:8px"><button class="ghost" id="newCust">＋ 新客戶</button><a class="muted" href="#/calls">📜 歷史來電 ›</a></div></section></div>
    <section class="card" id="mid"><div class="muted empty">先在左邊選客戶，或點一通來電</div></section>
    <section class="card" id="right"></section></div>`;
  const q = $('#q');
  q.focus();
  let timer;
  const search = async () => {
    const v = q.value.trim();
    st.results = v ? await api('GET', '/api/customers?limit=30&q=' + encodeURIComponent(v)).catch(() => {
      const all = store.get('c:/api/customers?limit=1000', []);
      return all.filter(c => [c.name, c.phone, c.other_phones, c.address, c.code].some(x => (x || '').includes(v))).slice(0, 30);
    }) : [];
    renderResults(v);
  };
  const renderResults = (v = q.value.trim()) => {
    $('#results').innerHTML = st.results.map((c, i) => `<div class="result${st.customer && st.customer.id === c.id ? ' on' : ''}" data-i="${i}"><b>${esc(c.name)}</b> <span class="muted">${esc(c.phone || '')}</span>
      <div class="muted">${esc(c.address)}${c.billing_type === 'monthly' ? '・月結' : ''}${c.balance > 0 ? '・欠 ' + money(c.balance) : ''}</div></div>`).join('')
      || (v ? '<div class="muted">找不到，按「＋ 新客戶」</div>' : '');
  };
  q.oninput = () => { clearTimeout(timer); timer = setTimeout(search, 150); };
  q.onkeydown = e => { if (e.key === 'Enter' && st.results[0]) pick(st.results[0]); };
  $('#results').onclick = e => { const r = e.target.closest('.result'); if (r) pick(st.results[+r.dataset.i]); };
  $('#newCust').onclick = () => customerDialog({ phone: /^\d+$/.test(q.value.trim()) ? q.value.trim() : '', name: /^\d+$/.test(q.value.trim()) ? '' : q.value.trim() }, c => pick(c));

  // 來電排隊（電話正在響的）
  const drawCalls = () => {
    const box = $('#calls'); if (!box) return;
    const wait = S.calls.filter(k => k.state === 'wait').length;
    box.hidden = !S.calls.length;
    box.innerHTML = `<div class="row between"><b>📞 來電 ${wait ? `<span class="pill accent">${wait} 通待處理</span>` : ''}</b>
      ${S.calls.some(k => k.state === 'done') ? '<button class="ghost" id="clearDone">清掉已處理</button>' : ''}</div>
      ${S.calls.map(k => callRowHTML(k, k.id === st.callId)).join('')}`;
  };
  const takeCall = k => {
    st.callId = k.id;
    const c = (k.matches || [])[0];
    if (c) { q.value = fmtPhone(k.phone); st.results = k.matches; renderResults(); pick(c, true); }
    else { drawCalls(); customerDialog({ phone: k.phone }, nc => { st.results = [nc]; renderResults(); pick(nc, true); }); }
  };
  const nextCall = () => [...S.calls].reverse().find(k => k.state === 'wait');
  $('#calls').onclick = e => {
    const x = e.target.closest('[data-dismiss]');
    if (x) { S.calls = S.calls.filter(k => String(k.id) !== x.dataset.dismiss); saveCalls(); if (String(st.callId) === x.dataset.dismiss) st.callId = null; drawCalls(); return; }
    if (e.target.id === 'clearDone') { S.calls = S.calls.filter(k => k.state !== 'done'); saveCalls(); drawCalls(); return; }
    const r = e.target.closest('[data-call]'); if (r) takeCall(S.calls.find(k => String(k.id) === r.dataset.call));
  };
  S.newPage = {
    onCall(call) {
      // 手上沒有在記的客人才自動帶出來；正在記別人就只排進清單
      if (!st.customer) takeCall(S.calls.find(k => k.id === call.id)); else drawCalls();
      const r = $(`[data-call="${call.id}"]`); if (r) r.classList.add('flash');
      drawUnhandled();
    },
  };
  window.addEventListener('hashchange', () => { S.newPage = null; }, { once: true });

  // 未處理來電（打來了但還沒下單；重開程式也不會不見）
  let unhandled = [];
  const drawUnhandled = async () => {
    unhandled = await api('GET', '/api/calls/history?unhandled=1').catch(() => store.get('c:/api/calls/history?unhandled=1', []));
    store.set('c:/api/calls/history?unhandled=1', unhandled);
    const box = $('#unhandled'); if (!box) return;
    const live = new Set(S.calls.filter(k => k.state === 'wait').map(k => k.id));
    const list = unhandled.filter(l => !live.has(l.id));
    box.hidden = !list.length;
    box.innerHTML = `<b>☎ 未處理來電 <span class="pill bad">${list.length}</span></b> <span class="muted">沒下單、還沒回撥</span>${list.map(callLine).join('')}`;
  };
  $('#unhandled').onclick = async e => {
    const h = e.target.closest('[data-handled]');
    if (h) { try { await api('POST', `/api/calls/${h.dataset.handled}/handled`, {}); toast('已標記處理'); drawUnhandled(); } catch (err) { toast(err.message, 'bad'); } return; }
    const tk = e.target.closest('[data-take]');
    if (tk) { const l = unhandled.find(x => String(x.id) === tk.dataset.take); takeServerCall(l); return; }
    const nc = e.target.closest('[data-newc]');
    if (nc) { const l = unhandled.find(x => x.phone === nc.dataset.newc); customerDialog({ phone: nc.dataset.newc }, c => { st.callId = l && l.id; pick(c, true); }); }
  };
  const takeServerCall = async l => {
    st.callId = l.id || null;
    q.value = fmtPhone(l.phone);
    if (!l.customer_id) return customerDialog({ phone: l.phone }, c => pick(c, true));
    const c = await api('GET', '/api/customers/' + l.customer_id).catch(() => null);
    if (c) pick(c, true);
    if (!l.id) q.value = '';
  };
  drawCalls();
  drawUnhandled();
  if (S.takeCall) { const k = S.takeCall; S.takeCall = null; takeServerCall(k); }
  else { const first = nextCall(); if (first && Date.now() - (first.ts || 0) < 10 * 60000) takeCall(first); }

  function pick(c, keepCall) {
    if (!keepCall) st.callId = null;
    st.customer = c; st.qty = {};
    drawCalls(); renderResults();
    renderMid();
  }
  const priceOf = pid => { const p = products.find(x => x.id === +pid); const sp = ((st.customer && st.customer.prices) || []).find(x => x.product_id === +pid); return sp ? sp.price : (p ? p.sale_price : 0); };
  const total = () => Object.entries(st.qty).reduce((s, [pid, n]) => s + n * priceOf(pid), 0);

  async function renderMid() {
    const c = st.customer;
    $('#mid').innerHTML = `<div class="big">${esc(c.name)}</div><div class="muted">載入中…</div>`;
    const full = await api('GET', '/api/customers/' + c.id).catch(() => c);
    st.customer = { ...c, ...full };
    const cu = st.customer;
    if (cu.default_driver_id && !st.driver) st.driver = cu.default_driver_id;
    const fav = (cu.favorites && cu.favorites.frequent) || [], last = cu.favorites && cu.favorites.last;
    const gas = sellable.filter(p => p.category === 'gas'), others = sellable.filter(p => p.category !== 'gas');
    $('#mid').innerHTML = `<div class="row between"><div><span class="cust-name" style="font-size:24px">${esc(cu.name)}</span> <span class="muted">${cu.code ? '編號 ' + esc(cu.code) : ''}</span></div>
        <a href="#/customers/${cu.id}" class="muted">客戶資料 ›</a></div>
      <div>${esc(cu.address)}${cu.floor ? '・' + esc(cu.floor) : ''}${cu.has_elevator === 0 ? '・<b>無電梯</b>' : ''}　<span class="muted">${esc(cu.phone || '')}</span></div>
      <div class="row" style="gap:4px;margin:6px 0">${cu.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : ''}${cu.balance > 0 ? `<span class="pill bad">欠 ${money(cu.balance)}</span>` : ''}
        ${cu.needs_invoice ? `<span class="pill">開發票 ${esc(cu.tax_id || '')}</span>` : ''}</div>
      ${cu.open_orders > 0 ? `<div class="call-warn">⚠ 已經有 ${cu.open_orders} 張單還沒送到，可能是來催的</div>` : ''}
      ${cu.order_note ? `<div class="order-note">📌 訂單常態備註：${esc(cu.order_note)}</div>` : ''}
      ${cu.note ? `<div class="muted">📝 ${esc(cu.note)}</div>` : ''}
      ${fav.length ? `<h3>常叫品項 <span class="muted">點一下加一支</span></h3><div class="chips">${fav.map(f => `<button class="chip fav" data-plus="${f.product_id}">${esc(f.name)}</button>`).join('')}</div>` : ''}
      ${last && last.items && last.items.length ? `<div class="call-last">上一張單 ${esc((last.delivery_date || last.ordered_at || '').slice(5, 10).replace('-', '/'))}：${last.items.map(i => esc(i.name) + '×' + i.qty).join('、')}・${money(last.total_amount)}
        <button class="ghost" id="rebuy">↻ 回購</button></div>` : ''}
      <h3>瓦斯</h3><div class="quick">${gas.map(qbox).join('') || '<div class="muted">還沒有瓦斯商品，請到「商品庫存 → 商品」新增</div>'}</div>
      ${others.length ? `<details ${others.some(p => st.qty[p.id]) ? 'open' : ''}><summary class="muted" style="margin:12px 0 6px;cursor:pointer">其他商品（${others.length}）</summary><div class="quick">${others.map(qbox).join('')}</div></details>` : ''}`;
    $('#mid').onclick = e => {
      const b = e.target.closest('button'); if (!b) return;
      if (b.id === 'rebuy') { st.qty = {}; last.items.forEach(i => { st.qty[i.product_id] = i.qty; }); sync(); toast('已照上一張單帶入'); return; }
      if (b.dataset.plus || b.dataset.minus) {
        const pid = b.dataset.plus || b.dataset.minus;
        st.qty[pid] = Math.max(0, (st.qty[pid] || 0) + (b.dataset.plus ? 1 : -1));
        if (!st.qty[pid]) delete st.qty[pid];
        sync();
      }
    };
    $('#mid').oninput = e => { if (e.target.dataset.qty) { const n = parseInt(e.target.value) || 0; if (n > 0) st.qty[e.target.dataset.qty] = n; else delete st.qty[e.target.dataset.qty]; sync(true); } };
    $('#mid').onkeydown = e => { if (e.key === 'Enter' && e.target.dataset.qty) create(); };
    await loadShifts();
    sync();
  }
  function qbox(p) {
    const sp = (st.customer.prices || []).find(x => x.product_id === p.id);
    return `<div class="q" data-p="${p.id}"><div><b>${esc(p.name)}</b></div><div class="muted">${money(sp ? sp.price : p.sale_price)}${sp ? '（特價）' : ''}${p.stock_qty != null ? '・庫存 ' + p.stock_qty : ''}</div>
      <div class="stepper"><button data-minus="${p.id}">−</button><input data-qty="${p.id}" inputmode="numeric" value=""><button data-plus="${p.id}">＋</button></div></div>`;
  }
  async function loadShifts() {
    st.shifts = await data('/api/shifts?date=' + st.date).catch(() => []);
  }
  const onShift = (d, s) => st.shifts.some(x => x.driver_id === d && (!s || x.slot === s));
  function renderRight() {
    const c = st.customer;
    const items = Object.entries(st.qty);
    const sorted = [...drivers].sort((a, b) => onShift(b.id, st.slot) - onShift(a.id, st.slot));
    $('#right').innerHTML = `<h3 style="margin-top:0">新增訂單 ${c ? '<span class="muted">' + esc(c.name) + '</span>' : ''}</h3>
      <label>配送日</label><div class="row" style="flex-wrap:nowrap"><input type="date" id="dd" value="${st.date}" min="${t}">
        <button class="${st.date === t ? 'primary' : ''}" data-date="${t}">今天</button><button class="${st.date === addDays(t, 1) ? 'primary' : ''}" data-date="${addDays(t, 1)}">明天</button></div>
      <label>時段</label><div class="segment" id="slot">${[['', '不指定'], ...SLOTS.map(s => [s, SLOT[s]])].map(([k, v]) => `<button type="button" class="${st.slot === k ? 'on' : ''}" data-slot="${k}">${v}</button>`).join('')}</div>
      <label>師傅</label><select id="drv"><option value="">先不派（放待派）</option>${sorted.map(d => `<option value="${d.id}" ${st.driver === d.id ? 'selected' : ''}>${esc(d.name)}${st.slot ? (onShift(d.id, st.slot) ? `（${SLOT[st.slot]}有班）` : '（這時段沒班）') : (onShift(d.id) ? '（有排班）' : '（沒排班）')}</option>`).join('')}</select>
      <div class="items">${items.map(([pid, n]) => { const p = products.find(x => x.id === +pid); return `<div class="row between item"><span class="grow">${esc(p ? p.name : '#' + pid)}</span>
        <span class="stepper sm"><button data-minus="${pid}">−</button><b>${n}</b><button data-plus="${pid}">＋</button></span><span class="money" style="width:80px;text-align:right">${money(n * priceOf(pid))}</span></div>`; }).join('')
        || `<div class="muted empty">${c ? '從中間點品項加進來' : '先選客戶'}</div>`}</div>
      ${c && c.billing_type === 'monthly' ? '<div class="muted">月結客戶，不用收錢</div>' : `<label>收款方式</label><div class="segment" id="pay"><button type="button" class="${st.pay === 'cash' ? 'on' : ''}" data-m="cash">現金</button><button type="button" class="${st.pay === 'transfer' ? 'on' : ''}" data-m="transfer">轉帳</button></div>`}
      <label>備註（給師傅看）</label><input id="note" placeholder="例如：放後門、下午 3 點後" value="${esc(st.note || '')}">
      <div class="total-bar"><span>合計</span><span id="total">${money(total())}</span></div>
      <button class="primary lg" id="create" ${c && items.length ? '' : 'disabled'}>送出訂單（Enter）</button>`;
  }
  function sync(fromInput) {
    $$('#mid .q').forEach(el => {
      const n = st.qty[el.dataset.p] || 0;
      el.classList.toggle('has', n > 0);
      if (!fromInput) $('input', el).value = n || '';
    });
    const note = $('#note'); if (note) st.note = note.value;
    renderRight();
  }
  const right = $('#right');
  right.onclick = async e => {
    const b = e.target.closest('button'); if (!b) return;
    if (b.dataset.date) { st.date = b.dataset.date; await loadShifts(); sync(); return; }
    if ('slot' in b.dataset) { st.slot = b.dataset.slot; sync(); return; }
    if (b.dataset.m) { st.pay = b.dataset.m; sync(); return; }
    if (b.dataset.plus || b.dataset.minus) {
      const pid = b.dataset.plus || b.dataset.minus;
      st.qty[pid] = Math.max(0, (st.qty[pid] || 0) + (b.dataset.plus ? 1 : -1));
      if (!st.qty[pid]) delete st.qty[pid];
      sync(); return;
    }
    if (b.id === 'create') create();
  };
  right.onchange = async e => {
    if (e.target.id === 'dd') { st.date = e.target.value || t; await loadShifts(); sync(); }
    if (e.target.id === 'drv') st.driver = e.target.value ? +e.target.value : null;
  };
  right.oninput = e => { if (e.target.id === 'note') st.note = e.target.value; };
  right.onkeydown = e => { if (e.key === 'Enter' && e.target.tagName !== 'SELECT' && e.target.type !== 'date') create(); };
  renderRight();

  async function create() {
    if (!st.customer) return toast('請先選客戶', 'bad');
    const items = Object.entries(st.qty).map(([pid, qty]) => ({ product_id: +pid, qty }));
    if (!items.length) return toast('請選品項', 'bad');
    const note = [st.customer.order_note, st.note].filter(Boolean).join('；');
    const body = { customer_id: st.customer.id, items, note: note || null, delivery_date: st.date, delivery_slot: st.slot || null };
    if (st.customer.billing_type !== 'monthly') body.pay_method = st.pay;
    if (st.driver) body.driver_id = st.driver;
    if (st.callId) body.call_id = st.callId;
    const btn = $('#create'); btn.disabled = true;
    try {
      const o = await api('POST', '/api/orders', body);
      const k = S.calls.find(x => x.id === st.callId); if (k) { k.state = 'done'; k.order_no = o.order_no; saveCalls(); }
      toast(`已建立 ${o.order_no}（${dayText(o.delivery_date)} ${slotText(o.delivery_slot)}）${o.driver_name ? '，派給 ' + o.driver_name : '，放在待派'}`, 'ok', o.driver_name ? lineAction(o) : null);
    } catch (e) {
      if (!e.network) { btn.disabled = false; return toast(e.message, 'bad'); }
      queue('POST', '/api/orders', body, '建立訂單');
      const k = S.calls.find(x => x.id === st.callId); if (k) { k.state = 'done'; k.order_no = '離線排隊中'; saveCalls(); }
      toast('離線中，訂單會在連上後自動送出', 'ok');
    }
    officeNew(view);
  }
}

// ---------------------------------------------------------------------
// 從舊系統抓資料：瀏覽器按 F12 → 這裡跳出確認視窗 → Enter 存、Esc 不存
// ---------------------------------------------------------------------
const LEGACY_FIELDS = [['name', '名字 *'], ['phone', '電話'], ['phone2', '電話 2'], ['address', '地址 *'], ['area', '區域'], ['tax_id', '統一編號']];
S.capQueue = [];
function capDialog() {
  let d = $('#capdlg');
  if (!d) { d = document.createElement('dialog'); d.id = 'capdlg'; d.className = 'capdlg'; document.body.appendChild(d); }
  return d;
}
async function checkCaptures() {
  if (!S.me || S.me.role === 'driver') return;
  try {
    const list = await api('GET', '/api/legacy/captures');
    if (list.length) showCapture(list[list.length - 1].id);
  } catch { }
}
const cylText = (size, qty) => qty > 0 ? `${size} 借走 ${qty} 支` : `${size} 寄放 ${-qty} 支在店裡`;
async function showCapture(id) {
  const d = capDialog();
  if (d.open) { if (+d.dataset.id !== +id && !S.capQueue.includes(id)) S.capQueue.push(id); if (+d.dataset.id === +id) loadCapture(id); return; }
  loadCapture(id);
}
async function loadCapture(id) {
  const d = capDialog();
  let r;
  try { r = await api('GET', '/api/legacy/captures/' + id); } catch (e) { toast(e.message, 'bad'); return; }
  if (r.status !== 'pending') return nextCapture();
  const p = r.page, m = r.matches;
  const cur = c => c || {};
  d.dataset.id = id;
  const bal = Object.entries(p.balance || {});
  const last = (p.deliveries || [])[0];
  const prices = Object.entries(p.prices || {});
  d.innerHTML = `<form method="dialog">
    <h2 style="margin-top:0">📥 舊系統資料 <span class="pill">編號 ${esc(p.legacy_id)}</span></h2>
    ${(p.warnings || []).map(w => `<div class="call-warn">⚠ ${esc(w)}</div>`).join('')}
    <label>存成</label><select name="dest">
      ${m.map(c => `<option value="${c.id}">更新：${esc(c.name)}（${esc(c.match_by)}對到）</option>`).join('')}
      <option value="0" ${m.length ? '' : 'selected'}>新增一位新客戶</option></select>
    <div class="form-grid" id="capf">${LEGACY_FIELDS.map(([k, t]) => `<div><label>${t}</label><input name="${k}" value="${esc(p[k] ?? '')}"><div class="was" data-was="${k}"></div></div>`).join('')}
      <div><label>&nbsp;</label><label style="color:var(--text)"><input type="checkbox" name="needs_invoice" ${p.needs_invoice ? 'checked' : ''}>要開發票${p.invoice ? `<span class="muted">（舊系統：${esc(p.invoice)}）</span>` : ''}</label></div></div>
    <label>備註（舊系統的注意事項）</label><textarea name="note" rows="2">${esc(p.note || '')}</textarea><div class="was" data-was="note"></div>
    <div class="capsum">
      <div>🛢 桶子：${bal.length ? bal.map(([s, q]) => `<span class="pill ${q > 0 ? 'bad' : 'blue'}">${esc(cylText(s, q))}</span>`).join(' ') : '<span class="muted">沒有借桶或寄桶</span>'}</div>
      <div>🚚 送貨紀錄 <b>${(p.deliveries || []).length}</b> 筆${last ? `，最近一次 ${esc(last.at.slice(0, 10))}：${esc(last.items.map(i => i.size + '×' + i.qty).join('、'))}` : ''}${r.saved_deliveries ? `<span class="muted">（之前已存 ${r.saved_deliveries} 筆，重複的不會再存）</span>` : ''}</div>
      ${(p.cylinders_out || []).length ? `<div>🔖 出桶鋼瓶 ${p.cylinders_out.length} 支：${p.cylinders_out.map(c => esc(c.size + ' ' + c.barcode)).join('、')}</div>` : ''}
      ${prices.length ? `<div>💲 舊系統售價：${prices.map(([s, v]) => {
        const prod = r.products[s];
        if (!prod) return `<span class="pill">${esc(s)} ${money(v)}</span>`;
        if (prod.sale_price === v) return `<span class="pill">${esc(s)} ${money(v)}（同原價）</span>`;
        return `<label class="pill accent" style="cursor:pointer"><input type="checkbox" data-price="${prod.id}" data-v="${v}" checked> ${esc(s)} ${money(v)} 設成特價（原價 ${money(prod.sale_price)}）</label>`;
      }).join(' ')}</div>` : ''}
    </div>
    <div class="row" style="margin-top:14px"><button class="primary" value="ok">存檔（Enter）</button><button value="cancel" formnovalidate>不存（Esc）</button>
      ${S.capQueue.length ? `<span class="muted">後面還有 ${S.capQueue.length} 筆</span>` : ''}</div></form>`;
  const form = $('form', d);
  const showWas = () => {
    const t = m.find(c => String(c.id) === form.elements.dest.value);
    $$('[data-was]', d).forEach(el => {
      const k = el.dataset.was, old = t ? cur(t)[k] : null, now = form.elements[k].value.trim();
      el.textContent = t && (old ?? '') !== now ? '原本：' + (old || '（空白）') : '';
    });
  };
  form.elements.dest.onchange = showWas;
  form.oninput = showWas;
  showWas();
  form.onsubmit = async e => {
    if (e.submitter && e.submitter.value === 'cancel') return;
    e.preventDefault();
    const f = Object.fromEntries(LEGACY_FIELDS.map(([k]) => [k, form.elements[k].value]));
    f.note = form.elements.note.value; f.needs_invoice = form.elements.needs_invoice.checked;
    const body = { customer_id: +form.elements.dest.value, customer: f, prices: $$('[data-price]', d).filter(x => x.checked).map(x => ({ product_id: +x.dataset.price, price: +x.dataset.v })) };
    try {
      const res = await api('POST', `/api/legacy/captures/${id}/save`, body);
      d.close('saved');
      toast(`✓ 已存：${f.name}（新增送貨紀錄 ${res.deliveries_added} 筆${res.refill_days ? `，大約 ${res.refill_days} 天叫一次` : ''}）`, 'ok');
      store.del('c:/api/customers?limit=1000');
      rerender();
    } catch (err) { toast(err.message, 'bad'); }
  };
  d.onclose = async () => {
    if (d.returnValue !== 'saved') { await api('POST', `/api/legacy/captures/${id}/cancel`, {}).catch(() => { }); toast('沒有存（要的話在舊系統再按一次 F12）'); }
    d.returnValue = '';
    nextCapture();
  };
  // textarea 裡按 Enter 換行；Ctrl+Enter 一樣存檔
  form.elements.note.onkeydown = e => { if (e.key === 'Enter' && e.ctrlKey) { e.preventDefault(); form.requestSubmit(); } };
  if (!d.open) d.showModal();
  $('button.primary', d).focus();
}
function nextCapture() { const n = S.capQueue.shift(); if (n) setTimeout(() => showCapture(n), 50); }

function legacyCustomerHTML(lg) {
  if (!lg) return '';
  const i = lg.info, pr = Object.entries(i.prices || {}), tot = Object.entries(i.totals || {});
  return `<div class="card"><h3 style="margin-top:0">📥 舊系統資料 <span class="pill">編號 ${esc(i.legacy_id)}</span> <span class="muted" style="font-size:13px;font-weight:400">最後抓取 ${esc(i.captured_at)}</span></h3>
    <div class="row" style="gap:6px;flex-wrap:wrap">
      ${lg.balance.length ? lg.balance.map(b => `<span class="pill ${b.qty > 0 ? 'bad' : 'blue'}">${esc(cylText(b.size, b.qty))}</span>`).join('') : '<span class="muted">沒有借桶或寄桶</span>'}
      ${i.cylinder_type ? `<span class="pill">瓦斯桶別 ${esc(i.cylinder_type)}</span>` : ''}
      ${pr.length ? `<span class="muted">舊售價 ${pr.map(([s, v]) => esc(s) + ' ' + money(v)).join('、')}</span>` : ''}
      ${tot.length ? `<span class="muted">累計購買 ${tot.map(([s, v]) => esc(s) + ' ' + v).join('、')}</span>` : ''}</div>
    ${lg.cylinders_out.length ? `<h4>出桶鋼瓶（在客人那邊）</h4><div class="table-wrap"><table><tr><th>條碼</th><th>規格</th><th>下次檢驗</th><th>桶齡</th><th>出桶</th><th class="num">天數</th><th>司機</th></tr>
      ${lg.cylinders_out.map(c => `<tr><td>${esc(c.barcode)}</td><td>${esc(c.size || '')}</td><td>${esc(c.next_inspection || '')}${c.next_inspection && c.next_inspection < today() ? ' <span class="pill bad">過期</span>' : ''}</td><td>${c.age_years ?? ''}</td><td>${esc((c.out_at || '').slice(0, 10))}</td><td class="num">${c.days ?? ''}</td><td>${esc(c.driver_name || '')}</td></tr>`).join('')}</table></div>` : ''}
    <h4>舊送貨紀錄 <span class="pill">${lg.deliveries.length}</span></h4>
    <div class="table-wrap" style="max-height:360px;overflow:auto"><table><tr><th>日期</th><th>品項</th><th class="num">金額</th><th>鋼瓶</th><th>付款</th><th>送貨員</th><th>備註</th></tr>
      ${lg.deliveries.map(x => `<tr><td style="white-space:nowrap">${esc(x.delivered_at.slice(0, 16))}</td><td>${esc(x.items || '')}${x.stored_gas ? ' 存氣 ' + x.stored_gas : ''}</td><td class="num">${money(x.amount)}</td><td>${esc(x.cylinder || '')}</td><td>${esc(x.pay_status || '')}</td><td>${esc(x.driver_name || '')}</td><td>${esc(x.note || '')}</td></tr>`).join('') || '<tr><td class="muted">沒有</td></tr>'}</table></div></div>`;
}

// 書籤小工具：在舊系統網頁裡按 F12，把目前的客戶頁送到這台電腦的瓦斯行程式
function bookmarkletCode() {
  const local = /^(localhost|127\.0\.0\.1)$/.test(location.hostname) ? location.origin : 'http://localhost:8080';
  const src = `(function(){var U='${local}/api/local/capture';
function all(w,o){try{if(w.document&&w.document.body)o.push(w.document)}catch(e){}try{for(var i=0;i<w.frames.length;i++)all(w.frames[i],o)}catch(e){}return o}
function has(d){try{return /客戶編號/.test(d.body.innerText||d.body.textContent||'')}catch(e){return false}}
function md(){var ds=all(window.top,[]);for(var i=0;i<ds.length;i++)if(has(ds[i]))return ds[i];try{var f=window.top.frames.main;if(f&&f.document&&f.document.body)return f.document}catch(e){}return document}
function tip(m,ok){try{var d=md(),b=d.createElement('div');b.textContent=m;b.style.cssText='position:fixed;z-index:2147483647;right:16px;top:16px;max-width:480px;padding:12px 18px;border-radius:8px;font:bold 18px sans-serif;color:#fff;background:'+(ok?'#2b8a3e':'#c92a2a')+';box-shadow:0 4px 16px rgba(0,0,0,.3)';d.body.appendChild(b);setTimeout(function(){b.remove()},ok?2500:7000)}catch(e){alert(m)}}
function rows(h){var m=h.match(/\\d{4}\\/\\d{1,2}\\/\\d{1,2}[\\s\\S]{0,700}?\\d{1,2}:\\d{2}:\\d{2}/g)||[];return m.map(function(x){return x.replace(/<[^>]*>|\\s+/g,'')})}
function get(w,u){return new Promise(function(ok){try{var x=new w.XMLHttpRequest();x.open('GET',u,true);x.onload=function(){ok(x.status==200?x.responseText:'')};x.onerror=function(){ok('')};x.send()}catch(e){ok('')}})}
function pages(d,pw,done){var w=d.defaultView,q='';try{q=w.str}catch(e){}if(!q||!/customer_id=/.test(q)){var m=/customer_id=(\\d+)/.exec(d.location.href)||[];try{m[1]=m[1]||w.customer_id}catch(e){}if(!m[1])return done([]);q='customer_id='+m[1]+'&plankey=N'}var base=new URL('gas_record.asp',d.location.href).href+'?'+q,seen={},out=[],n=0;rows(d.documentElement.outerHTML).forEach(function(k){seen[k]=1});
function batch(from){var ps=[];for(var p=from;p<from+10;p++)ps.push(get(w,base+'&page='+p+'&key=&_='+Date.now()));Promise.all(ps).then(function(hs){var last=0;hs.forEach(function(h){var add=0;rows(h).forEach(function(k){if(!seen[k]){seen[k]=1;add++}});if(add){out.push(h);n++}last=add});try{pw.document.body.innerHTML='<div style="font:bold 20px sans-serif;padding:24px">抓取中… 已多抓 '+n+' 頁</div>'}catch(e){}if(last&&from<200)batch(from+10);else done(out)})}batch(1)}
function send(){var ds=all(window.top,[]),best=md(),live=has(best),list=live?[best]:ds,x=Math.max(0,(screen.availWidth||1200)-460),w=window.open('','gascap','width=460,height=220,left='+x+',top=40');if(!w){tip('✗ 小視窗被擋住了：請按網址列右邊的「已封鎖彈出式視窗」圖示，選「一律允許」，再按一次 F12');return}try{w.document.body.innerHTML='<div style="font:bold 20px sans-serif;padding:24px">抓取中…</div>'}catch(e){}
var go=function(extra){var d=best,f=d.createElement('form');f.method='POST';f.action=U;f.target='gascap';f.acceptCharset='UTF-8';f.style.display='none';var add=function(k,v){var i=d.createElement('textarea');i.name=k;i.value=v;f.appendChild(i)};add('enc','uri');var sn=t.__gasSnap;if(!live&&sn){add('url',sn.u);add('title',sn.ti);add('html',encodeURIComponent(sn.h+extra))}list.forEach(function(x){add('url',x.location.href);add('title',x.title||'');add('html',encodeURIComponent(x.documentElement.outerHTML+(x===best?extra:'')))});d.body.appendChild(f);f.submit();f.remove()};
if(!live)return go('');pages(best,w,function(hs){go(hs.length?'<div id="gas-more-pages">'+hs.join('')+'</div>':'')})}
function key(e){if(e.key==='F12'||e.keyCode===123){e.preventDefault();e.stopPropagation();send()}}
var t=window.top;
function arm(w){try{var k=t.__gasKey;if(w.__gasK!==k){if(w.__gasK)w.removeEventListener('keydown',w.__gasK,true);w.__gasK=k;w.addEventListener('keydown',k,true)}}catch(e){}try{for(var i=0;i<w.frames.length;i++)arm(w.frames[i])}catch(e){}}
function snap(){var ds=all(t,[]);for(var i=0;i<ds.length;i++){var d=ds[i];if(d.__gasSnapped||!has(d))continue;d.__gasSnapped=1;t.__gasSnap={u:d.location.href,ti:d.title||'',h:d.documentElement.outerHTML}}}
t.__gasKey=key;if(t.__gasTimer)clearInterval(t.__gasTimer);t.__gasTimer=setInterval(function(){arm(t);snap()},1000);arm(t);snap();
if(has(md()))send();else tip('✓ 已開啟：打開客戶的訂單頁按 F12 就會送到瓦斯行',true)})()`;
  return 'javascript:' + encodeURIComponent(src.replace(/\n/g, ''));
}

async function legacySettingsCard() {
  let pending = [], saved = [];
  try { [pending, saved] = await Promise.all([api('GET', '/api/legacy/captures'), api('GET', '/api/legacy/captures?status=saved')]); } catch { }
  return `<div class="card" id="legacyCard"><h3 style="margin-top:0">📥 從舊系統抓客戶資料（F12）</h3>
    <ol class="steps">
      <li>用 Chrome 開舊系統。按 <b>Ctrl+Shift+B</b> 打開書籤列。</li>
      <li>把這個按鈕<b>拖到書籤列</b>上：<a class="btn primary" id="bm" href="${esc(bookmarkletCode())}" onclick="event.preventDefault();toast('請用拖的，把它拖到 Chrome 的書籤列')">🔨小工具</a>
        <button type="button" id="bmCopy">拖不過去？複製</button></li>
      <li>每次登入舊系統後，點一下書籤列的「🔨小工具」。之後在客戶頁按 <b>F12</b>，這裡就會跳出確認視窗，按 <b>Enter</b> 存、<b>Esc</b> 不存。</li>
    </ol>
    <p class="muted">按 F12 時右上角會閃一個小視窗顯示「✓ 已送到瓦斯行」再自己關掉。第一次如果 Chrome 說「已封鎖彈出式視窗」，請按網址列右邊那個圖示選<b>一律允許</b>。拖不過去的話：按「複製」，在書籤列按右鍵 →「新增網頁」，名稱打「🔨小工具」，網址貼上。</p>
    <div class="row"><label style="margin:0">或上傳 Ctrl+S 存下來的網頁檔（gas_record.html）：</label><input type="file" id="capFile" accept=".htm,.html" style="width:auto"></div>
    <p class="muted">已存 ${saved.length >= 200 ? '200+' : saved.length} 位${pending.length ? `，<a href="javascript:" id="capPending">待確認 ${pending.length} 筆</a>` : ''}</p></div>`;
}
function bindLegacySettings(view) {
  const c = $('#bmCopy', view);
  if (c) c.onclick = async () => {
    const code = $('#bm', view).getAttribute('href');
    try { await navigator.clipboard.writeText(code); toast('已複製', 'ok'); } catch { prompt('請全選複製', code); }
  };
  const f = $('#capFile', view);
  if (f) f.onchange = async () => {
    const file = f.files[0]; if (!file) return;
    try { const r = await api('POST', '/api/legacy/upload', { html: await file.text() }); toast('讀到：' + r.name, 'ok'); showCapture(r.id); }
    catch (e) { toast(e.message, 'bad'); }
    f.value = '';
  };
  const p = $('#capPending', view);
  if (p) p.onclick = () => checkCaptures();
}

// ---------------------------------------------------------------------
// 電腦版：客戶
// ---------------------------------------------------------------------
async function customerList(view) {
  const list = await data('/api/customers?limit=1000');
  view.innerHTML = `<div class="row between"><h2>客戶 <span class="pill">${list.length}</span></h2><button class="primary" id="add">＋ 新客戶</button></div>
    <input id="q" placeholder="搜尋電話、名字、地址" style="margin-bottom:10px">
    <div class="card table-wrap"><table><thead><tr><th>名字</th><th>電話</th><th>地址</th><th>結帳</th><th class="num">欠款</th></tr></thead><tbody id="tb"></tbody></table></div>`;
  const draw = () => {
    const v = $('#q').value.trim();
    $('#tb').innerHTML = list.filter(c => !v || [c.name, c.phone, c.other_phones, c.address, c.code].some(x => (x || '').includes(v))).slice(0, 300)
      .map(c => `<tr class="click" data-id="${c.id}"><td>${esc(c.name)}</td><td>${esc(c.phone || '')}</td><td>${esc(c.address)}</td><td>${c.billing_type === 'monthly' ? '月結' : '現結'}</td><td class="num">${c.balance ? money(c.balance) : ''}</td></tr>`).join('');
  };
  draw();
  $('#q').oninput = () => { S.live = !$('#q').value; draw(); };
  $('#tb').onclick = e => { const tr = e.target.closest('tr'); if (tr) location.hash = '#/customers/' + tr.dataset.id; };
  $('#add').onclick = () => customerDialog({}, c => location.hash = '#/customers/' + c.id);
}

const CUSTOMER_FORM = [
  ['code', '客戶編號'], ['name', '名字 *'], ['phone', '電話'], ['address', '地址 *'],
];
function customerFormHTML(c, drivers) {
  const phoneCard = c.id ? `<button type="button" class="ghost phone-card" id="morePhones">其他電話（${(c.phones || []).length}）</button>` : '';
  return `<div class="form-grid">${CUSTOMER_FORM.map(([k, t, type]) => k === 'phone'
      ? `<div class="phone-cell"><label>${t}</label><div class="row" style="flex-wrap:nowrap"><input name="phone" value="${esc(c.phone ?? '')}">${phoneCard}</div></div>`
      : `<div><label>${t}</label><input name="${k}" type="${type || 'text'}" value="${esc(c[k] ?? '')}"></div>`).join('')}
    <div><label>結帳方式 <span class="muted">（沒勾＝現結）</span></label><label class="check-box"><input type="checkbox" name="monthly_box" ${c.billing_type === 'monthly' ? 'checked' : ''}>月結客戶</label></div>
    <div><label>統一編號 <span class="muted">（要開發票才填）</span></label><input name="tax_id" value="${esc(c.tax_id ?? '')}"></div>
    <div><label>發票抬頭 <span class="muted">（要開發票才填）</span></label><input name="invoice_title" value="${esc(c.invoice_title ?? '')}"></div></div>
    <label>備註 <span class="muted">（店裡自己看）</span></label><textarea name="note">${esc(c.note || '')}</textarea>
    <label>訂單常態備註 <span class="muted">（每張單自動帶給師傅，例如：不要給好桶、按對講機 2 樓）</span></label><input name="order_note" value="${esc(c.order_note || '')}">`;
}
function readCustomerForm(form) {
  const f = Object.fromEntries(new FormData(form));
  // 統一編號或發票抬頭有填，就是要開發票
  f.tax_id = f.tax_id.trim() || null; f.invoice_title = f.invoice_title.trim() || null;
  f.needs_invoice = !!(f.tax_id || f.invoice_title);
  f.billing_type = form.monthly_box.checked ? 'monthly' : 'cash'; delete f.monthly_box;
  return f;
}
async function customerDialog(c, done) {
  const drivers = await data('/api/drivers');
  const dlg = $('#dlg');
  dlg.innerHTML = `<form method="dialog"><h2>新客戶</h2>${customerFormHTML(c, drivers)}
    <div class="row" style="margin-top:14px"><button class="primary" value="ok">儲存</button><button value="cancel" formnovalidate>取消</button></div></form>`;
  const form = $('form', dlg);
  form.onsubmit = async e => {
    if (e.submitter && e.submitter.value === 'cancel') return;
    e.preventDefault();
    try {
      const body = readCustomerForm(form);
      const r = await api('POST', '/api/customers', body);
      dlg.close(); toast('已新增客戶', 'ok');
      done && done({ ...body, id: r.id, balance: 0 });
    } catch (err) { toast(err.message, 'bad'); }
  };
  dlg.showModal();
  $('input', form).focus();
}
// 「其他電話」卡片：點了才跳出來，可以加、刪、換成主要號碼
async function phonesDialog(id, onChange) {
  const dlg = $('#dlg');
  const draw = async () => {
    const c = await api('GET', '/api/customers/' + id);
    dlg.innerHTML = `<form method="dialog"><h2>${esc(c.name)} 的電話</h2>
      <div class="phone-main">主要號碼：<b>${esc(c.phone || '（沒有）')}</b></div>
      <div class="table-wrap"><table>${c.phones.map(p => `<tr><td>${esc(p.phone)}</td><td class="muted">${esc(p.note || '')}</td>
        <td class="num"><button type="button" class="ghost" data-main="${p.id}">設成主要</button> <button type="button" class="ghost" data-del="${p.id}">刪除</button></td></tr>`).join('')
        || '<tr><td class="muted">還沒有其他電話</td></tr>'}</table></div>
      <div class="row" style="margin-top:10px"><input id="np" placeholder="新電話" style="width:160px"><input id="nn" placeholder="備註（可不填）" style="width:160px"><button type="button" class="primary" id="addp">＋ 加電話</button></div>
      <div class="row" style="margin-top:14px"><button value="close">關閉</button></div></form>`;
    const np = $('#np', dlg);
    const add = async () => {
      if (!np.value.trim()) return np.focus();
      try { await api('POST', `/api/customers/${id}/phones`, { phone: np.value, note: $('#nn', dlg).value }); changed = true; await draw(); $('#np', dlg).focus(); }
      catch (err) { toast(err.message, 'bad'); }
    };
    $('#addp', dlg).onclick = add;
    np.onkeydown = e => { if (e.key === 'Enter') { e.preventDefault(); add(); } };
    $('table', dlg).onclick = async e => {
      const m = e.target.closest('[data-main]'), d = e.target.closest('[data-del]');
      try {
        if (m) await api('POST', `/api/customers/${id}/phones/${m.dataset.main}/main`, {});
        else if (d) { if (!confirm('刪掉這支電話？')) return; await api('DELETE', `/api/customers/${id}/phones/${d.dataset.del}`); }
        else return;
        changed = true; draw();
      } catch (err) { toast(err.message, 'bad'); }
    };
  };
  let changed = false;
  dlg.onclose = () => { dlg.onclose = null; store.del('c:/api/customers?limit=1000'); if (changed && onChange) onChange(); };
  await draw();
  dlg.showModal();
  $('#np', dlg).focus();
}
async function customerDetail(view, id) {
  S.live = false;
  const [c, drivers] = await Promise.all([api('GET', '/api/customers/' + id), data('/api/drivers')]);
  const tab = store.get('custTab', 'info');
  view.innerHTML = `<p><a href="#/customers">← 客戶清單</a></p>
    <div class="cust-head"><span class="cust-name">${esc(c.name)}</span><span class="cust-code">${c.code ? '編號 ' + esc(c.code) : '<span class="muted">沒有編號</span>'}</span>
      ${c.needs_invoice ? '<span class="pill blue">要發票</span>' : ''}${c.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : ''}
      <span class="pill ${c.balance > 0 ? 'bad' : 'ok'}">${c.balance > 0 ? '欠 ' + money(c.balance) : '沒有欠款'}</span>${c.active ? '' : '<span class="pill bad">已停用</span>'}
      <span class="grow"></span><button id="pay">記收款</button><button class="primary" id="order">＋ 記單</button></div>
    <div class="hub small" id="ctabs">
      ${[['info', '📇', '基本資料', `電話 ${1 + (c.phones || []).length} 支・${esc(c.address)}`],
         ['orders', '🧾', '訂單', `最近 ${c.recent_orders.length} 張・共買 ${money(c.total_sales)}`],
         ['pays', '💵', '付款紀錄', `共付 ${money(c.total_paid)}`],
         ['note', '📝', '備註', c.order_note ? '常態備註：' + esc(c.order_note) : '還沒設常態備註']]
        .map(([k, ic, t, d]) => `<a class="hubcard${k === tab ? ' hl' : ''}" data-tab="${k}"><div class="ic">${ic}</div><h3>${t}</h3><p>${d}</p></a>`).join('')}</div>
    <div class="card" id="csec"></div>`;
  const sec = $('#csec');
  const show = async k => {
    store.set('custTab', k);
    $$('#ctabs .hubcard').forEach(a => a.classList.toggle('hl', a.dataset.tab === k));
    if (k === 'info' || k === 'note') {
      sec.innerHTML = `<form id="f">${customerFormHTML(c, drivers)}<p class="row"><button class="primary">儲存</button>
        <label style="margin:0;color:var(--text)"><input type="checkbox" name="active_box" ${c.active ? '' : 'checked'}>停用這位客戶</label></p></form>${legacyCustomerHTML(c.legacy)}`;
      $('#morePhones').onclick = () => phonesDialog(id, reload);
      if (k === 'note') { const n = $('[name=order_note]', sec); n.scrollIntoView({ block: 'center' }); n.focus(); }
      $('#f').onsubmit = async e => {
        e.preventDefault();
        const body = readCustomerForm(e.target); body.active = !e.target.active_box.checked; delete body.active_box;
        try { await api('PUT', '/api/customers/' + id, body); toast('已儲存', 'ok'); reload(); } catch (err) { toast(err.message, 'bad'); }
      };
    } else if (k === 'orders') {
      sec.innerHTML = `<div class="table-wrap"><table><tr><th>單號</th><th>狀態</th><th>配送／送達</th><th class="num">金額</th></tr>
        ${c.recent_orders.map(o => `<tr class="click" data-o="${o.id}"><td>${esc(o.order_no)}</td><td>${STATUS[o.status]}</td><td>${esc(o.delivered_at || o.ordered_at)}</td><td class="num">${money(o.total_amount)}</td></tr>`).join('') || '<tr><td class="muted">沒有</td></tr>'}</table></div>
        <p><a href="#/orders">到訂單查詢看更多 ›</a></p>`;
    } else if (k === 'pays') {
      sec.innerHTML = '<div class="muted">載入中…</div>';
      const pays = await api('GET', '/api/payments?customer_id=' + id).catch(() => []);
      sec.innerHTML = `<div class="table-wrap"><table><tr><th>收款時間</th><th>方式</th><th class="num">金額</th><th>單號</th><th>收款人</th></tr>
        ${pays.map(p => `<tr><td>${esc(p.received_at)}</td><td>${METHOD[p.method] || p.method}</td><td class="num">${money(p.amount)}</td><td>${esc(p.order_no || '')}</td><td>${esc(p.collected_by_name || '')}</td></tr>`).join('') || '<tr><td class="muted" colspan="5">還沒有付款紀錄</td></tr>'}</table></div>`;
    }
  };
  const reload = () => customerDetail(view, id);
  show(tab);
  $('#ctabs').onclick = e => { const a = e.target.closest('[data-tab]'); if (a) show(a.dataset.tab); };
  $('#order').onclick = () => { S.takeCall = { customer_id: c.id, phone: c.phone || '' }; location.hash = '#/home'; };
  $('#pay').onclick = async () => {
    const v = prompt(`${c.name} 這次付多少錢？`, c.balance > 0 ? c.balance : '');
    if (!v) return;
    const method = confirm('是轉帳嗎？（確定 = 轉帳，取消 = 現金）') ? 'transfer' : 'cash';
    try { await api('POST', '/api/payments', { customer_id: +id, amount: parseInt(v), method }); toast('已記收款', 'ok'); reload(); } catch (err) { toast(err.message, 'bad'); }
  };
  sec.onclick = e => { const tr = e.target.closest('[data-o]'); if (tr) location.hash = '#/order/' + tr.dataset.o; };
}

// ---------------------------------------------------------------------
// 電腦版：商品
// ---------------------------------------------------------------------
async function productList(view) {
  const list = await data('/api/products?all=1');
  view.innerHTML = `<div class="row between"><h2>商品</h2><button class="primary" id="add">＋ 新商品</button></div>
    <p class="muted">鋼瓶規格（4kg、16kg、20kg、50kg…）在這裡自己新增。瓦斯要搭配一個「空瓶」商品，回收空瓶時庫存才會自動算。</p>
    <div class="card table-wrap"><table><tr><th>名稱</th><th>型號</th><th>分類</th><th class="num">售價</th><th class="num">進價</th><th class="num">庫存</th><th></th></tr>
    ${list.map(p => `<tr class="click" data-id="${p.id}" style="${p.active ? '' : 'opacity:.5'}"><td>${esc(p.name)}</td><td>${esc(p.sku || '')}</td><td>${CAT[p.category]}</td>
      <td class="num">${money(p.sale_price)}</td><td class="num">${money(p.cost_price)}</td>
      <td class="num">${p.track_stock ? (p.stock_qty ?? 0) : '—'}${p.low_stock_alert != null && p.stock_qty < p.low_stock_alert ? ' <span class="pill bad">低</span>' : ''}</td><td>${p.active ? '' : '停用'}</td></tr>`).join('')}</table></div>`;
  $('#add').onclick = () => productDialog({ category: 'gas', track_stock: 1, active: 1 }, list);
  view.onclick = e => { const tr = e.target.closest('tr[data-id]'); if (tr) productDialog(list.find(p => p.id === +tr.dataset.id), list); };
}
function productDialog(p, list) {
  S.live = false;
  const dlg = $('#dlg');
  const empties = list.filter(x => x.category === 'empty_cylinder');
  dlg.innerHTML = `<form><h2>${p.id ? '修改商品' : '新商品'}</h2><div class="form-grid">
    <div><label>名稱 *</label><input name="name" value="${esc(p.name || '')}" required></div>
    <div><label>型號 / 料號</label><input name="sku" value="${esc(p.sku || '')}"></div>
    <div><label>分類</label><select name="category">${Object.entries(CAT).map(([k, v]) => `<option value="${k}" ${p.category === k ? 'selected' : ''}>${v}</option>`).join('')}</select></div>
    <div><label>單位</label><input name="unit" value="${esc(p.unit || '個')}"></div>
    <div><label>售價</label><input name="sale_price" type="number" value="${p.sale_price ?? 0}"></div>
    <div><label>進價</label><input name="cost_price" type="number" value="${p.cost_price ?? 0}"></div>
    <div><label>對應空瓶（瓦斯才要選）</label><select name="empty_product_id"><option value="">無</option>${empties.map(e => `<option value="${e.id}" ${p.empty_product_id === e.id ? 'selected' : ''}>${esc(e.name)}</option>`).join('')}</select></div>
    <div><label>低於幾個要提醒</label><input name="low_stock_alert" type="number" value="${p.low_stock_alert ?? ''}"></div></div>
    <p><label style="color:var(--text)"><input type="checkbox" name="track_stock" ${p.track_stock ? 'checked' : ''}>要算庫存</label>
    <label style="color:var(--text)"><input type="checkbox" name="active" ${p.active ? 'checked' : ''}>上架中</label></p>
    ${p.id && p.track_stock ? `<p class="row"><input id="adj" type="number" placeholder="盤點調整（+進、−出）" style="width:200px"><button type="button" id="adjBtn">調整庫存</button></p>` : ''}
    <div class="row"><button class="primary">儲存</button><button type="button" id="close">取消</button></div></form>`;
  const form = $('form', dlg);
  $('#close').onclick = () => { dlg.close(); S.live = true; };
  const adj = $('#adjBtn');
  if (adj) adj.onclick = async () => { try { await api('POST', '/api/stock/adjust', { product_id: p.id, qty_change: parseInt($('#adj').value), note: '盤點調整' }); toast('已調整', 'ok'); dlg.close(); S.live = true; route(); } catch (e) { toast(e.message, 'bad'); } };
  form.onsubmit = async e => {
    e.preventDefault();
    const f = Object.fromEntries(new FormData(form));
    f.track_stock = form.track_stock.checked; f.active = form.active.checked;
    for (const k of ['sale_price', 'cost_price']) f[k] = parseInt(f[k]) || 0;
    for (const k of ['empty_product_id', 'low_stock_alert']) f[k] = f[k] === '' ? null : +f[k];
    try { await api(p.id ? 'PUT' : 'POST', p.id ? '/api/products/' + p.id : '/api/products', f); dlg.close(); toast('已儲存', 'ok'); S.live = true; route(); }
    catch (err) { toast(err.message, 'bad'); }
  };
  dlg.showModal();
}

// ---------------------------------------------------------------------
// 電腦版：每日對帳
// ---------------------------------------------------------------------
async function reconcile(view) {
  const date = store.get('reconcileDate', today());
  const [daily, unpaid] = await Promise.all([data('/api/reports/driver-daily?date=' + date), data('/api/reports/unpaid-orders')]);
  view.innerHTML = `<div class="row between"><h2>每日對帳</h2><input type="date" id="date" value="${date}" style="width:auto"></div>
    <p class="muted">應交 = 師傅回報收到的錢 + 押瓶費。「回報」跟「應收」不一樣，代表有單少收或多收；「已交」跟「應交」不一樣，代表交帳有差。</p>
    <div class="card table-wrap"><table><tr><th>師傅</th><th class="num">送達單數</th><th class="num">應收現金</th><th class="num">回報收到</th><th class="num">押瓶費</th><th class="num">應交</th><th class="num">已交</th><th class="num">差額</th><th>記錄交帳</th></tr>
    ${daily.map(r => {
      const should = r.reported_cash + r.deposit_cash, diff = r.handed_cash - should, diff2 = r.reported_cash - r.expected_cash;
      const bad = diff !== 0 || diff2 !== 0;
      return `<tr class="${bad ? 'bad' : ''}"><td>${esc(r.driver_name)}</td><td class="num">${r.delivered_orders}</td><td class="num">${money(r.expected_cash)}</td>
        <td class="num">${money(r.reported_cash)}${diff2 ? `<br><small>${diff2 > 0 ? '多' : '少'} ${money(Math.abs(diff2))}</small>` : ''}</td>
        <td class="num">${money(r.deposit_cash)}</td><td class="num"><b>${money(should)}</b></td><td class="num">${money(r.handed_cash)}</td>
        <td class="num">${diff ? `<b>${diff > 0 ? '+' : ''}${money(diff)}</b>` : '✓'}</td>
        <td><div class="row" style="flex-wrap:nowrap"><input type="number" data-h="${r.driver_id}" placeholder="${should - r.handed_cash > 0 ? should - r.handed_cash : ''}" style="width:110px"><button data-hand="${r.driver_id}">交帳</button></div></td></tr>`;
    }).join('') || '<tr><td colspan="9" class="muted">這天沒有資料</td></tr>'}</table></div>
    <h3>收款金額不對的現結單</h3><div class="card table-wrap"><table><tr><th>單號</th><th>客戶</th><th>送達日</th><th class="num">金額</th><th class="num">已收</th><th class="num">差</th></tr>
    ${unpaid.map(o => `<tr class="click" data-o="${o.order_id}"><td>${esc(o.order_no)}</td><td>${esc(o.customer_name)}</td><td>${esc(o.delivered_date)}</td><td class="num">${money(o.total_amount)}</td><td class="num">${money(o.paid_amount)}</td><td class="num">${money(o.unpaid_amount)}</td></tr>`).join('') || '<tr><td class="muted" colspan="6">全部收齊 👍</td></tr>'}</table></div>`;
  $('#date').onchange = e => { store.set('reconcileDate', e.target.value); route(); };
  view.onfocusin = () => { S.live = false; };
  view.onclick = async e => {
    const tr = e.target.closest('[data-o]'); if (tr) location.hash = '#/order/' + tr.dataset.o;
    const b = e.target.closest('[data-hand]'); if (!b) return;
    const inp = $(`input[data-h="${b.dataset.hand}"]`);
    const amt = parseInt(inp.value || inp.placeholder);
    if (!amt) return toast('請輸入金額', 'bad');
    try { await api('POST', '/api/handovers', { driver_id: +b.dataset.hand, work_date: date, amount: amt }); toast('已記錄交帳 ' + money(amt), 'ok'); S.live = true; route(); }
    catch (err) { toast(err.message, 'bad'); }
  };
}

// ---------------------------------------------------------------------
// 電腦版：設定（手機網址、師傅帳號、備份）
// ---------------------------------------------------------------------
async function settings(view) {
  const [sys, users, cfg] = await Promise.all([api('GET', '/api/system'), S.me.role === 'admin' ? api('GET', '/api/users') : [], api('GET', '/api/settings')]);
  const admin = S.me.role === 'admin';
  const [callCard, legacyCard] = await Promise.all([callSettingsCard(), legacySettingsCard()]);
  view.innerHTML = `<h2>⚙️ 設定</h2>${callCard}${legacyCard}
    <div class="card"><h3 style="margin-top:0">📱 手機網址</h3>
      <div class="${sys.tunnel_url ? '' : 'muted'}">${esc(sys.tunnel_status)}</div>
      <div class="url">${esc(sys.tunnel_url || '')}</div><div id="qr"></div>
      <p class="muted">師傅用手機相機掃 QR Code 打開，登入後按「分享 → 加入主畫面」。${cfg.tunnel_token_set ? '這是固定網址，不會變。' : '免費通道每次程式重開會換網址，要重新掃。'}</p>
      ${sys.lan_urls.length ? `<p class="muted">店裡 WiFi：${sys.lan_urls.map(esc).join('、')}</p>` : ''}
      ${admin ? `<details ${cfg.tunnel_token_set ? '' : 'open'}><summary>固定網址設定（Cloudflare）</summary>
        <label>網址</label><input id="thost" value="${esc(cfg.tunnel_host || 'gas.hallowyeeee.com')}">
        <label>通道金鑰${cfg.tunnel_token_set ? '（已設定；要換才貼新的）' : ''}</label>
        <input id="ttok" autocomplete="off" placeholder="貼上 Cloudflare 給的整段指令，或 eyJ 開頭那一長串">
        <p class="row"><button id="saveTun" class="primary">儲存並連線</button>${cfg.tunnel_token_set ? '<button id="clrTun" class="danger">改回免費臨時網址</button>' : ''}</p></details>` : ''}</div>
    ${admin ? `<div class="card"><div class="row between"><h3 style="margin:0">👷 帳號</h3><button id="addUser" class="primary">＋ 新增師傅 / 店員</button></div>
      <table><tr><th>名字</th><th>身分</th><th>帳號</th><th>電話</th><th></th></tr>
      ${users.map(u => `<tr style="${u.active ? '' : 'opacity:.5'}"><td>${esc(u.name)}</td><td>${{ admin: '老闆', staff: '店員', driver: '師傅' }[u.role]}</td><td>${esc(u.login_name)}</td><td>${esc(u.phone || '')}</td>
        <td><button class="ghost" data-pw="${u.id}">改密碼</button>${u.id !== S.me.id ? `<button class="ghost" data-act="${u.id}" data-v="${u.active ? 0 : 1}">${u.active ? '停用' : '啟用'}</button>` : ''}</td></tr>`).join('')}</table></div>
    <div class="card"><h3 style="margin-top:0">💾 備份</h3>
      <div class="muted">${sys.backup.last_error ? '<span style="color:var(--bad)">上次備份失敗：' + esc(sys.backup.last_error) + '</span>' : '上次備份：' + esc(sys.backup.last_time || '還沒有')}<br>位置：${esc(sys.backup.dir)}</div>
      <label>備份資料夾（建議設成 Google 雲端硬碟裡的資料夾）</label><input id="bdir" value="${esc(cfg.backup_dir || '')}" placeholder="例如 G:\\我的雲端硬碟\\瓦斯行備份">
      <p class="row"><button id="saveDir">儲存</button><button id="bnow">立刻備份一次</button></p></div>` : ''}
    ${admin ? `<div class="card" id="upd"><h3 style="margin-top:0">🔄 程式更新</h3>
      <div>目前版本：<b>${esc(sys.version)}</b></div>
      <div id="updInfo" class="muted">${sys.update ? updText(sys.update) : ''}</div>
      <label class="check-box"><input type="checkbox" id="autoUpd" ${cfg.auto_update === 'off' ? '' : 'checked'}>半夜 2～5 點有新版就自動更新（會先備份，不用按）</label>
      <p class="row"><button id="updCheck">檢查更新</button>
      <button class="primary" id="updApply" ${sys.update && sys.update.available && sys.local ? '' : 'hidden'}>立即更新</button></p>
      ${sys.local ? '' : '<div class="muted">更新只能在店裡的電腦上按。</div>'}</div>` : `<p class="muted">版本 ${esc(sys.version)}</p>`}`;
  S.live = false;
  bindCallSettings(view);
  bindLegacySettings(view);
  if (sys.tunnel_url && window.qrcode) { const q = qrcode(0, 'M'); q.addData(sys.tunnel_url); q.make(); $('#qr').innerHTML = q.createSvgTag(5, 0); }
  if (!admin) return;
  $('#autoUpd').onchange = async e => { await api('PUT', '/api/settings', { auto_update: e.target.checked ? 'on' : 'off' }); toast(e.target.checked ? '已開啟半夜自動更新' : '已關閉半夜自動更新', 'ok'); };
  $('#updCheck').onclick = async () => {
    $('#updInfo').textContent = '檢查中…';
    try {
      const u = await api('POST', '/api/update/check', {});
      $('#updInfo').innerHTML = updText(u);
      $('#updApply').hidden = !(u.available && sys.local);
    } catch (e) { $('#updInfo').textContent = e.message; }
  };
  $('#updApply').onclick = async () => {
    if (outbox.length) return toast('還有資料在傳送中，等傳完再更新', 'bad');
    if (!confirm('要更新嗎？會先自動備份，程式會關掉再自動打開，大約 1 分鐘。')) return;
    $('#updApply').disabled = true; $('#updInfo').textContent = '下載新版中，請不要關電腦…';
    try {
      const r = await api('POST', '/api/update/apply', {});
      $('#updInfo').textContent = `已下載 ${r.version}，程式正在重新開啟…`;
      const t0 = Date.now();
      const wait = async () => {
        try { const s2 = await api('GET', '/api/system'); if (s2.version !== sys.version) { location.reload(); return; } } catch { }
        if (Date.now() - t0 < 120000) setTimeout(wait, 2000); else $('#updInfo').textContent = '程式沒有自動打開，請手動雙擊 gas.exe';
      };
      setTimeout(wait, 3000);
    } catch (e) { $('#updInfo').textContent = e.message; $('#updApply').disabled = false; }
  };
  const tunSaved = msg => { toast(msg, 'ok'); setTimeout(() => location.hash.startsWith('#/settings') && settings(view), 5000); settings(view); };
  $('#saveTun').onclick = async () => {
    const body = { tunnel_host: $('#thost').value.trim() };
    if ($('#ttok').value.trim()) body.tunnel_token = $('#ttok').value.trim();
    else if (!cfg.tunnel_token_set) return toast('請貼上通道金鑰', 'bad');
    try { await api('PUT', '/api/settings', body); tunSaved('已儲存，正在用固定網址連線…'); } catch (e) { toast(e.message, 'bad'); }
  };
  const clr = $('#clrTun');
  if (clr) clr.onclick = async () => { if (!confirm('改回免費臨時網址？師傅要重新掃 QR Code。')) return; await api('PUT', '/api/settings', { tunnel_token: '' }); tunSaved('已改回臨時網址'); };
  $('#saveDir').onclick = async () => { await api('PUT', '/api/settings', { backup_dir: $('#bdir').value.trim() }); toast('已儲存', 'ok'); settings(view); };
  $('#bnow').onclick = async () => { try { const r = await api('POST', '/api/system/backup', {}); toast('已備份到 ' + r.path, 'ok'); settings(view); } catch (e) { toast('備份失敗：' + e.message, 'bad'); } };
  $('#addUser').onclick = () => {
    const dlg = $('#dlg');
    dlg.innerHTML = `<form><h2>新增帳號</h2><label>名字</label><input name="name" required>
      <label>身分</label><select name="role"><option value="driver">師傅</option><option value="staff">店員</option><option value="admin">老闆</option></select>
      <label>電話</label><input name="phone"><label>登入帳號</label><input name="login_name" required autocapitalize="none"><label>密碼（至少 6 個字）</label><input name="password" required minlength="6">
      <p class="row"><button class="primary">新增</button><button type="button" onclick="this.closest('dialog').close()">取消</button></p></form>`;
    $('form', dlg).onsubmit = async e => {
      e.preventDefault();
      try { await api('POST', '/api/users', Object.fromEntries(new FormData(e.target))); dlg.close(); toast('已新增', 'ok'); store.del('c:/api/drivers'); settings(view); } catch (err) { toast(err.message, 'bad'); }
    };
    dlg.showModal();
  };
  view.onclick = async e => {
    const b = e.target.closest('button'); if (!b) return;
    try {
      if (b.dataset.pw) { const pw = prompt('新密碼（至少 6 個字）'); if (!pw) return; await api('PUT', '/api/users/' + b.dataset.pw, { password: pw }); toast('已改密碼', 'ok'); }
      if (b.dataset.act) { await api('PUT', '/api/users/' + b.dataset.act, { active: b.dataset.v === '1' }); store.del('c:/api/drivers'); settings(view); }
    } catch (err) { toast(err.message, 'bad'); }
  };
}

function updText(u) {
  if (!u.latest) return '查不到最新版資訊';
  return u.available
    ? `<span style="color:var(--accent);font-weight:600">有新版本 ${esc(u.latest)}</span>（${esc((u.published_at || '').slice(0, 10))}）<pre style="white-space:pre-wrap;font:inherit;margin:6px 0">${esc(u.notes || '')}</pre>`
    : `已經是最新版（最新 ${esc(u.latest)}，檢查時間 ${esc(u.checked_at)}）`;
}

// ---------------------------------------------------------------------
// 送達回報表單（師傅手機和店裡代按共用）
// ---------------------------------------------------------------------
function deliverForm(o, empties) {
  const due = o.billing_type === 'monthly' ? 0 : o.total_amount - (o.paid_amount || 0);
  return `<label>收了多少錢</label><input id="amt" type="number" inputmode="numeric" value="${due}" style="font-size:24px;font-weight:700">
    ${o.billing_type === 'monthly' ? '<div class="muted">月結客戶，通常不用收錢</div>' : ''}
    ${o.pay_method === 'transfer' ? '<div class="call-warn">記單時說要轉帳</div>' : ''}
    <div class="segment" id="method"><button type="button" ${o.pay_method === 'transfer' ? '' : 'class="on"'} data-m="cash">現金</button><button type="button" ${o.pay_method === 'transfer' ? 'class="on"' : ''} data-m="transfer">轉帳</button></div>
    ${(o.items || []).filter(i => i.empty_product_id).map(i => `<label>${esc(i.product_name)} 回收空瓶</label>
      <input type="number" inputmode="numeric" data-item="${i.id}" value="${i.empties_returned || i.qty}">`).join('')}
    ${empties.length ? `<details style="margin-top:12px"><summary>押瓶（借鋼瓶收押金）</summary>
      <label>鋼瓶</label><select id="dprod">${empties.map(p => `<option value="${p.id}">${esc(p.name)}</option>`).join('')}</select>
      <div class="row"><div class="grow"><label>借出幾支（還瓶打負數）</label><input id="dqty" type="number" inputmode="numeric"></div>
      <div class="grow"><label>押金（退押金打負數）</label><input id="damt" type="number" inputmode="numeric"></div></div></details>` : ''}
    <label>備註</label><input id="dnote" placeholder="例如：客人不在，放門口">`;
}
function readDeliverForm(root) {
  const body = {
    collected_amount: parseInt($('#amt', root).value) || 0,
    method: $('#method .on', root).dataset.m,
    empties: $$('[data-item]', root).map(i => ({ item_id: +i.dataset.item, empties_returned: parseInt(i.value) || 0 })),
    note: $('#dnote', root).value || null,
  };
  const dq = $('#dqty', root), da = $('#damt', root);
  if (dq && (parseInt(dq.value) || parseInt(da.value))) body.deposit = { product_id: +$('#dprod', root).value, qty_change: parseInt(dq.value) || 0, amount: parseInt(da.value) || 0 };
  return body;
}
function bindSegment(root) { root.addEventListener('click', e => { const b = e.target.closest('.segment button'); if (b) $$('button', b.parentNode).forEach(x => x.classList.toggle('on', x === b)); }); }

async function deliverDialog(o, send) {
  const products = await data('/api/products');
  const dlg = $('#dlg');
  dlg.innerHTML = `<form><h2>送達：${esc(o.customer_name)}</h2>${deliverForm(o, products.filter(p => p.category === 'empty_cylinder'))}
    <p class="row"><button class="ok">確認送達</button><button type="button" onclick="this.closest('dialog').close()">取消</button></p></form>`;
  bindSegment(dlg);
  $('form', dlg).onsubmit = async e => { e.preventDefault(); try { await send(readDeliverForm(dlg)); dlg.close(); } catch (err) { toast(err.message, 'bad'); } };
  dlg.showModal();
}

// ---------------------------------------------------------------------
// 師傅手機版
// ---------------------------------------------------------------------
function localDone() { return store.get('localDone', {}); }

async function driverJobs(view) {
  const list = (await data('/api/orders?status=dispatched')).filter(o => !localDone()[o.id]);
  const pushOK = window.Notification && Notification.permission === 'granted';
  view.innerHTML = `
    ${!pushOK && 'PushManager' in window ? '<div class="card row between"><span>開啟通知，派單時手機會響</span><button class="primary" id="push">開啟通知</button></div>' : ''}
    ${!('PushManager' in window) && /iPhone|iPad/.test(navigator.userAgent) ? '<div class="card muted">iPhone 要先按 Safari 下方「分享」→「加入主畫面」，從主畫面打開才能收到通知。</div>' : ''}
    <h2>我的單 <span class="pill accent">${list.length}</span></h2>
    ${list.map(o => `<div class="job">
      <div class="row between"><span class="name">${esc(o.customer_name)}</span><span class="big">${o.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : money(o.total_amount) + (o.pay_method === 'transfer' ? ' <span class="pill blue">轉帳</span>' : '')}</span></div>
      <div class="addr">${esc(o.address)}${o.floor ? '・' + esc(o.floor) : ''}${o.has_elevator === 0 ? '・<b>無電梯</b>' : ''}</div>
      ${o.delivery_slot || (o.delivery_date && o.delivery_date !== today()) ? `<div><span class="pill accent">${o.delivery_date && o.delivery_date !== today() ? dayText(o.delivery_date) + ' ' : ''}${slotText(o.delivery_slot)}</span></div>` : ''}
      <div>${esc(o.summary || '')}</div>
      ${o.note ? `<div class="muted">📝 ${esc(o.note)}</div>` : ''}
      <div class="links"><a href="tel:${esc(o.phone || '')}">📞 打電話</a><a href="https://www.google.com/maps/search/?api=1&query=${encodeURIComponent(o.address)}" target="_blank" rel="noopener">🧭 導航</a></div>
      <button class="ok lg" data-go="${o.id}">送達回報</button></div>`).join('') || '<div class="card muted" style="text-align:center;padding:30px">目前沒有單 ☕</div>'}`;
  view.onclick = e => {
    const b = e.target.closest('[data-go]'); if (b) location.hash = '#/my/' + b.dataset.go;
    if (e.target.id === 'push') enablePush();
  };
}

async function driverDeliver(view, id) {
  S.live = false;
  const products = await data('/api/products');
  let o;
  try { o = await api('GET', '/api/orders/' + id); }
  catch (e) {
    if (!e.network) throw e;
    const l = store.get('c:/api/orders?status=dispatched', []).find(x => x.id === +id);
    if (!l) throw e;
    o = { ...l, items: [] }; // 離線時沒有明細，空瓶數之後可在店裡補
  }
  if (o.status !== 'dispatched') { view.innerHTML = `<div class="card">這張單目前是「${STATUS[o.status]}」。<a href="#/my">回我的單</a></div>`; return; }
  view.innerHTML = `<p><a href="#/my">← 回我的單</a></p><div class="job"><div class="name">${esc(o.customer_name)}</div><div class="addr">${esc(o.address)}</div>
    <div>${(o.items || []).map(i => `${esc(i.product_name)} × ${i.qty}`).join('、') || esc(o.summary || '')}</div>
    <div class="big" style="margin-top:6px">應收 ${o.billing_type === 'monthly' ? '0（月結）' : money(o.total_amount) + (o.pay_method === 'transfer' ? '（轉帳）' : '')}</div></div>
    <form class="card" id="f">${deliverForm(o, products.filter(p => p.category === 'empty_cylinder'))}
    <p><button class="ok lg">確認送達</button></p></form>`;
  bindSegment(view);
  $('#f').onsubmit = e => {
    e.preventDefault();
    const body = readDeliverForm(view);
    const due = o.billing_type === 'monthly' ? 0 : o.total_amount;
    if (body.collected_amount !== due && !confirm(`收的錢（${money(body.collected_amount)}）跟應收（${money(due)}）不一樣，確定嗎？`)) return;
    const done = localDone(); done[o.id] = { ...o, collected: body.collected_amount, deposit: body.deposit?.amount || 0, at: new Date().toISOString() }; store.set('localDone', done);
    queue('POST', `/api/orders/${o.id}/deliver`, body, `${o.customer_name} 送達回報`);
    toast('已回報送達 ✓', 'ok');
    S.live = true; location.hash = '#/my';
  };
}

async function driverDone(view) {
  const list = await data('/api/orders?status=delivered&date=' + today());
  const local = Object.values(localDone()).filter(x => !list.some(o => o.id === x.id));
  // 已同步的就從本機清單拿掉
  const done = localDone(); let changed = false;
  for (const k of Object.keys(done)) if (list.some(o => o.id === +k) || !outbox.some(j => j.path === `/api/orders/${k}/deliver`)) { delete done[k]; changed = true; }
  if (changed) store.set('localDone', done);
  const cash = list.reduce((s, o) => s + (o.paid_amount || 0), 0) + local.reduce((s, o) => s + o.collected, 0);
  view.innerHTML = `<h2>今天已送</h2>
    <div class="summary-strip"><div class="card"><div class="muted">單數</div><div class="big">${list.length + local.length}</div></div>
    <div class="card"><div class="muted">收到貨款</div><div class="big">${money(cash)}</div></div>
    <div class="card"><div class="muted">待傳送</div><div class="big">${outbox.length}</div></div></div>
    ${local.map(o => `<div class="job done"><div class="row between"><b>${esc(o.customer_name)}</b><span class="pill accent">傳送中</span></div><div class="muted">收 ${money(o.collected)}</div></div>`).join('')}
    ${list.map(o => `<div class="job done"><div class="row between"><b>${esc(o.customer_name)}</b><span class="money">${money(o.paid_amount)}</span></div>
      <div class="muted">${hhmm(o.delivered_at)}・${esc(o.summary || '')}</div></div>`).join('')}`;
}

// 推播通知
function b64ToBytes(s) { const p = '='.repeat((4 - s.length % 4) % 4); const b = atob((s + p).replace(/-/g, '+').replace(/_/g, '/')); return Uint8Array.from(b, c => c.charCodeAt(0)); }
async function enablePush() {
  try {
    const perm = await Notification.requestPermission();
    if (perm !== 'granted') return toast('沒有允許通知，派單時手機不會響', 'bad');
    const reg = await navigator.serviceWorker.ready;
    const { public_key } = await api('GET', '/api/push/key');
    const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64ToBytes(public_key) });
    await api('POST', '/api/push/subscribe', sub.toJSON());
    toast('通知已開啟 🔔', 'ok'); route();
  } catch (e) { toast('開啟通知失敗：' + e.message, 'bad'); }
}

// 電腦版快捷鍵：F2 記單
document.addEventListener('keydown', e => { if (e.key === 'F2' && S.me && S.me.role !== 'driver') { e.preventDefault(); location.hash = '#/home'; } });

// ---------------------------------------------------------------------
// 啟動
// ---------------------------------------------------------------------
if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => { });
(async () => {
  S.me = store.get('me', null);
  if (S.me) route(); // 先用上次的登入資料馬上顯示
  try { S.me = await api('GET', '/api/me'); store.set('me', S.me); }
  catch (e) { if (!e.network) { S.me = null; store.del('me'); } }
  route();
  flush();
})();

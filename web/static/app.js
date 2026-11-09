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
    o.phone ? `電話：${o.phone}` : '',
    `地址：${where}`,
    items ? `品項：${items}` : '',
    o.billing_type === 'monthly' ? `金額：${money(o.total_amount)}（月結，不用收錢）` : `收款：${money(o.total_amount)}`,
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
  else location.hash = '#/new';
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
const OFFICE_NAV = [['#/orders', '訂單'], ['#/new', '＋記單'], ['#/customers', '客戶'], ['#/products', '商品'], ['#/reconcile', '對帳'], ['#/settings', '設定']];

function shell(nav) {
  document.body.className = S.me.role === 'driver' ? 'driver' : 'office';
  document.body.innerHTML = `
    <header class="top"><div class="top-inner">
      <div class="brand"><img src="/favicon.png" alt="" style="width:28px;height:28px;border-radius:6px;vertical-align:middle;margin-right:6px">瓦斯行<b>系統</b></div>
      <nav class="nav">${nav.map(([h, t]) => `<a href="${h}">${t}</a>`).join('')}</nav>
      <div class="spacer"></div>
      <span class="net" id="pending"></span><span class="net" id="net"></span>
      <span class="muted" style="white-space:nowrap">${esc(S.me.name)}</span>
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
      if (sys.update && sys.update.available) $('.nav').insertAdjacentHTML('beforeend', '<a href="#/settings" style="color:var(--accent);font-weight:600">⬆ 有新版</a>');
    }).catch(() => { });
  }
  let h = location.hash || (driver ? '#/my' : '#/orders');
  $$('.nav a').forEach(a => a.classList.toggle('on', h === a.getAttribute('href') || h.startsWith(a.getAttribute('href') + '/')));
  const [, page, arg] = h.split('/');
  const view = $('#view');
  const views = driver
    ? { my: arg ? driverDeliver : driverJobs, 'my-done': driverDone }
    : { orders: officeOrders, new: officeNew, customers: arg ? customerDetail : customerList, products: productList, reconcile: reconcile, settings: settings, order: orderDetail };
  const fn = views[page] || (driver ? driverJobs : officeOrders);
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
// 電腦版：訂單看板
// ---------------------------------------------------------------------
function orderCard(o, drivers) {
  const pending = o._pending ? ' pending' : '';
  const paid = o.status === 'delivered' && o.billing_type === 'cash'
    ? (o.paid_amount === o.total_amount ? '<span class="pill ok">已收齊</span>' : `<span class="pill bad">收 ${money(o.paid_amount)}</span>`) : '';
  let actions = '';
  if (o.status === 'new' || o.status === 'dispatched') {
    actions = drivers.map(d => `<button data-dispatch="${o.id}" data-driver="${d.id}" ${o.driver_id === d.id ? 'class="primary"' : ''}>${esc(d.name)}</button>`).join('')
      + (o.status === 'dispatched' ? `<button class="ghost" data-line="${o.id}">傳 LINE</button>` : '')
      + `<button class="ghost" data-open="${o.id}">詳情</button>`;
  } else {
    actions = `<button class="ghost" data-open="${o.id}">詳情</button>`;
  }
  return `<div class="order ${o.status}${pending}">
    <div class="row between"><span class="title">${esc(o.customer_name)}</span><span class="money">${money(o.total_amount)}</span></div>
    <div class="addr">${esc(o.address)}${o.floor ? '・' + esc(o.floor) : ''}${o.has_elevator === 0 ? '・無電梯' : ''}</div>
    <div class="row"><span class="grow">${esc(o.summary || '')}</span>
      ${o.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : ''}${paid}
      <span class="muted">${o.status === 'delivered' ? '送達 ' + hhmm(o.delivered_at) : hhmm(o.ordered_at)}${o.driver_name && o.status !== 'new' ? '・' + esc(o.driver_name) : ''}</span></div>
    ${o.note ? `<div class="muted">📝 ${esc(o.note)}</div>` : ''}
    <div class="actions">${actions}</div></div>`;
}

async function officeOrders(view) {
  const [orders, drivers] = await Promise.all([data('/api/orders?limit=300'), data('/api/drivers')]);
  const t = today();
  const cols = [
    ['new', '新單', orders.filter(o => o.status === 'new')],
    ['dispatched', '已派單（送貨中）', orders.filter(o => o.status === 'dispatched')],
    ['delivered', '今天已送達', orders.filter(o => o.status === 'delivered' && (o.delivered_at || '').startsWith(t))],
  ];
  view.innerHTML = `<div class="row between"><h2>訂單</h2><a class="btn primary" href="#/new">＋ 記單（F2）</a></div>
    ${drivers.length ? '' : '<div class="card">還沒有師傅帳號，請到「設定」新增師傅，才能派單。</div>'}
    <div class="board">${cols.map(([k, title, list]) => `<section class="col"><h3>${title}<span class="pill">${list.length}</span></h3>
      ${list.map(o => orderCard(o, drivers)).join('') || '<div class="muted">沒有</div>'}</section>`).join('')}</div>`;
  view.onclick = e => {
    const b = e.target.closest('button'); if (!b) return;
    if (b.dataset.open) location.hash = '#/order/' + b.dataset.open;
    if (b.dataset.line) { const o = orders.find(x => x.id === +b.dataset.line); if (o) sendLine(o); }
    if (b.dataset.dispatch) {
      const id = +b.dataset.dispatch, driver = +b.dataset.driver;
      const d = drivers.find(x => x.id === driver);
      patch('/api/orders?limit=300', list => list.map(o => o.id === id ? { ...o, status: 'dispatched', driver_id: driver, driver_name: d.name, _pending: true } : o));
      queue('POST', `/api/orders/${id}/dispatch`, { driver_id: driver }, '派單');
      const o = orders.find(x => x.id === id);
      toast(`已派給 ${d.name}`, 'ok', o && lineAction({ ...o, driver_id: driver, driver_name: d.name }));
      route(true);
    }
  };
}

// 訂單詳情
async function orderDetail(view, id) {
  S.live = false;
  const [o, drivers] = await Promise.all([api('GET', '/api/orders/' + id), data('/api/drivers')]);
  const editable = o.status === 'new' || o.status === 'dispatched';
  view.innerHTML = `<p><a href="#/orders">← 回訂單</a></p>
    <div class="card"><div class="row between"><h2 style="margin:0">${esc(o.order_no)}　${esc(o.customer_name)}</h2>
      <span class="pill ${o.status === 'delivered' ? 'ok' : o.status === 'cancelled' ? 'bad' : 'accent'}">${STATUS[o.status]}</span></div>
      <p>${esc(o.address)}　${esc(o.phone || '')}　${o.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : '現結'}
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
// 電腦版：記單
// ---------------------------------------------------------------------
async function officeNew(view) {
  S.live = false;
  const [products, drivers] = await Promise.all([data('/api/products'), data('/api/drivers')]);
  const st = { customer: null, qty: {}, results: [] };
  const sellable = products.filter(p => p.category !== 'empty_cylinder');
  view.innerHTML = `<h2>記單</h2><div class="new-grid"><div>
    <section class="card callstack" id="calls" hidden></section>
    <section class="card"><label style="margin-top:0">找客戶（電話、名字、地址）</label>
      <input id="q" placeholder="輸入後按 Enter 選第一個" autocomplete="off">
      <div class="results" id="results"></div>
      <p><button class="ghost" id="newCust">＋ 新客戶</button></p></section></div>
    <section class="card" id="right"><div class="muted">先在左邊選客戶</div></section></div>`;
  const q = $('#q');
  q.focus();
  let timer;
  const search = async () => {
    const v = q.value.trim();
    st.results = v ? await api('GET', '/api/customers?limit=30&q=' + encodeURIComponent(v)).catch(() => {
      const all = store.get('c:/api/customers?limit=1000', []);
      return all.filter(c => [c.name, c.phone, c.other_phones, c.address].some(x => (x || '').includes(v))).slice(0, 30);
    }) : [];
    renderResults(v);
  };
  const renderResults = (v = q.value.trim()) => {
    $('#results').innerHTML = st.results.map((c, i) => `<div class="result" data-i="${i}"><b>${esc(c.name)}</b> <span class="muted">${esc(c.phone || '')}</span>
      <div class="muted">${esc(c.address)}${c.billing_type === 'monthly' ? '・月結' : ''}${c.balance > 0 ? '・欠 ' + money(c.balance) : ''}</div></div>`).join('')
      || (v ? '<div class="muted">找不到，按「＋ 新客戶」</div>' : '');
  };
  q.oninput = () => { clearTimeout(timer); timer = setTimeout(search, 150); };
  q.onkeydown = e => { if (e.key === 'Enter' && st.results[0]) pick(st.results[0]); };
  $('#results').onclick = e => { const r = e.target.closest('.result'); if (r) pick(st.results[+r.dataset.i]); };
  $('#newCust').onclick = () => customerDialog({ phone: /^\d+$/.test(q.value.trim()) ? q.value.trim() : '', name: /^\d+$/.test(q.value.trim()) ? '' : q.value.trim() }, c => pick(c));

  // 來電排隊
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
    if (c) { q.value = fmtPhone(k.phone); st.results = k.matches; renderResults(); pick(c); }
    else { drawCalls(); customerDialog({ phone: k.phone }, nc => { st.results = [nc]; renderResults(); pick(nc); }); }
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
    },
  };
  window.addEventListener('hashchange', () => { S.newPage = null; }, { once: true });
  drawCalls();
  const first = nextCall(); if (first && Date.now() - (first.ts || 0) < 10 * 60000) takeCall(first);

  function pick(c) {
    st.customer = c; st.qty = {};
    drawCalls();
    $$('.result').forEach(r => r.classList.toggle('on', st.results[+r.dataset.i] === c));
    renderRight();
  }
  function lineTotal() {
    return Object.entries(st.qty).reduce((s, [pid, n]) => {
      const p = products.find(x => x.id === +pid); const sp = (st.customer.prices || []).find(x => x.product_id === +pid);
      return s + n * (sp ? sp.price : p.sale_price);
    }, 0);
  }
  async function renderRight() {
    const c = st.customer;
    const full = await api('GET', '/api/customers/' + c.id).catch(() => c);
    st.customer = { ...c, ...full };
    const def = drivers.find(d => d.id === st.customer.default_driver_id);
    const gas = sellable.filter(p => p.category === 'gas'), others = sellable.filter(p => p.category !== 'gas');
    $('#right').innerHTML = `<div class="row between"><div><span class="big">${esc(c.name)}</span> <span class="muted">${esc(c.phone || '')}</span></div>
        <a href="#/customers/${c.id}" class="muted">客戶資料</a></div>
      <div>${esc(c.address)}${c.floor ? '・' + esc(c.floor) : ''}${c.has_elevator === 0 ? '・<b>無電梯</b>' : ''}
        ${c.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : ''}${st.customer.balance > 0 ? `<span class="pill bad">欠 ${money(st.customer.balance)}</span>` : ''}
        ${c.needs_invoice ? `<span class="pill">開發票 ${esc(c.tax_id || '')}</span>` : ''}</div>
      ${c.note ? `<div class="muted">📝 ${esc(c.note)}</div>` : ''}
      ${c.open_orders > 0 ? `<div class="call-warn">⚠ 已經有 ${c.open_orders} 張單還沒送到，可能是來催的</div>` : ''}
      ${c.last_order ? `<div class="call-last">上次 <b>${c.last_order.days_ago <= 0 ? '今天' : c.last_order.days_ago + ' 天前'}</b>：${esc(c.last_order.items || '')}・${money(c.last_order.total_amount)}${c.last_order.driver_name ? '・' + esc(c.last_order.driver_name) + ' 送' : ''}</div>` : ''}
      <h3>瓦斯</h3><div class="quick">${gas.map(qbox).join('') || '<div class="muted">還沒有瓦斯商品，請到「商品」新增</div>'}</div>
      ${others.length ? `<h3>其他商品</h3><div class="quick">${others.map(qbox).join('')}</div>` : ''}
      <label>備註（給師傅看）</label><input id="note" placeholder="例如：放後門、下午 3 點後">
      <div class="total-bar"><span>合計</span><span id="total">${money(0)}</span></div>
      <label>派給</label><div class="row" id="drivers">${drivers.map(d => `<button data-d="${d.id}" ${def && def.id === d.id ? 'class="primary"' : ''}>${esc(d.name)}</button>`).join('')}<button data-d="">先不派</button></div>
      <p><button class="primary lg" id="create">建立訂單（Enter）</button></p>`;
    st.driver = def ? def.id : null;
    const box = $('#right');
    box.onclick = e => {
      const b = e.target.closest('button'); if (!b) return;
      if (b.dataset.plus || b.dataset.minus) {
        const pid = b.dataset.plus || b.dataset.minus;
        st.qty[pid] = Math.max(0, (st.qty[pid] || 0) + (b.dataset.plus ? 1 : -1));
        if (!st.qty[pid]) delete st.qty[pid];
        syncQty();
      }
      if ('d' in b.dataset) { st.driver = b.dataset.d ? +b.dataset.d : null; $$('#drivers button').forEach(x => x.classList.toggle('primary', x === b)); }
      if (b.id === 'create') create();
    };
    box.oninput = e => { if (e.target.dataset.qty) { const n = parseInt(e.target.value) || 0; if (n > 0) st.qty[e.target.dataset.qty] = n; else delete st.qty[e.target.dataset.qty]; syncQty(true); } };
    box.onkeydown = e => { if (e.key === 'Enter' && e.target.id !== 'note') create(); if (e.key === 'Enter' && e.target.id === 'note') create(); };
    syncQty();
  }
  function qbox(p) {
    const sp = (st.customer.prices || []).find(x => x.product_id === p.id);
    return `<div class="q" data-p="${p.id}"><div><b>${esc(p.name)}</b></div><div class="muted">${money(sp ? sp.price : p.sale_price)}${sp ? '（特價）' : ''}${p.stock_qty != null ? '・庫存 ' + p.stock_qty : ''}</div>
      <div class="stepper"><button data-minus="${p.id}">−</button><input data-qty="${p.id}" inputmode="numeric" value=""><button data-plus="${p.id}">＋</button></div></div>`;
  }
  function syncQty(fromInput) {
    $$('#right .q').forEach(el => {
      const n = st.qty[el.dataset.p] || 0;
      el.classList.toggle('has', n > 0);
      if (!fromInput) $('input', el).value = n || '';
    });
    $('#total').textContent = money(lineTotal());
  }
  async function create() {
    const items = Object.entries(st.qty).map(([pid, qty]) => ({ product_id: +pid, qty }));
    if (!items.length) return toast('請選商品數量', 'bad');
    const body = { customer_id: st.customer.id, items, note: $('#note').value || null };
    if (st.driver) body.driver_id = st.driver;
    const btn = $('#create'); btn.disabled = true;
    try {
      const o = await api('POST', '/api/orders', body);
      const k = S.calls.find(x => x.id === st.callId); if (k) { k.state = 'done'; k.order_no = o.order_no; saveCalls(); }
      toast(`已建立 ${o.order_no}${o.driver_name ? '，派給 ' + o.driver_name : ''}`, 'ok', o.driver_name ? lineAction(o) : null);
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
  ['name', '名字 *'], ['phone', '電話'], ['address', '地址 *'], ['floor', '樓層'], ['area', '區域'],
  ['code', '客戶編號'], ['refill_days', '大約幾天換一次瓶', 'number'], ['tax_id', '統一編號'], ['invoice_title', '發票抬頭'],
];
function customerFormHTML(c, drivers) {
  const phoneCard = c.id ? `<button type="button" class="ghost phone-card" id="morePhones">其他電話（${(c.phones || []).length}）</button>` : '';
  return `<div class="form-grid">${CUSTOMER_FORM.map(([k, t, type]) => k === 'phone'
      ? `<div><label>${t}</label><div class="row" style="flex-wrap:nowrap"><input name="phone" value="${esc(c.phone ?? '')}">${phoneCard}</div></div>`
      : `<div><label>${t}</label><input name="${k}" type="${type || 'text'}" value="${esc(c[k] ?? '')}"></div>`).join('')}
    <div><label>結帳方式</label><select name="billing_type"><option value="cash">現結</option><option value="monthly" ${c.billing_type === 'monthly' ? 'selected' : ''}>月結</option></select></div>
    <div><label>電梯</label><select name="has_elevator"><option value="">不確定</option><option value="1" ${c.has_elevator === 1 ? 'selected' : ''}>有電梯</option><option value="0" ${c.has_elevator === 0 ? 'selected' : ''}>沒有電梯</option></select></div>
    <div><label>固定師傅</label><select name="default_driver_id"><option value="">不指定</option>${drivers.map(d => `<option value="${d.id}" ${c.default_driver_id === d.id ? 'selected' : ''}>${esc(d.name)}</option>`).join('')}</select></div>
    <div><label>&nbsp;</label><label style="color:var(--text)"><input type="checkbox" name="needs_invoice" ${c.needs_invoice ? 'checked' : ''}>要開發票</label></div></div>
    <label>備註</label><textarea name="note">${esc(c.note || '')}</textarea>`;
}
function readCustomerForm(form) {
  const f = Object.fromEntries(new FormData(form));
  f.needs_invoice = form.needs_invoice.checked;
  for (const k of ['refill_days', 'default_driver_id', 'has_elevator']) f[k] = f[k] === '' ? null : +f[k];
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
  const [c, drivers, products] = await Promise.all([api('GET', '/api/customers/' + id), data('/api/drivers'), data('/api/products')]);
  const empties = products.filter(p => p.category === 'empty_cylinder');
  view.innerHTML = `<p><a href="#/customers">← 回客戶</a></p>
    <div class="card"><div class="row between"><h2 style="margin:0">${esc(c.name)}</h2>
      <div class="row"><span>買 ${money(c.total_sales)}・付 ${money(c.total_paid)}</span><span class="pill ${c.balance > 0 ? 'bad' : 'ok'}">${c.balance > 0 ? '欠 ' + money(c.balance) : '沒有欠款'}</span>
      <button id="pay">記收款</button></div></div>
      <form id="f">${customerFormHTML(c, drivers)}<p class="row"><button class="primary">儲存</button>
        <label style="margin:0;color:var(--text)"><input type="checkbox" name="active_box" ${c.active ? '' : 'checked'}>停用這位客戶</label></p></form></div>
    <div class="card"><h3 style="margin-top:0">押瓶</h3>
      ${c.deposits.length ? c.deposits.map(d => `<div>${esc(d.cylinder_name)}：手上 ${d.cylinders_held} 支，押金 ${money(d.deposit_amount)}</div>`).join('') : '<div class="muted">沒有押瓶</div>'}
      ${empties.length ? `<div class="row" style="margin-top:8px"><select id="dp" style="width:auto">${empties.map(p => `<option value="${p.id}">${esc(p.name)}</option>`).join('')}</select>
        <input id="dq" type="number" placeholder="支數（還瓶打負數）" style="width:170px"><input id="da" type="number" placeholder="押金（退款打負數）" style="width:170px"><button id="dep">記錄押瓶</button></div>`
        : '<div class="muted">要先在「商品」新增空瓶商品（分類選空瓶）</div>'}</div>
    <div class="card"><h3 style="margin-top:0">特別價格</h3>
      ${c.prices.map(p => `<div>${esc(p.name)}：${money(p.price)} <button class="ghost" data-delprice="${p.product_id}">刪除</button></div>`).join('') || '<div class="muted">沒有，用一般售價</div>'}
      <div class="row" style="margin-top:8px"><select id="pp" style="width:auto">${products.filter(p => p.category !== 'empty_cylinder').map(p => `<option value="${p.id}">${esc(p.name)}（${money(p.sale_price)}）</option>`).join('')}</select>
      <input id="pv" type="number" placeholder="特價" style="width:120px"><button id="setprice">設定</button></div></div>
    <div class="card table-wrap"><h3 style="margin-top:0">最近訂單</h3><table>
      ${c.recent_orders.map(o => `<tr class="click" data-o="${o.id}"><td>${esc(o.order_no)}</td><td>${STATUS[o.status]}</td><td class="num">${money(o.total_amount)}</td><td>${esc(o.delivered_at || o.ordered_at)}</td></tr>`).join('') || '<tr><td class="muted">沒有</td></tr>'}</table></div>
    ${legacyCustomerHTML(c.legacy)}`;
  const reload = () => customerDetail(view, id);
  $('#morePhones').onclick = () => phonesDialog(id, reload);
  $('#f').onsubmit = async e => {
    e.preventDefault();
    const body = readCustomerForm(e.target); body.active = !e.target.active_box.checked; delete body.active_box;
    try { await api('PUT', '/api/customers/' + id, body); toast('已儲存', 'ok'); } catch (err) { toast(err.message, 'bad'); }
  };
  $('#pay').onclick = async () => {
    const v = prompt(`${c.name} 這次付多少錢？`, c.balance > 0 ? c.balance : '');
    if (!v) return;
    const method = confirm('是轉帳嗎？（確定 = 轉帳，取消 = 現金）') ? 'transfer' : 'cash';
    try { await api('POST', '/api/payments', { customer_id: +id, amount: parseInt(v), method }); toast('已記收款', 'ok'); reload(); } catch (err) { toast(err.message, 'bad'); }
  };
  const dep = $('#dep');
  if (dep) dep.onclick = async () => {
    try { await api('POST', '/api/deposits', { customer_id: +id, product_id: +$('#dp').value, qty_change: parseInt($('#dq').value) || 0, amount: parseInt($('#da').value) || 0 }); toast('已記錄', 'ok'); reload(); }
    catch (err) { toast(err.message, 'bad'); }
  };
  $('#setprice').onclick = async () => { try { await api('PUT', `/api/customers/${id}/prices`, { product_id: +$('#pp').value, price: parseInt($('#pv').value) }); reload(); } catch (err) { toast(err.message, 'bad'); } };
  view.addEventListener('click', async e => {
    const b = e.target.closest('[data-delprice]'); if (b) { await api('PUT', `/api/customers/${id}/prices`, { product_id: +b.dataset.delprice, price: null }); reload(); }
    const tr = e.target.closest('[data-o]'); if (tr) location.hash = '#/order/' + tr.dataset.o;
  });
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
  view.innerHTML = `<h2>設定</h2>${callCard}${legacyCard}
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
      <p class="row"><button id="updCheck">檢查更新</button>
      <button class="primary" id="updApply" ${sys.update && sys.update.available && sys.local ? '' : 'hidden'}>立即更新</button></p>
      ${sys.local ? '' : '<div class="muted">更新只能在店裡的電腦上按。</div>'}</div>` : `<p class="muted">版本 ${esc(sys.version)}</p>`}`;
  S.live = false;
  bindCallSettings(view);
  bindLegacySettings(view);
  if (sys.tunnel_url && window.qrcode) { const q = qrcode(0, 'M'); q.addData(sys.tunnel_url); q.make(); $('#qr').innerHTML = q.createSvgTag(5, 0); }
  if (!admin) return;
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
    <div class="segment" id="method"><button type="button" class="on" data-m="cash">現金</button><button type="button" data-m="transfer">轉帳</button></div>
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
      <div class="row between"><span class="name">${esc(o.customer_name)}</span><span class="big">${o.billing_type === 'monthly' ? '<span class="pill blue">月結</span>' : money(o.total_amount)}</span></div>
      <div class="addr">${esc(o.address)}${o.floor ? '・' + esc(o.floor) : ''}${o.has_elevator === 0 ? '・<b>無電梯</b>' : ''}</div>
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
    <div class="big" style="margin-top:6px">應收 ${o.billing_type === 'monthly' ? '0（月結）' : money(o.total_amount)}</div></div>
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
document.addEventListener('keydown', e => { if (e.key === 'F2' && S.me && S.me.role !== 'driver') { e.preventDefault(); location.hash = '#/new'; } });

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

// 讓網頁可以加到主畫面、斷網時也能打開，並接收派單通知。
const CACHE = 'gas-v6';
const SHELL = ['/', '/app.js', '/style.css', '/favicon.png', '/icon-192.png', '/manifest.webmanifest'];

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(SHELL)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});
// 網頁檔：先拿網路上最新的，斷網就用快取；API 一律走網路
self.addEventListener('fetch', e => {
  const url = new URL(e.request.url);
  if (e.request.method !== 'GET' || url.origin !== location.origin || url.pathname.startsWith('/api/')) return;
  e.respondWith(
    fetch(e.request).then(r => {
      const copy = r.clone();
      caches.open(CACHE).then(c => c.put(e.request, copy));
      return r;
    }).catch(() => caches.match(e.request).then(r => r || caches.match('/')))
  );
});
self.addEventListener('push', e => {
  let d = {};
  try { d = e.data.json(); } catch { d = { title: '新派單', body: e.data ? e.data.text() : '' }; }
  const job = /派單/.test(d.title || '新派單');
  e.waitUntil(Promise.all([
    self.registration.showNotification(d.title || '新派單', {
      body: d.body || '', icon: '/icon-192.png', badge: '/icon-192.png', tag: d.url || 'gas', renotify: true,
      requireInteraction: job, // 派單通知一直留在螢幕上，等師傅點掉
      vibrate: [400, 150, 400, 150, 400], data: { url: d.url || '/#/my' },
    }),
    // 回報店裡「這支手機有收到」，電腦上的通知燈號才會亮綠燈
    self.registration.pushManager.getSubscription().then(sub => sub && fetch('/api/push/ack', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ endpoint: sub.endpoint }),
    })).catch(() => { }),
  ]));
});
self.addEventListener('notificationclick', e => {
  e.notification.close();
  const url = e.notification.data && e.notification.data.url || '/#/my';
  // 點開派單通知 = 師傅看到了
  const m = /#\/my\/(\d+)/.exec(url);
  if (m) fetch('/api/orders/seen', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ids: [+m[1]] }) }).catch(() => { });
  e.waitUntil(clients.matchAll({ type: 'window', includeUncontrolled: true }).then(list => {
    for (const c of list) { if ('focus' in c) { c.navigate(url); return c.focus(); } }
    return clients.openWindow(url);
  }));
});

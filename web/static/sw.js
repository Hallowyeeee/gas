// 讓網頁可以加到主畫面、斷網時也能打開，並接收派單通知。
const CACHE = 'gas-v2';
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
  e.waitUntil(self.registration.showNotification(d.title || '新派單', {
    body: d.body || '', icon: '/icon-192.png', badge: '/icon-192.png', tag: d.url || 'gas', renotify: true,
    vibrate: [300, 100, 300], data: { url: d.url || '/#/my' },
  }));
});
self.addEventListener('notificationclick', e => {
  e.notification.close();
  const url = e.notification.data && e.notification.data.url || '/#/my';
  e.waitUntil(clients.matchAll({ type: 'window', includeUncontrolled: true }).then(list => {
    for (const c of list) { if ('focus' in c) { c.navigate(url); return c.focus(); } }
    return clients.openWindow(url);
  }));
});

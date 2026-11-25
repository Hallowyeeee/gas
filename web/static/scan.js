// 拍照辨識：掃條碼、照片讀數字。全部在手機（或電腦）上算，免費、不用網路。
// 辨識工具放在 /vendor/（內建在程式裡），第一次用到才下載，之後手機會記住。
'use strict';
const Scan = (() => {
  const TESS = '/vendor/tesseract-5.1.1/';
  const ZX = '/vendor/zxing-2.2.4/';
  const FORMATS = ['Code128', 'Code39', 'Code93', 'EAN-13', 'EAN-8', 'ITF', 'Codabar', 'QRCode', 'DataMatrix'];
  const scripts = {};
  const loadScript = src => scripts[src] || (scripts[src] = new Promise((ok, no) => {
    const s = document.createElement('script'); s.src = src; s.onload = ok;
    s.onerror = () => { delete scripts[src]; no(new Error('辨識工具載入失敗，請確認連得到店裡電腦')); };
    document.head.appendChild(s);
  }));
  const h = (s, map) => String(s ?? '').replace(/[&<>"']/g, c => map[c]);
  const esc = s => h(s, { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' });

  // ---------------- 條碼 ----------------
  let zxReady;
  function zxing() {
    return zxReady || (zxReady = loadScript(ZX + 'zxing-reader.js').then(() => {
      ZXingWASM.prepareZXingModule({ overrides: { locateFile: (p, pre) => p.endsWith('.wasm') ? ZX + 'zxing_reader.wasm' : pre + p }, fireImmediately: true });
      return ZXingWASM;
    }).catch(e => { zxReady = null; throw e; }));
  }
  let native;
  async function nativeDetector() {
    if (native !== undefined) return native;
    native = null;
    try {
      if ('BarcodeDetector' in window) {
        const ok = await BarcodeDetector.getSupportedFormats();
        const want = ['code_128', 'code_39', 'code_93', 'ean_13', 'ean_8', 'itf', 'codabar', 'qr_code', 'data_matrix'].filter(f => ok.includes(f));
        if (want.includes('code_128')) native = new BarcodeDetector({ formats: want });
      }
    } catch { native = null; }
    return native;
  }
  // 一張圖（canvas）找條碼：手機內建的先試，不行再用 zxing
  async function decodeCanvas(cv, harder) {
    const nd = await nativeDetector();
    if (nd) { try { const r = await nd.detect(cv); if (r.length) return r[0].rawValue; } catch { } }
    const zx = await zxing();
    const img = cv.getContext('2d').getImageData(0, 0, cv.width, cv.height);
    const r = await zx.readBarcodes(img, { formats: FORMATS, tryHarder: true, tryRotate: !!harder, tryInvert: !!harder, tryDownscale: true, maxNumberOfSymbols: 1 });
    const v = r.find(x => x.isValid && x.text);
    return v ? v.text : null;
  }
  function toCanvas(src, maxSide, rect, angle) {
    const sw = rect ? rect.w : (src.videoWidth || src.width), sh = rect ? rect.h : (src.videoHeight || src.height);
    const s = Math.min(maxSide / Math.max(sw, sh), 4);
    const w = Math.round(sw * s), hh = Math.round(sh * s);
    const cv = document.createElement('canvas'); const g = cv.getContext('2d', { willReadFrequently: true });
    if (angle) {
      const rad = angle * Math.PI / 180, c = Math.abs(Math.cos(rad)), si = Math.abs(Math.sin(rad));
      cv.width = Math.round(w * c + hh * si); cv.height = Math.round(w * si + hh * c);
      g.fillStyle = '#fff'; g.fillRect(0, 0, cv.width, cv.height);
      g.translate(cv.width / 2, cv.height / 2); g.rotate(rad); g.translate(-w / 2, -hh / 2);
    } else { cv.width = w; cv.height = hh; }
    g.drawImage(src, rect ? rect.x : 0, rect ? rect.y : 0, sw, sh, 0, 0, w, hh);
    return cv;
  }
  // 照片找條碼：整張、放大中間、轉幾個角度都試（照片斜斜的也讀得到）
  async function decodeImage(img) {
    const W = img.width, H = img.height;
    const tries = [[null, 1600, 0]];
    for (const a of [0, -8, 8, -15, 15]) tries.push([{ x: W * 0.1, y: H * 0.1, w: W * 0.8, h: H * 0.8 }, 1800, a]);
    tries.push([null, 2400, 0]);
    // 條碼常常只佔一小塊：切成九宮格放大找
    for (let i = 0; i < 3; i++) for (let j = 0; j < 3; j++) tries.push([{ x: W * j / 4, y: H * i / 4, w: W / 2, h: H / 2 }, 1600, 0]);
    for (const [rect, size, angle] of tries) {
      const v = await decodeCanvas(toCanvas(img, size, rect, angle), true);
      if (v) return v;
    }
    return null;
  }

  // 開一個蓋在最上面的視窗（店裡電腦的記度數視窗也是 dialog，要用 dialog 才蓋得過去）
  function overlay(html, cls) {
    const d = document.createElement('dialog');
    d.className = 'scan-dlg ' + (cls || '');
    d.innerHTML = html;
    document.body.appendChild(d);
    d.addEventListener('close', () => setTimeout(() => d.remove(), 0));
    d.showModal();
    return d;
  }
  const pickFile = () => new Promise(ok => {
    const i = document.createElement('input'); i.type = 'file'; i.accept = 'image/*'; i.capture = 'environment';
    i.onchange = () => ok(i.files[0] || null); i.click();
  });
  async function fileToImage(file) {
    try { return await createImageBitmap(file, { imageOrientation: 'from-image' }); } catch { return await createImageBitmap(file); }
  }

  const camOK = () => !!(window.isSecureContext && navigator.mediaDevices && navigator.mediaDevices.getUserMedia);
  // 掃條碼：可以用鏡頭的就一直掃，不行就拍照。回傳號碼；按「手打」或關掉回傳 null
  function barcode(title) {
    return new Promise(resolve => {
      let done = false, stream = null, timer = null;
      const d = overlay(`<div class="scan-head"><b>${esc(title || '掃條碼')}</b><button type="button" class="ghost" data-x>✕</button></div>
        <div class="scan-cam" ${camOK() ? '' : 'hidden'}><video playsinline muted></video><div class="scan-aim"></div></div>
        <p class="scan-msg muted">${camOK() ? '把條碼放進框框裡，靠近一點、對清楚，讀到會自動跳。' : '按「拍照」拍條碼（店裡網路的網址沒辦法直接開鏡頭）。'}</p>
        <div class="row scan-btns"><button type="button" data-photo>📷 拍照</button><button type="button" class="ghost" data-type>⌨️ 手打</button></div>`);
      const msg = d.querySelector('.scan-msg');
      const finish = v => {
        if (done) return; done = true; clearTimeout(timer);
        if (stream) stream.getTracks().forEach(t => t.stop());
        if (d.open) d.close();
        if (v && navigator.vibrate) navigator.vibrate(120);
        resolve(v || null);
      };
      d.addEventListener('close', () => finish(null));
      d.querySelector('[data-x]').onclick = () => finish(null);
      d.querySelector('[data-type]').onclick = () => finish(null);
      d.querySelector('[data-photo]').onclick = async () => {
        const f = await pickFile(); if (!f) return;
        msg.textContent = '讀取中…';
        try {
          const v = await decodeImage(await fileToImage(f));
          if (v) return finish(v);
          msg.innerHTML = '<span class="price-none">這張讀不到條碼。</span>靠近一點、別反光、對焦清楚再拍一次，或按「手打」。';
        } catch (e) { msg.textContent = e.message; }
      };
      if (!camOK()) return;
      const video = d.querySelector('video');
      (async () => {
        try {
          stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment', width: { ideal: 1920 }, height: { ideal: 1080 } }, audio: false });
          if (done) return stream.getTracks().forEach(t => t.stop());
          video.srcObject = stream; await video.play();
          zxing().catch(() => { });
          let n = 0;
          const tick = async () => {
            if (done) return;
            try {
              const vw = video.videoWidth, vh = video.videoHeight;
              if (vw) {
                // 框框那一塊（畫面中間 80% 寬、40% 高）；每 3 次換整個畫面試一次
                const rect = n++ % 3 === 2 ? null : { x: vw * 0.1, y: vh * 0.3, w: vw * 0.8, h: vh * 0.4 };
                const v = await decodeCanvas(toCanvas(video, 1280, rect, 0), false);
                if (v) return finish(v);
              }
            } catch (e) { msg.textContent = e.message; }
            timer = setTimeout(tick, 120);
          };
          tick();
        } catch {
          d.querySelector('.scan-cam').hidden = true;
          msg.textContent = '鏡頭打不開（可能沒有允許使用相機），請按「拍照」。';
        }
      })();
    });
  }

  // ---------------- 照片讀數字 ----------------
  let worker;
  function tesseract() {
    return worker || (worker = loadScript(TESS + 'tesseract.min.js').then(() => Tesseract.createWorker('eng', 1, {
      workerPath: TESS + 'worker.min.js', corePath: TESS, langPath: TESS.slice(0, -1), gzip: true, workerBlobURL: false,
    })).catch(e => { worker = null; throw e; }));
  }
  // 灰階、拉開對比；字是淺色（例如藍底白字）就反過來，變成白底黑字。
  // bin = 再切成純黑白；四周留白邊（字貼邊的話辨識會亂掉）
  function prepare(img, rect, targetH, bin, flip, useMax) {
    const sw = rect ? rect.w : img.width, sh = rect ? rect.h : img.height;
    const s = rect ? Math.min(targetH / sh, 6) : Math.min(1600 / sw, 2);
    const w = Math.max(1, Math.round(sw * s)), hh = Math.max(1, Math.round(sh * s));
    const pad = rect ? Math.round(hh * 0.3) : 0;
    const cv = document.createElement('canvas'); cv.width = w + pad * 2; cv.height = hh + pad * 2;
    const g = cv.getContext('2d', { willReadFrequently: true });
    g.drawImage(img, rect ? rect.x : 0, rect ? rect.y : 0, sw, sh, pad, pad, w, hh);
    const id = g.getImageData(pad, pad, w, hh), p = id.data, n = p.length / 4;
    const v = new Uint8Array(n); const hist = new Uint32Array(256);
    for (let i = 0; i < n; i++) { const k = useMax ? Math.max(p[i * 4], p[i * 4 + 1], p[i * 4 + 2]) : Math.min(p[i * 4], p[i * 4 + 1], p[i * 4 + 2]); v[i] = k; hist[k]++; }
    let lo = 0, hi = 255, acc = 0;
    while (lo < 255 && (acc += hist[lo]) < n * 0.02) lo++;
    acc = 0; while (hi > 0 && (acc += hist[hi]) < n * 0.02) hi--;
    const range = Math.max(1, hi - lo);
    const h2 = new Uint32Array(256); let sum = 0;
    for (let i = 0; i < n; i++) { const k = Math.round(Math.max(0, Math.min(255, (v[i] - lo) * 255 / range))); v[i] = k; h2[k]++; sum += k; }
    // 大津法找黑白分界
    let t = 128;
    if (bin) { let sB = 0, wB = 0, best = -1, tot = 0; for (let i = 0; i < 256; i++) tot += i * h2[i];
      for (let i = 0; i < 256; i++) { wB += h2[i]; if (!wB) continue; const wF = n - wB; if (!wF) break; sB += i * h2[i];
        const mB = sB / wB, mF = (tot - sB) / wF, b = wB * wF * (mB - mF) * (mB - mF); if (b > best) { best = b; t = i; } } }
    const inv = (sum / n < 128) !== !!flip;
    for (let i = 0; i < n; i++) { let k = inv ? 255 - v[i] : v[i]; if (bin) k = (inv ? 255 - v[i] : v[i]) > (inv ? 255 - t : t) ? 255 : 0; p[i * 4] = p[i * 4 + 1] = p[i * 4 + 2] = k; }
    g.fillStyle = '#fff'; g.fillRect(0, 0, cv.width, cv.height);
    g.putImageData(id, pad, pad);
    return cv;
  }
  // rect 有給 = 一行字（框選的那一塊）；沒給 = 整張照片找數字
  async function readDigits(img, rect) {
    const w = await tesseract();
    await w.setParameters({ tessedit_pageseg_mode: rect ? '7' : '11', tessedit_char_whitelist: rect ? '0123456789.' : '0123456789' });
    let best = null; const votes = {};
    // 框選的：灰階、黑白、幾種大小都試，讀到最多次的那個答案勝出（一樣多就看把握度）
    // 第 4 個值 = 用最亮的顏色（錶上紅色的小數位才不會跟黑底混在一起）
    const variants = rect ? [[40, false], [40, true], [60, false], [60, true], [90, false], [90, true], [60, true, false, true], [90, true, false, true]] : [[0, false], [0, true], [0, true, true]];
    for (const [th, bin, flip, useMax] of variants) {
      const r = await w.recognize(prepare(img, rect, th, bin, flip, useMax));
      const text = (r.data.text || '').trim();
      const runs = text.match(/\d+(?:\.\d+)?/g) || [];
      if (rect) {
        const k = runs.join(''); if (!k) continue;
        const v = votes[k] || (votes[k] = { text, runs, n: 0, confidence: 0 });
        v.n++; v.confidence = Math.max(v.confidence, r.data.confidence);
      } else {
        // 整張照片：一個字塊一個字塊拿（不要把整行黏在一起）
        const words = (r.data.words || []).map(x => (x.text || '').replace(/\D/g, '')).filter(Boolean);
        if (!best) best = { text, runs: [], confidence: r.data.confidence };
        best.runs.push(...(words.length ? words : runs));
      }
    }
    if (rect) best = Object.values(votes).sort((a, b) => b.n - a.n || b.confidence - a.confidence)[0] || null;
    return best || { text: '', runs: [], confidence: 0 };
  }
  // 用手指（或滑鼠）框住要讀的地方，回傳照片上的範圍
  function pickRect(img, title) {
    return new Promise(resolve => {
      const d = overlay(`<div class="scan-head"><b>${esc(title || '用手指框住數字')}</b><button type="button" class="ghost" data-x>✕</button></div>
        <div class="crop-wrap"><canvas></canvas><div class="crop-box" hidden></div></div>
        <p class="muted">用手指從數字左上角拉到右下角，框緊一點比較準。</p>
        <div class="row"><button type="button" class="primary" data-ok disabled>讀這一塊</button><button type="button" class="ghost" data-x>取消</button></div>`);
      const cv = d.querySelector('canvas'), box = d.querySelector('.crop-box'), wrap = d.querySelector('.crop-wrap');
      const maxW = Math.min(window.innerWidth - 40, 900), maxH = window.innerHeight * 0.6;
      const s = Math.min(maxW / img.width, maxH / img.height, 1);
      cv.width = Math.round(img.width * s); cv.height = Math.round(img.height * s);
      cv.getContext('2d').drawImage(img, 0, 0, cv.width, cv.height);
      let start = null, sel = null;
      const pos = e => { const b = cv.getBoundingClientRect(); return { x: Math.max(0, Math.min(cv.width, (e.clientX - b.left) * cv.width / b.width)), y: Math.max(0, Math.min(cv.height, (e.clientY - b.top) * cv.height / b.height)) }; };
      const draw = () => { if (!sel) return; const b = cv.getBoundingClientRect(), k = b.width / cv.width;
        Object.assign(box.style, { left: cv.offsetLeft + sel.x * k + 'px', top: cv.offsetTop + sel.y * k + 'px', width: sel.w * k + 'px', height: sel.h * k + 'px' }); box.hidden = false; };
      cv.addEventListener('pointerdown', e => { e.preventDefault(); cv.setPointerCapture(e.pointerId); start = pos(e); sel = null; box.hidden = true; });
      cv.addEventListener('pointermove', e => { if (!start) return; const p = pos(e);
        sel = { x: Math.min(start.x, p.x), y: Math.min(start.y, p.y), w: Math.abs(p.x - start.x), h: Math.abs(p.y - start.y) }; draw(); });
      cv.addEventListener('pointerup', () => { start = null; d.querySelector('[data-ok]').disabled = !(sel && sel.w > 8 && sel.h > 5); });
      wrap.style.touchAction = 'none';
      let res = null;
      d.querySelectorAll('[data-x]').forEach(b => b.onclick = () => d.close());
      d.querySelector('[data-ok]').onclick = () => { res = { x: sel.x / s, y: sel.y / s, w: sel.w / s, h: sel.h / s }; d.close(); };
      d.addEventListener('close', () => resolve(res));
    });
  }
  // 從照片框一塊讀數字（框選→辨識），回傳讀到的數字字串，沒讀到回傳 ''
  async function pickAndRead(img, title) {
    const rect = await pickRect(img, title);
    if (!rect) return null;
    const r = await readDigits(img, rect);
    return r.runs.join('');
  }

  // 錶上讀到的數字串 → 可能的度數（最後一兩位可能是紅色小數）
  function meterCandidates(runs) {
    const out = [];
    for (const r of runs) {
      if (r.includes('.')) { const v = parseFloat(r); if (!isNaN(v)) out.push(v); continue; }
      if (r.length < 3 || r.length > 9) continue;
      const n = parseInt(r, 10);
      out.push(n, n / 10, n / 100);
      // 錶的數字窗常常前後多讀一位：去掉頭或尾再試
      if (r.length > 4) { const a = r.slice(1), b = r.slice(0, -1); out.push(parseInt(a, 10), parseInt(a, 10) / 10, parseInt(b, 10), parseInt(b, 10) / 10); }
    }
    return out.filter(v => isFinite(v));
  }
  // 從候選裡挑最合理的：不能比上次小、用量不能大得離譜，挑用量最接近平常的
  function pickReading(runs, last, usual) {
    const c = meterCandidates(runs);
    // 第一次記（沒有上次度數）沒辦法判斷合不合理：只接受框選的那一個數字
    if (last == null) return runs.length === 1 && /^\d{1,8}(\.\d+)?$/.test(runs[0]) ? parseFloat(runs[0]) : null;
    const max = Math.max((usual || 0) * 5, 500);
    const ok = c.filter(v => v >= last && v - last <= max);
    if (!ok.length) return null;
    const target = usual || 0;
    ok.sort((a, b) => Math.abs(a - last - target) - Math.abs(b - last - target));
    return Math.round(ok[0] * 100) / 100;
  }

  // 付費辨識的說明（ⓘ）
  function paidInfo() {
    const d = overlay(`<div class="scan-head"><b>ⓘ 付費的辨識（目前沒有用）</b><button type="button" class="ghost" data-x>✕</button></div>
      <p>現在用的是<b>免費</b>的辨識：在手機上算、不用網路，但錶面髒、反光、斜拍容易認錯，所以一定要對一下再存。</p>
      <p>如果以後覺得不夠準，可以改用付費的，準很多：</p>
      <div class="table-wrap"><table>
        <tr><th></th><th>一張大約</th><th>每月 300 張</th><th>說明</th></tr>
        <tr><td>Claude Haiku 5.5</td><td class="num">NT$0.01</td><td class="num">約 NT$2</td><td>準，看得懂照片在拍什麼，直接回答度數或鋼瓶號碼</td></tr>
        <tr><td>Claude Sonnet 5.5</td><td class="num">NT$0.1</td><td class="num">約 NT$35</td><td>最準，鋼印、反光也比較認得出</td></tr>
        <tr><td>Google 辨識</td><td class="num">前 1000 張免費<br>之後約 NT$0.05</td><td class="num">免費</td><td>只把照片裡所有字列出來，要自己挑哪串是度數</td></tr>
      </table></div>
      <p class="muted">付費的要有網路（一張約 1～3 秒）；每家店要自己申請帳號、儲值（Claude 最低 5 美元，約 NT$160），在設定貼上金鑰。費用是 2026 年 11 月的估計，實際依照片大小會差一點。想改用的話跟寫程式的人說一聲。</p>
      <p><button type="button" class="primary" data-x>知道了</button></p>`);
    d.querySelectorAll('[data-x]').forEach(b => b.onclick = () => d.close());
  }

  // 民國日期 ↔ 西元
  const roc = y => y - 1911;
  return { barcode, decodeImage, readDigits, pickRect, pickAndRead, pickReading, meterCandidates, fileToImage, paidInfo, camOK, roc, prepare };
})();

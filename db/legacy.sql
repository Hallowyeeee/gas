-- =====================================================================
-- 第 3 版：從舊系統（捷程 easy88 瓦斯行 3.0）抓過來的資料
-- 舊送貨紀錄跟正式訂單分開放：不扣庫存、不算進師傅對帳。
-- =====================================================================

-- 舊系統的客戶編號（例如 86000），用來認出同一位客人
-- （customers.legacy_id 欄位由程式先加上，見 app.go migrate）
CREATE UNIQUE INDEX IF NOT EXISTS idx_customers_legacy ON customers(legacy_id);

-- 每按一次 F12 抓到的一頁（確認前是 pending）
CREATE TABLE IF NOT EXISTS legacy_captures (
  id          INTEGER PRIMARY KEY,
  legacy_id   TEXT,
  name        TEXT,
  phone       TEXT,
  source_url  TEXT,
  parsed      TEXT    NOT NULL,                 -- 讀出來的資料（JSON）
  raw_gz      BLOB,                             -- 原始網頁（壓縮），萬一讀錯可以回頭查
  status      TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','saved','cancelled')),
  customer_id INTEGER REFERENCES customers(id),
  captured_at TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  done_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_legacy_captures_status ON legacy_captures(status, id);

-- 舊系統上的其他客戶資訊（瓦斯桶別、各規格售價、累計購買）
CREATE TABLE IF NOT EXISTS legacy_customer_info (
  customer_id   INTEGER PRIMARY KEY REFERENCES customers(id),
  legacy_id     TEXT,
  cylinder_type TEXT,                           -- 瓦斯桶別
  prices        TEXT,                           -- {"20kg":770,...}
  totals        TEXT,                           -- 累計購買 {"20kg":38,...}
  captured_at   TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

-- 桶子結餘：正數 = 客人借走店裡的桶（欠我們）；負數 = 客人寄放空桶在店裡
CREATE TABLE IF NOT EXISTS legacy_cylinder_balance (
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  size        TEXT    NOT NULL,                 -- 20kg、16kg、50丙…
  qty         INTEGER NOT NULL,
  updated_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  PRIMARY KEY (customer_id, size)
);

-- 舊送貨紀錄
CREATE TABLE IF NOT EXISTS legacy_deliveries (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  delivered_at TEXT   NOT NULL,                 -- 送貨日期＋叫貨時間 2026-10-21 15:29:31
  items       TEXT,                             -- 給人看的：20kg×1(770)、16kg×1(660)
  stored_gas  INTEGER NOT NULL DEFAULT 0,       -- 存氣
  amount      INTEGER NOT NULL DEFAULT 0,       -- 金額
  paid        INTEGER,                          -- 已付
  cylinder    TEXT,                             -- 欠桶 / 寄桶 / 還桶
  pay_status  TEXT,                             -- 已付款…
  driver_name TEXT,
  position    TEXT,
  note        TEXT,
  UNIQUE (customer_id, delivered_at)
);
CREATE INDEX IF NOT EXISTS idx_legacy_deliveries_customer ON legacy_deliveries(customer_id, delivered_at);

CREATE TABLE IF NOT EXISTS legacy_delivery_items (
  delivery_id INTEGER NOT NULL REFERENCES legacy_deliveries(id) ON DELETE CASCADE,
  size        TEXT    NOT NULL,
  qty         INTEGER NOT NULL,
  unit_price  INTEGER,
  PRIMARY KEY (delivery_id, size)
);

-- 出桶鋼瓶（舊系統「出桶鋼瓶超過 360 天」清單）
CREATE TABLE IF NOT EXISTS legacy_cylinders_out (
  customer_id     INTEGER NOT NULL REFERENCES customers(id),
  barcode         TEXT    NOT NULL,
  cylinder_no     TEXT,
  size            TEXT,
  next_inspection TEXT,                         -- 下次檢驗（轉成西元 2025-06-21）
  age_years       INTEGER,                      -- 桶齡
  driver_name     TEXT,
  out_at          TEXT,                         -- 出桶日期時間
  PRIMARY KEY (customer_id, barcode)
);

-- 換瓶提醒：舊送貨紀錄也算「上次送瓦斯」
DROP VIEW IF EXISTS v_refill_due;
CREATE VIEW v_refill_due AS
WITH last_gas AS (
  SELECT customer_id, MAX(last_delivered) AS last_delivered FROM (
    SELECT o.customer_id, MAX(o.delivered_at) AS last_delivered
    FROM orders o
    JOIN order_items oi ON oi.order_id = o.id
    JOIN products p ON p.id = oi.product_id
    WHERE o.status = 'delivered' AND p.category = 'gas'
    GROUP BY o.customer_id
    UNION ALL
    SELECT d.customer_id, MAX(d.delivered_at)
    FROM legacy_deliveries d
    WHERE EXISTS (SELECT 1 FROM legacy_delivery_items i WHERE i.delivery_id = d.id)
    GROUP BY d.customer_id
  ) GROUP BY customer_id
),
last_snooze AS (
  SELECT customer_id, MAX(snooze_to) AS snooze_to
  FROM refill_reminder_logs WHERE action = 'snoozed' GROUP BY customer_id
)
SELECT c.id AS customer_id, c.name, c.phone, c.address, c.refill_days,
       date(l.last_delivered) AS last_delivered,
       date(l.last_delivered, '+' || c.refill_days || ' days') AS due_date
FROM customers c
JOIN last_gas l ON l.customer_id = c.id
LEFT JOIN last_snooze s ON s.customer_id = c.id
WHERE c.active = 1 AND c.refill_days IS NOT NULL
  AND date(l.last_delivered, '+' || c.refill_days || ' days') <= date('now','localtime')
  AND (s.snooze_to IS NULL OR s.snooze_to <= date('now','localtime'))
  AND NOT EXISTS (SELECT 1 FROM orders o2
                  WHERE o2.customer_id = c.id AND o2.status IN ('new','dispatched'));

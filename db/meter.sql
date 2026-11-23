-- 計流量（抄錶）收費：每次開程式都會跑一次，所以全部寫成「沒有才建」。
-- 不佔資料庫版本號，避免跟其他功能撞號。

CREATE TABLE IF NOT EXISTS meter_readings (
  id           INTEGER PRIMARY KEY,
  customer_id  INTEGER NOT NULL REFERENCES customers(id),
  prev_reading REAL    NOT NULL,                  -- 上次度數
  reading      REAL    NOT NULL,                  -- 這次度數
  usage        REAL    NOT NULL,                  -- 用了幾度 = 這次 − 上次
  unit_price   INTEGER NOT NULL CHECK (unit_price >= 0), -- 每度單價（抄錶當下）
  amount       INTEGER NOT NULL,                  -- 要收的錢（四捨五入到元）
  billing_type TEXT    NOT NULL CHECK (billing_type IN ('cash','monthly')),
  photo        BLOB,                              -- 錶的照片（壓縮過的 JPEG，可以沒有）
  note         TEXT,
  read_by      INTEGER REFERENCES users(id),      -- 誰抄的
  read_at      TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  cancelled_at TEXT                               -- 抄錯作廢
);
CREATE INDEX IF NOT EXISTS idx_meter_customer ON meter_readings(customer_id, read_at);
CREATE INDEX IF NOT EXISTS idx_meter_time ON meter_readings(read_at);

-- 客戶欠款：訂單 + 抄錶金額 − 收款
DROP VIEW IF EXISTS v_customer_balance;
CREATE VIEW v_customer_balance AS
SELECT c.id AS customer_id, c.name, c.billing_type,
  COALESCE((SELECT SUM(total_amount) FROM orders o WHERE o.customer_id = c.id AND o.status = 'delivered'),0)
  + COALESCE((SELECT SUM(amount) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL),0) AS total_sales,
  COALESCE((SELECT SUM(amount) FROM payments p WHERE p.customer_id = c.id),0) AS total_paid,
  COALESCE((SELECT SUM(total_amount) FROM orders o WHERE o.customer_id = c.id AND o.status = 'delivered'),0)
  + COALESCE((SELECT SUM(amount) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL),0)
  - COALESCE((SELECT SUM(amount) FROM payments p WHERE p.customer_id = c.id),0) AS balance
FROM customers c;

-- 月結帳單：訂單 + 月結客人的抄錶
DROP VIEW IF EXISTS v_monthly_statement;
CREATE VIEW v_monthly_statement AS
SELECT customer_id, name, period, SUM(order_count) AS order_count, SUM(amount) AS amount FROM (
  SELECT o.customer_id, c.name, strftime('%Y-%m', o.delivered_at) AS period, COUNT(*) AS order_count, SUM(o.total_amount) AS amount
  FROM orders o JOIN customers c ON c.id = o.customer_id
  WHERE o.status = 'delivered' AND o.billing_type = 'monthly'
  GROUP BY o.customer_id, period
  UNION ALL
  SELECT m.customer_id, c.name, strftime('%Y-%m', m.read_at), COUNT(*), SUM(m.amount)
  FROM meter_readings m JOIN customers c ON c.id = m.customer_id
  WHERE m.cancelled_at IS NULL AND m.billing_type = 'monthly'
  GROUP BY m.customer_id, strftime('%Y-%m', m.read_at)
) GROUP BY customer_id, period;

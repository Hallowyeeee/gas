-- =====================================================================
-- 瓦斯行管理系統：資料庫結構（SQLite）
-- 金額一律用「整數元」存，不用小數，避免四捨五入對不上。
-- 時間一律存成台灣時間文字 'YYYY-MM-DD HH:MM:SS'。
-- =====================================================================
PRAGMA foreign_keys = ON;

-- ---------------------------------------------------------------------
-- 系統設定（店名、電話、預設換瓶天數……）
-- ---------------------------------------------------------------------
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- ---------------------------------------------------------------------
-- 使用者：老闆、店員、師傅
-- ---------------------------------------------------------------------
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  name          TEXT    NOT NULL,                 -- 顯示名稱，例如「阿明」
  role          TEXT    NOT NULL CHECK (role IN ('admin','staff','driver')),
  phone         TEXT,
  login_name    TEXT    NOT NULL UNIQUE,
  password_hash TEXT    NOT NULL,
  active        INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
  created_at    TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);

-- 師傅手機的推播通知訂閱
CREATE TABLE push_subscriptions (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id),
  endpoint   TEXT    NOT NULL UNIQUE,
  p256dh     TEXT    NOT NULL,
  auth       TEXT    NOT NULL,
  created_at TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);

-- ---------------------------------------------------------------------
-- 客戶
-- ---------------------------------------------------------------------
CREATE TABLE customers (
  id                INTEGER PRIMARY KEY,
  code              TEXT UNIQUE,                  -- 客戶編號（可不填）
  name              TEXT    NOT NULL,
  phone             TEXT,
  phone2            TEXT,
  address           TEXT    NOT NULL,
  area              TEXT,                         -- 區域，派單分區用
  billing_type      TEXT    NOT NULL DEFAULT 'cash'
                    CHECK (billing_type IN ('cash','monthly')),  -- 現結 / 月結
  default_driver_id INTEGER REFERENCES users(id),
  refill_days       INTEGER CHECK (refill_days IS NULL OR refill_days > 0), -- 大約幾天換一次瓶
  floor             TEXT,                         -- 樓層，例如「5F」
  has_elevator      INTEGER CHECK (has_elevator IS NULL OR has_elevator IN (0,1)), -- 有沒有電梯
  needs_invoice     INTEGER NOT NULL DEFAULT 0 CHECK (needs_invoice IN (0,1)),     -- 要不要開發票
  tax_id            TEXT CHECK (tax_id IS NULL OR (length(tax_id) = 8 AND tax_id NOT GLOB '*[^0-9]*')), -- 統一編號 8 碼
  invoice_title     TEXT,                         -- 發票抬頭
  note              TEXT,
  active            INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
  created_at        TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  updated_at        TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX idx_customers_phone ON customers(phone);

-- ---------------------------------------------------------------------
-- 商品：瓦斯、空瓶、爐具、耗材、服務（例如安裝費）
-- ---------------------------------------------------------------------
CREATE TABLE products (
  id               INTEGER PRIMARY KEY,
  sku              TEXT UNIQUE,                   -- 型號 / 料號
  name             TEXT    NOT NULL,              -- 例如「20kg 家用瓦斯」
  category         TEXT    NOT NULL
                   CHECK (category IN ('gas','empty_cylinder','stove','consumable','service')),
  unit             TEXT    NOT NULL DEFAULT '個',
  sale_price       INTEGER NOT NULL DEFAULT 0 CHECK (sale_price >= 0),
  cost_price       INTEGER NOT NULL DEFAULT 0 CHECK (cost_price >= 0), -- 最近一次進價
  track_stock      INTEGER NOT NULL DEFAULT 1 CHECK (track_stock IN (0,1)),
  empty_product_id INTEGER REFERENCES products(id), -- 瓦斯對應的空瓶商品（回收空瓶時用）
  low_stock_alert  INTEGER,                       -- 低於這個數量要提醒
  active           INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
  created_at       TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);

-- 個別客戶的特別價格（沒設就用 products.sale_price）
CREATE TABLE customer_prices (
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  product_id  INTEGER NOT NULL REFERENCES products(id),
  price       INTEGER NOT NULL CHECK (price >= 0),
  PRIMARY KEY (customer_id, product_id)
);

-- ---------------------------------------------------------------------
-- 訂單
-- 狀態：new 新單 → dispatched 已派單 → delivered 已送達；cancelled 取消
-- ---------------------------------------------------------------------
CREATE TABLE orders (
  id            INTEGER PRIMARY KEY,
  order_no      TEXT    NOT NULL UNIQUE,          -- 例如 20261106-001
  customer_id   INTEGER NOT NULL REFERENCES customers(id),
  status        TEXT    NOT NULL DEFAULT 'new'
                CHECK (status IN ('new','dispatched','delivered','cancelled')),
  billing_type  TEXT    NOT NULL CHECK (billing_type IN ('cash','monthly')), -- 下單當下現結或月結
  driver_id     INTEGER REFERENCES users(id),
  address       TEXT    NOT NULL,                 -- 送貨地址（下單當下的快照）
  phone         TEXT,
  total_amount  INTEGER NOT NULL DEFAULT 0,       -- 由明細自動加總，不要手動改
  needs_invoice INTEGER NOT NULL DEFAULT 0 CHECK (needs_invoice IN (0,1)),
  tax_id        TEXT,                             -- 開發票時的統編（下單當下的快照）
  invoice_no    TEXT,                             -- 發票號碼
  note          TEXT,
  ordered_at    TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  dispatched_at TEXT,
  delivered_at  TEXT,
  cancelled_at  TEXT,
  created_by    INTEGER REFERENCES users(id),
  CHECK (status <> 'dispatched' OR driver_id IS NOT NULL),
  CHECK (status <> 'delivered'  OR (driver_id IS NOT NULL AND delivered_at IS NOT NULL))
);
CREATE INDEX idx_orders_customer  ON orders(customer_id);
CREATE INDEX idx_orders_driver    ON orders(driver_id, status);
CREATE INDEX idx_orders_delivered ON orders(delivered_at);

CREATE TABLE order_items (
  id               INTEGER PRIMARY KEY,
  order_id         INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id       INTEGER NOT NULL REFERENCES products(id),
  qty              INTEGER NOT NULL CHECK (qty > 0),
  unit_price       INTEGER NOT NULL CHECK (unit_price >= 0),
  amount           INTEGER NOT NULL,              -- = qty × unit_price - discount
  discount         INTEGER NOT NULL DEFAULT 0 CHECK (discount >= 0),
  empties_returned INTEGER NOT NULL DEFAULT 0 CHECK (empties_returned >= 0), -- 回收空瓶數
  CHECK (amount = qty * unit_price - discount)
);
CREATE INDEX idx_order_items_order ON order_items(order_id);

-- ---------------------------------------------------------------------
-- 收款
-- 現結：師傅送達時收的錢（order_id 有值、collected_by = 師傅）
-- 月結：客戶月底一次付（order_id 可空白，只記 customer_id）
-- 退款用負數
-- ---------------------------------------------------------------------
CREATE TABLE payments (
  id           INTEGER PRIMARY KEY,
  customer_id  INTEGER NOT NULL REFERENCES customers(id),
  order_id     INTEGER REFERENCES orders(id),
  amount       INTEGER NOT NULL CHECK (amount <> 0),
  method       TEXT    NOT NULL DEFAULT 'cash'
               CHECK (method IN ('cash','transfer','check','other')),
  collected_by INTEGER REFERENCES users(id),      -- 誰收的（師傅或店裡）
  received_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  note         TEXT,
  created_by   INTEGER REFERENCES users(id)
);
CREATE INDEX idx_payments_customer  ON payments(customer_id);
CREATE INDEX idx_payments_order     ON payments(order_id);
CREATE INDEX idx_payments_collector ON payments(collected_by, received_at);

-- 師傅每天把收到的現金交回店裡
CREATE TABLE cash_handovers (
  id          INTEGER PRIMARY KEY,
  driver_id   INTEGER NOT NULL REFERENCES users(id),
  work_date   TEXT    NOT NULL,                   -- 'YYYY-MM-DD'
  amount      INTEGER NOT NULL CHECK (amount >= 0),
  received_by INTEGER REFERENCES users(id),       -- 店裡誰點收
  note        TEXT,
  created_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX idx_handovers ON cash_handovers(driver_id, work_date);

-- ---------------------------------------------------------------------
-- 進貨（瓦斯、爐具、耗材）
-- ---------------------------------------------------------------------
CREATE TABLE suppliers (
  id     INTEGER PRIMARY KEY,
  name   TEXT NOT NULL,
  phone  TEXT,
  note   TEXT,
  active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1))
);

CREATE TABLE purchases (
  id           INTEGER PRIMARY KEY,
  supplier_id  INTEGER NOT NULL REFERENCES suppliers(id),
  purchased_at TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  total_amount INTEGER NOT NULL DEFAULT 0,        -- 由明細自動加總
  invoice_no   TEXT,
  note         TEXT,
  created_by   INTEGER REFERENCES users(id)
);

CREATE TABLE purchase_items (
  id               INTEGER PRIMARY KEY,
  purchase_id      INTEGER NOT NULL REFERENCES purchases(id) ON DELETE CASCADE,
  product_id       INTEGER NOT NULL REFERENCES products(id),
  qty              INTEGER NOT NULL CHECK (qty > 0),
  unit_cost        INTEGER NOT NULL CHECK (unit_cost >= 0),
  amount           INTEGER NOT NULL,
  empties_returned INTEGER NOT NULL DEFAULT 0 CHECK (empties_returned >= 0), -- 還給供應商的空瓶
  CHECK (amount = qty * unit_cost)
);

-- 付給供應商的錢
CREATE TABLE supplier_payments (
  id          INTEGER PRIMARY KEY,
  supplier_id INTEGER NOT NULL REFERENCES suppliers(id),
  purchase_id INTEGER REFERENCES purchases(id),
  amount      INTEGER NOT NULL CHECK (amount <> 0),
  method      TEXT    NOT NULL DEFAULT 'cash'
              CHECK (method IN ('cash','transfer','check','other')),
  paid_at     TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  note        TEXT
);

-- ---------------------------------------------------------------------
-- 庫存異動：每一次進出都留一筆，庫存 = 全部加總
-- 送達、進貨會自動寫入（見下方 trigger），盤點差異用 adjust 手動寫
-- ---------------------------------------------------------------------
CREATE TABLE stock_movements (
  id         INTEGER PRIMARY KEY,
  product_id INTEGER NOT NULL REFERENCES products(id),
  qty_change INTEGER NOT NULL CHECK (qty_change <> 0),
  reason     TEXT    NOT NULL CHECK (reason IN ('sale','sale_reversal','empty_in','empty_in_reversal',
                                                'purchase','purchase_reversal','empty_out','empty_out_reversal','adjust')),
  ref_type   TEXT,                                -- 'order' / 'purchase' / NULL
  ref_id     INTEGER,
  note       TEXT,
  created_at TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX idx_stock_product ON stock_movements(product_id);

-- ---------------------------------------------------------------------
-- 押瓶費：客戶借鋼瓶收押金、還瓶退押金
-- qty_change：借出 +、歸還 -；amount：收押金 +、退押金 -
-- 押金不是營業收入，所以不放在 payments 裡
-- ---------------------------------------------------------------------
CREATE TABLE cylinder_deposits (
  id           INTEGER PRIMARY KEY,
  customer_id  INTEGER NOT NULL REFERENCES customers(id),
  product_id   INTEGER NOT NULL REFERENCES products(id), -- 哪種規格的空瓶
  qty_change   INTEGER NOT NULL,
  amount       INTEGER NOT NULL,
  method       TEXT    NOT NULL DEFAULT 'cash'
               CHECK (method IN ('cash','transfer','check','other')),
  collected_by INTEGER REFERENCES users(id),     -- 誰收／誰退（師傅或店裡）
  order_id     INTEGER REFERENCES orders(id),    -- 跟著哪張單一起處理（可空白）
  receipt_no   TEXT,                             -- 押瓶單號
  happened_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  note         TEXT,
  created_by   INTEGER REFERENCES users(id),
  CHECK (qty_change <> 0 OR amount <> 0)
);
CREATE INDEX idx_deposits_customer  ON cylinder_deposits(customer_id);
CREATE INDEX idx_deposits_collector ON cylinder_deposits(collected_by, happened_at);

-- 換瓶提醒處理紀錄（已聯絡、延後）
CREATE TABLE refill_reminder_logs (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  action      TEXT    NOT NULL CHECK (action IN ('contacted','snoozed','ordered','skip')),
  snooze_to   TEXT,                               -- 延後到哪天 'YYYY-MM-DD'
  note        TEXT,
  created_by  INTEGER REFERENCES users(id),
  created_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);

-- ---------------------------------------------------------------------
-- 金額異動紀錄：誰在什麼時候改了金額，改之前、改之後是多少
-- ---------------------------------------------------------------------
CREATE TABLE audit_log (
  id         INTEGER PRIMARY KEY,
  table_name TEXT NOT NULL,
  row_id     INTEGER NOT NULL,
  action     TEXT NOT NULL CHECK (action IN ('insert','update','delete')),
  old_data   TEXT,                                -- JSON
  new_data   TEXT,                                -- JSON
  created_at TEXT NOT NULL DEFAULT (datetime('now','localtime'))
);

-- =====================================================================
-- Triggers：讓資料庫自己維持金額和庫存正確
-- =====================================================================

-- 訂單總額 = 明細加總
CREATE TRIGGER trg_order_items_ai AFTER INSERT ON order_items BEGIN
  UPDATE orders SET total_amount = (SELECT COALESCE(SUM(amount),0) FROM order_items WHERE order_id = NEW.order_id)
  WHERE id = NEW.order_id;
END;
CREATE TRIGGER trg_order_items_au AFTER UPDATE ON order_items BEGIN
  UPDATE orders SET total_amount = (SELECT COALESCE(SUM(amount),0) FROM order_items WHERE order_id = NEW.order_id)
  WHERE id = NEW.order_id;
  UPDATE orders SET total_amount = (SELECT COALESCE(SUM(amount),0) FROM order_items WHERE order_id = OLD.order_id)
  WHERE id = OLD.order_id AND OLD.order_id <> NEW.order_id;
END;
CREATE TRIGGER trg_order_items_ad AFTER DELETE ON order_items BEGIN
  UPDATE orders SET total_amount = (SELECT COALESCE(SUM(amount),0) FROM order_items WHERE order_id = OLD.order_id)
  WHERE id = OLD.order_id;
END;

-- 已送達的訂單不能再改明細（要改請先改回未送達，庫存會自動沖回）
CREATE TRIGGER trg_order_items_lock_i BEFORE INSERT ON order_items
WHEN (SELECT status FROM orders WHERE id = NEW.order_id) = 'delivered' BEGIN
  SELECT RAISE(ABORT, '已送達的訂單不能修改明細');
END;
CREATE TRIGGER trg_order_items_lock_u BEFORE UPDATE ON order_items
WHEN (SELECT status FROM orders WHERE id = OLD.order_id) = 'delivered'
  OR (SELECT status FROM orders WHERE id = NEW.order_id) = 'delivered' BEGIN
  SELECT RAISE(ABORT, '已送達的訂單不能修改明細');
END;
CREATE TRIGGER trg_order_items_lock_d BEFORE DELETE ON order_items
WHEN (SELECT status FROM orders WHERE id = OLD.order_id) = 'delivered' BEGIN
  SELECT RAISE(ABORT, '已送達的訂單不能修改明細');
END;

-- 送達：扣商品庫存、加回收空瓶
CREATE TRIGGER trg_order_delivered AFTER UPDATE OF status ON orders
WHEN NEW.status = 'delivered' AND OLD.status <> 'delivered' BEGIN
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT oi.product_id, -oi.qty, 'sale', 'order', NEW.id
    FROM order_items oi JOIN products p ON p.id = oi.product_id
    WHERE oi.order_id = NEW.id AND p.track_stock = 1;
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT p.empty_product_id, oi.empties_returned, 'empty_in', 'order', NEW.id
    FROM order_items oi JOIN products p ON p.id = oi.product_id
    WHERE oi.order_id = NEW.id AND p.empty_product_id IS NOT NULL AND oi.empties_returned > 0;
END;

-- 從已送達改回其他狀態：沖回庫存
CREATE TRIGGER trg_order_undelivered AFTER UPDATE OF status ON orders
WHEN OLD.status = 'delivered' AND NEW.status <> 'delivered' BEGIN
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT oi.product_id, oi.qty, 'sale_reversal', 'order', NEW.id
    FROM order_items oi JOIN products p ON p.id = oi.product_id
    WHERE oi.order_id = NEW.id AND p.track_stock = 1;
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT p.empty_product_id, -oi.empties_returned, 'empty_in_reversal', 'order', NEW.id
    FROM order_items oi JOIN products p ON p.id = oi.product_id
    WHERE oi.order_id = NEW.id AND p.empty_product_id IS NOT NULL AND oi.empties_returned > 0;
END;

-- 進貨：總額加總、加庫存、扣還給供應商的空瓶
CREATE TRIGGER trg_purchase_items_ai AFTER INSERT ON purchase_items BEGIN
  UPDATE purchases SET total_amount = (SELECT COALESCE(SUM(amount),0) FROM purchase_items WHERE purchase_id = NEW.purchase_id)
  WHERE id = NEW.purchase_id;
  UPDATE products SET cost_price = NEW.unit_cost WHERE id = NEW.product_id;
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT NEW.product_id, NEW.qty, 'purchase', 'purchase', NEW.purchase_id
    FROM products WHERE id = NEW.product_id AND track_stock = 1;
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT empty_product_id, -NEW.empties_returned, 'empty_out', 'purchase', NEW.purchase_id
    FROM products WHERE id = NEW.product_id AND empty_product_id IS NOT NULL AND NEW.empties_returned > 0;
END;
CREATE TRIGGER trg_purchase_items_ad AFTER DELETE ON purchase_items BEGIN
  UPDATE purchases SET total_amount = (SELECT COALESCE(SUM(amount),0) FROM purchase_items WHERE purchase_id = OLD.purchase_id)
  WHERE id = OLD.purchase_id;
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT OLD.product_id, -OLD.qty, 'purchase_reversal', 'purchase', OLD.purchase_id
    FROM products WHERE id = OLD.product_id AND track_stock = 1;
  INSERT INTO stock_movements (product_id, qty_change, reason, ref_type, ref_id)
    SELECT empty_product_id, OLD.empties_returned, 'empty_out_reversal', 'purchase', OLD.purchase_id
    FROM products WHERE id = OLD.product_id AND empty_product_id IS NOT NULL AND OLD.empties_returned > 0;
END;
-- 進貨明細不給直接修改（刪掉重開，庫存才不會亂）
CREATE TRIGGER trg_purchase_items_bu BEFORE UPDATE ON purchase_items BEGIN
  SELECT RAISE(ABORT, '進貨明細不能直接修改，請刪除後重新輸入');
END;

-- 金額異動紀錄
CREATE TRIGGER trg_audit_payments_i AFTER INSERT ON payments BEGIN
  INSERT INTO audit_log (table_name,row_id,action,new_data)
  VALUES ('payments', NEW.id, 'insert',
    json_object('customer_id',NEW.customer_id,'order_id',NEW.order_id,'amount',NEW.amount,'method',NEW.method,'collected_by',NEW.collected_by));
END;
CREATE TRIGGER trg_audit_payments_u AFTER UPDATE ON payments BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data,new_data)
  VALUES ('payments', NEW.id, 'update',
    json_object('customer_id',OLD.customer_id,'order_id',OLD.order_id,'amount',OLD.amount,'method',OLD.method,'collected_by',OLD.collected_by),
    json_object('customer_id',NEW.customer_id,'order_id',NEW.order_id,'amount',NEW.amount,'method',NEW.method,'collected_by',NEW.collected_by));
END;
CREATE TRIGGER trg_audit_payments_d AFTER DELETE ON payments BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data)
  VALUES ('payments', OLD.id, 'delete',
    json_object('customer_id',OLD.customer_id,'order_id',OLD.order_id,'amount',OLD.amount,'method',OLD.method,'collected_by',OLD.collected_by));
END;
CREATE TRIGGER trg_audit_orders_u AFTER UPDATE OF total_amount, status, driver_id, billing_type ON orders
WHEN OLD.total_amount IS NOT NEW.total_amount OR OLD.status IS NOT NEW.status
  OR OLD.driver_id IS NOT NEW.driver_id OR OLD.billing_type IS NOT NEW.billing_type BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data,new_data)
  VALUES ('orders', NEW.id, 'update',
    json_object('total_amount',OLD.total_amount,'status',OLD.status,'driver_id',OLD.driver_id,'billing_type',OLD.billing_type),
    json_object('total_amount',NEW.total_amount,'status',NEW.status,'driver_id',NEW.driver_id,'billing_type',NEW.billing_type));
END;
CREATE TRIGGER trg_audit_deposits_u AFTER UPDATE ON cylinder_deposits BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data,new_data)
  VALUES ('cylinder_deposits', NEW.id, 'update',
    json_object('customer_id',OLD.customer_id,'qty_change',OLD.qty_change,'amount',OLD.amount,'collected_by',OLD.collected_by),
    json_object('customer_id',NEW.customer_id,'qty_change',NEW.qty_change,'amount',NEW.amount,'collected_by',NEW.collected_by));
END;
CREATE TRIGGER trg_audit_deposits_d AFTER DELETE ON cylinder_deposits BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data)
  VALUES ('cylinder_deposits', OLD.id, 'delete',
    json_object('customer_id',OLD.customer_id,'qty_change',OLD.qty_change,'amount',OLD.amount,'collected_by',OLD.collected_by));
END;
CREATE TRIGGER trg_audit_handovers_u AFTER UPDATE ON cash_handovers BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data,new_data)
  VALUES ('cash_handovers', NEW.id, 'update',
    json_object('driver_id',OLD.driver_id,'work_date',OLD.work_date,'amount',OLD.amount),
    json_object('driver_id',NEW.driver_id,'work_date',NEW.work_date,'amount',NEW.amount));
END;
CREATE TRIGGER trg_audit_handovers_d AFTER DELETE ON cash_handovers BEGIN
  INSERT INTO audit_log (table_name,row_id,action,old_data)
  VALUES ('cash_handovers', OLD.id, 'delete',
    json_object('driver_id',OLD.driver_id,'work_date',OLD.work_date,'amount',OLD.amount));
END;

-- =====================================================================
-- Views：報表與核對
-- =====================================================================

-- 目前庫存
CREATE VIEW v_stock AS
SELECT p.id AS product_id, p.sku, p.name, p.category, p.unit,
       COALESCE(SUM(m.qty_change),0) AS qty,
       p.low_stock_alert,
       CASE WHEN p.low_stock_alert IS NOT NULL AND COALESCE(SUM(m.qty_change),0) < p.low_stock_alert
            THEN 1 ELSE 0 END AS is_low
FROM products p LEFT JOIN stock_movements m ON m.product_id = p.id
WHERE p.track_stock = 1
GROUP BY p.id;

-- 每張訂單的收款狀況（現結單應該要收齊）
CREATE VIEW v_order_payment AS
SELECT o.id AS order_id, o.order_no, o.customer_id, o.driver_id, o.status, o.billing_type,
       date(o.delivered_at) AS delivered_date,
       o.total_amount,
       COALESCE((SELECT SUM(amount) FROM payments WHERE order_id = o.id),0) AS paid_amount,
       o.total_amount - COALESCE((SELECT SUM(amount) FROM payments WHERE order_id = o.id),0) AS unpaid_amount
FROM orders o
WHERE o.status <> 'cancelled';

-- 每日師傅對帳：
--   應收現金（已送達的現結單金額）
--   師傅回報收到的現金
--   師傅收的押瓶費現金
--   師傅交回店裡的現金（應該 = 回報收到的 + 押瓶費）
CREATE VIEW v_driver_daily AS
WITH days AS (
  SELECT driver_id, date(delivered_at) AS work_date FROM orders
    WHERE status = 'delivered' AND driver_id IS NOT NULL
  UNION
  SELECT collected_by, date(received_at) FROM payments
    WHERE method = 'cash' AND collected_by IN (SELECT id FROM users WHERE role = 'driver')
  UNION
  SELECT collected_by, date(happened_at) FROM cylinder_deposits
    WHERE method = 'cash' AND collected_by IN (SELECT id FROM users WHERE role = 'driver')
  UNION
  SELECT driver_id, work_date FROM cash_handovers
)
SELECT d.driver_id, u.name AS driver_name, d.work_date,
  COALESCE((SELECT COUNT(*) FROM orders o
             WHERE o.driver_id = d.driver_id AND o.status = 'delivered'
               AND date(o.delivered_at) = d.work_date),0) AS delivered_orders,
  COALESCE((SELECT SUM(o.total_amount) FROM orders o
             WHERE o.driver_id = d.driver_id AND o.status = 'delivered' AND o.billing_type = 'cash'
               AND date(o.delivered_at) = d.work_date),0) AS expected_cash,
  COALESCE((SELECT SUM(p.amount) FROM payments p
             WHERE p.collected_by = d.driver_id AND p.method = 'cash'
               AND date(p.received_at) = d.work_date),0) AS reported_cash,
  COALESCE((SELECT SUM(x.amount) FROM cylinder_deposits x
             WHERE x.collected_by = d.driver_id AND x.method = 'cash'
               AND date(x.happened_at) = d.work_date),0) AS deposit_cash,
  COALESCE((SELECT SUM(h.amount) FROM cash_handovers h
             WHERE h.driver_id = d.driver_id AND h.work_date = d.work_date),0) AS handed_cash
FROM days d JOIN users u ON u.id = d.driver_id;

-- 對帳差異（只列出對不上的）
CREATE VIEW v_driver_daily_mismatch AS
SELECT *,
       reported_cash - expected_cash AS diff_reported_vs_expected,
       handed_cash   - (reported_cash + deposit_cash) AS diff_handed_vs_reported
FROM v_driver_daily
WHERE reported_cash <> expected_cash OR handed_cash <> reported_cash + deposit_cash;

-- 客戶押瓶狀況：手上有幾支、押金多少
CREATE VIEW v_customer_deposits AS
SELECT x.customer_id, c.name AS customer_name, x.product_id, p.name AS cylinder_name,
       SUM(x.qty_change) AS cylinders_held,
       SUM(x.amount)     AS deposit_amount
FROM cylinder_deposits x
JOIN customers c ON c.id = x.customer_id
JOIN products  p ON p.id = x.product_id
GROUP BY x.customer_id, x.product_id;

-- 押金總額（店裡收著的押金，將來要退的）
CREATE VIEW v_deposit_total AS
SELECT COALESCE(SUM(amount),0) AS total_deposit, COALESCE(SUM(qty_change),0) AS cylinders_out
FROM cylinder_deposits;

-- 客戶帳款餘額（正數 = 客戶還欠店裡）
CREATE VIEW v_customer_balance AS
SELECT c.id AS customer_id, c.name, c.billing_type,
  COALESCE((SELECT SUM(total_amount) FROM orders o
             WHERE o.customer_id = c.id AND o.status = 'delivered'),0) AS total_sales,
  COALESCE((SELECT SUM(amount) FROM payments p WHERE p.customer_id = c.id),0) AS total_paid,
  COALESCE((SELECT SUM(total_amount) FROM orders o
             WHERE o.customer_id = c.id AND o.status = 'delivered'),0)
  - COALESCE((SELECT SUM(amount) FROM payments p WHERE p.customer_id = c.id),0) AS balance
FROM customers c;

-- 月結客戶每月帳單
CREATE VIEW v_monthly_statement AS
SELECT o.customer_id, c.name, strftime('%Y-%m', o.delivered_at) AS period,
       COUNT(*) AS order_count, SUM(o.total_amount) AS amount
FROM orders o JOIN customers c ON c.id = o.customer_id
WHERE o.status = 'delivered' AND o.billing_type = 'monthly'
GROUP BY o.customer_id, period;

-- 供應商應付餘額（正數 = 店裡還欠供應商）
CREATE VIEW v_supplier_balance AS
SELECT s.id AS supplier_id, s.name,
  COALESCE((SELECT SUM(total_amount) FROM purchases WHERE supplier_id = s.id),0) AS total_purchased,
  COALESCE((SELECT SUM(amount) FROM supplier_payments WHERE supplier_id = s.id),0) AS total_paid,
  COALESCE((SELECT SUM(total_amount) FROM purchases WHERE supplier_id = s.id),0)
  - COALESCE((SELECT SUM(amount) FROM supplier_payments WHERE supplier_id = s.id),0) AS balance
FROM suppliers s;

-- 毛利（以送達當下的商品進價估算）
CREATE VIEW v_daily_profit AS
SELECT date(o.delivered_at) AS work_date, p.category,
       SUM(oi.amount) AS revenue,
       SUM(oi.qty * p.cost_price) AS est_cost,
       SUM(oi.amount) - SUM(oi.qty * p.cost_price) AS est_profit
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
WHERE o.status = 'delivered'
GROUP BY work_date, p.category;

-- 換瓶提醒：最後一次送瓦斯 + 換瓶天數 <= 今天
CREATE VIEW v_refill_due AS
WITH last_gas AS (
  SELECT o.customer_id, MAX(o.delivered_at) AS last_delivered
  FROM orders o
  JOIN order_items oi ON oi.order_id = o.id
  JOIN products p ON p.id = oi.product_id
  WHERE o.status = 'delivered' AND p.category = 'gas'
  GROUP BY o.customer_id
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

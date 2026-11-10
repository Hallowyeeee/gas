-- =====================================================================
-- 第 5 版：派送
--   orders.delivery_date / delivery_slot：預約哪天、哪個時段送（欄位由程式先加好）
--   driver_shifts：師傅每天上午／下午／晚上有沒有班
--   call_logs.handled_at / order_id：來電處理了沒（未處理來電提醒）
--   customers.order_note：訂單常態備註（每張單自動帶出來）
-- =====================================================================
UPDATE orders SET delivery_date = date(ordered_at) WHERE delivery_date IS NULL;
CREATE INDEX IF NOT EXISTS idx_orders_delivery ON orders(delivery_date, status);

-- 沒填預約日期就是下單當天
CREATE TRIGGER IF NOT EXISTS trg_orders_delivery_date AFTER INSERT ON orders
WHEN NEW.delivery_date IS NULL
BEGIN
  UPDATE orders SET delivery_date = date(NEW.ordered_at) WHERE id = NEW.id;
END;

CREATE TABLE IF NOT EXISTS driver_shifts (
  driver_id INTEGER NOT NULL REFERENCES users(id),
  work_date TEXT    NOT NULL,                               -- YYYY-MM-DD
  slot      TEXT    NOT NULL CHECK (slot IN ('am','pm','eve')),
  PRIMARY KEY (driver_id, work_date, slot)
);

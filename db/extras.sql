-- 存氣紀錄、調漲紀錄：每次開程式都會跑一次，全部「沒有才建」，不佔版本號。

-- 存氣：客人退回的鋼瓶裡還剩的瓦斯，只記公斤數（不算錢）。用掉存氣記負數。
CREATE TABLE IF NOT EXISTS gas_credits (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  product_id  INTEGER REFERENCES products(id),     -- 哪一種瓶（例如 20kg 瓦斯），可以不選
  kg          REAL    NOT NULL CHECK (kg <> 0),     -- 正數＝退回存氣，負數＝用掉
  order_id    INTEGER REFERENCES orders(id),        -- 送達回報時填的
  note        TEXT,
  created_by  INTEGER REFERENCES users(id),
  created_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_gas_credits_customer ON gas_credits(customer_id, created_at);

-- 調漲紀錄：改客人瓦斯價格時自動記一筆，也可以手動補
CREATE TABLE IF NOT EXISTS price_raises (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id),
  product_id  INTEGER REFERENCES products(id),
  old_price   INTEGER,
  new_price   INTEGER,
  raised_on   TEXT    NOT NULL,                     -- 調漲日期 YYYY-MM-DD
  note        TEXT,
  created_by  INTEGER REFERENCES users(id),
  created_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_price_raises_customer ON price_raises(customer_id, raised_on);

-- 之前單一「調漲日期」欄有填的，搬成一筆紀錄（只搬一次）
INSERT INTO price_raises (customer_id, raised_on, note)
SELECT id, price_raised_on, '之前的調漲日期' FROM customers
WHERE price_raised_on IS NOT NULL AND price_raised_on <> ''
  AND NOT EXISTS (SELECT 1 FROM price_raises r WHERE r.customer_id = customers.id AND r.raised_on = customers.price_raised_on);

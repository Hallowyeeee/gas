-- =====================================================================
-- 第 4 版：一位客戶可以有很多支電話
-- customers.phone 還是「主要號碼」；其他號碼都放 customer_phones。
-- 原本的 phone2 搬進來，之後有人寫 phone2（例如舊系統 F12）也會自動搬進來。
-- =====================================================================
CREATE TABLE IF NOT EXISTS customer_phones (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
  phone       TEXT    NOT NULL,
  note        TEXT,                                 -- 小備註，例如「老闆手機」
  created_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  UNIQUE (customer_id, phone)
);
CREATE INDEX IF NOT EXISTS idx_customer_phones_phone ON customer_phones(phone);

INSERT OR IGNORE INTO customer_phones (customer_id, phone)
  SELECT id, trim(phone2) FROM customers
  WHERE trim(COALESCE(phone2, '')) <> '' AND trim(phone2) <> trim(COALESCE(phone, ''));
UPDATE customers SET phone2 = NULL WHERE phone2 IS NOT NULL;

CREATE TRIGGER IF NOT EXISTS trg_customers_phone2_ins AFTER INSERT ON customers
WHEN NEW.phone2 IS NOT NULL
BEGIN
  INSERT OR IGNORE INTO customer_phones (customer_id, phone)
    SELECT NEW.id, trim(NEW.phone2) WHERE trim(NEW.phone2) <> '' AND trim(NEW.phone2) <> trim(COALESCE(NEW.phone, ''));
  UPDATE customers SET phone2 = NULL WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS trg_customers_phone2_upd AFTER UPDATE OF phone2 ON customers
WHEN NEW.phone2 IS NOT NULL
BEGIN
  INSERT OR IGNORE INTO customer_phones (customer_id, phone)
    SELECT NEW.id, trim(NEW.phone2) WHERE trim(NEW.phone2) <> '' AND trim(NEW.phone2) <> trim(COALESCE(NEW.phone, ''));
  UPDATE customers SET phone2 = NULL WHERE id = NEW.id;
END;

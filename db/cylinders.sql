-- 鋼瓶清單：每次開程式都會跑一次，全部「沒有才建」，不佔資料庫版本號。
-- 以「容器號碼」（鋼瓶本身的號碼，不會換）當身分；
-- 「標籤條碼」是定期檢驗合格標示貼紙上的條碼，每次重新檢驗會換新貼紙、號碼跟著換。

CREATE TABLE IF NOT EXISTS cylinders (
  id           INTEGER PRIMARY KEY,
  serial       TEXT    NOT NULL UNIQUE,              -- 容器號碼（例 707060）
  label_code   TEXT    UNIQUE,                       -- 目前貼紙的條碼（例 AW16586603）
  size_kg      REAL,                                 -- 容器規格（20、16、4 公斤…）
  tare_kg      REAL,                                 -- 容器實重（含閥）
  made_on      TEXT,                                 -- 出廠耐壓試驗日期 YYYY-MM（可以沒有）
  inspected_on TEXT,                                 -- 定期檢驗日期 YYYY-MM-DD
  next_due     TEXT    NOT NULL,                     -- 下次檢驗期限 YYYY-MM-DD
  status       TEXT    NOT NULL DEFAULT 'store'
               CHECK (status IN ('store','customer','scrapped')), -- 店裡 / 在客人家 / 報廢
  customer_id  INTEGER REFERENCES customers(id),     -- 在哪個客人家（status = customer）
  note         TEXT,
  photo        BLOB,                                 -- 貼紙照片（壓縮過的 JPEG，可以沒有）
  created_by   INTEGER REFERENCES users(id),
  created_at   TEXT    NOT NULL DEFAULT (datetime('now','localtime')),
  updated_at   TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_cylinders_due ON cylinders(status, next_due);

-- 換過的舊貼紙：掃到舊條碼也找得到是哪一支
CREATE TABLE IF NOT EXISTS cylinder_labels (
  id           INTEGER PRIMARY KEY,
  cylinder_id  INTEGER NOT NULL REFERENCES cylinders(id),
  label_code   TEXT    NOT NULL,
  next_due     TEXT,                                 -- 那張貼紙上的下次檢驗期限
  replaced_by  INTEGER REFERENCES users(id),
  replaced_at  TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
);
CREATE INDEX IF NOT EXISTS idx_cylinder_labels_code ON cylinder_labels(label_code);

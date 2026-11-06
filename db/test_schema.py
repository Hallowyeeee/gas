"""建立一個暫時的資料庫、放範例資料，檢查金額、庫存、對帳是否算對。
執行：python db/test_schema.py
"""
import sqlite3
import sys
from pathlib import Path

HERE = Path(__file__).parent


def fresh_db():
    db = sqlite3.connect(":memory:")
    db.execute("PRAGMA foreign_keys = ON")
    db.executescript((HERE / "schema.sql").read_text(encoding="utf-8"))
    db.executescript((HERE / "sample_data.sql").read_text(encoding="utf-8"))
    return db


def one(db, sql, *args):
    return db.execute(sql, args).fetchone()


failures = []


def check(name, got, want):
    ok = got == want
    print(("OK  " if ok else "FAIL"), name, "→", got, "" if ok else f"(應該是 {want})")
    if not ok:
        failures.append(name)


def expect_error(name, db, sql, *args):
    try:
        db.execute(sql, args)
    except sqlite3.DatabaseError as e:
        print("OK  ", name, "→ 擋下來了：", e)
        return
    print("FAIL", name, "→ 沒有擋下來")
    failures.append(name)


db = fresh_db()

# 訂單總額自動加總
check("訂單 1 總額", one(db, "SELECT total_amount FROM orders WHERE id=1")[0], 1800)
check("訂單 3 總額（含折扣）", one(db, "SELECT total_amount FROM orders WHERE id=3")[0], 1750 + 450)

# 庫存：進 50 瓶 20kg、送出 1+2+2+2 = 7 瓶 → 43；空瓶期初 40、回收 5、還供應商 30 → 15
check("20kg 瓦斯庫存", one(db, "SELECT qty FROM v_stock WHERE sku='GAS20'")[0], 43)
check("20kg 空瓶庫存", one(db, "SELECT qty FROM v_stock WHERE sku='EMPTY20'")[0], 15)
check("爐具庫存", one(db, "SELECT qty FROM v_stock WHERE sku='STOVE-A1'")[0], 4)

# 師傅阿明當天：應收現金 1800 + 2200 = 4000（訂單 2 是月結不算），回報 3900，交回 3900
row = one(db, """SELECT expected_cash, reported_cash, handed_cash
                 FROM v_driver_daily WHERE driver_name='阿明' AND work_date='2026-11-06'""")
check("阿明 11/6 應收/回報/交回", row, (4000, 3900, 3900))
check("對不上的清單有阿明", one(db, "SELECT COUNT(*) FROM v_driver_daily_mismatch WHERE driver_name='阿明'")[0], 1)
check("阿強 11/6 全部對得上", one(db, "SELECT COUNT(*) FROM v_driver_daily_mismatch WHERE driver_name='阿強'")[0], 0)

# 客戶欠款：王小明訂單 3 少收 100；陳記餐廳月結 1800 還沒付
check("王小明欠款", one(db, "SELECT balance FROM v_customer_balance WHERE name='王小明'")[0], 100)
check("陳記餐廳欠款", one(db, "SELECT balance FROM v_customer_balance WHERE name='陳記餐廳'")[0], 1800)
check("陳記 11 月月結單", one(db, "SELECT amount FROM v_monthly_statement WHERE name='陳記餐廳' AND period='2026-11'")[0], 1800)

# 供應商：進貨 50×700 + 5×1500 = 42500，付了 40000 → 欠 2500
check("供應商應付", one(db, "SELECT balance FROM v_supplier_balance WHERE supplier_id=1")[0], 2500)

# 換瓶提醒：林太太 10/1 送過、30 天換一次 → 已到期
check("林太太在換瓶提醒裡", one(db, "SELECT COUNT(*) FROM v_refill_due WHERE name='林太太'")[0], 1)

# 訂單 1 送了 2 瓶，改回未送達 → 庫存沖回 2 瓶
db.execute("UPDATE orders SET status='dispatched', delivered_at=NULL WHERE id=1")
check("訂單 1 改回未送達後 20kg 庫存", one(db, "SELECT qty FROM v_stock WHERE sku='GAS20'")[0], 45)
check("金額異動有留紀錄", one(db, "SELECT COUNT(*) FROM audit_log WHERE table_name='orders' AND row_id=1")[0] >= 1, True)

# 防呆
expect_error("已送達的訂單不能改明細", db, "UPDATE order_items SET qty=5, amount=5*900 WHERE order_id=3 AND product_id=1")
expect_error("明細金額必須 = 數量 × 單價 - 折扣", db,
             "INSERT INTO order_items (order_id,product_id,qty,unit_price,amount) VALUES (1,1,1,900,800)")
expect_error("派單一定要有師傅", db, "INSERT INTO orders (order_no,customer_id,status,billing_type,address) VALUES ('X',1,'dispatched','cash','x')")
expect_error("收款金額不能是 0", db, "INSERT INTO payments (customer_id,amount) VALUES (1,0)")

print()
if failures:
    print(f"有 {len(failures)} 項沒通過")
    sys.exit(1)
print("全部通過")

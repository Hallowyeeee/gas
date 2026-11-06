-- 範例資料：只用來測試，正式上線不會放進去
INSERT INTO settings VALUES ('shop_name','範例瓦斯行'), ('default_refill_days','30');

INSERT INTO users (id,name,role,login_name,password_hash) VALUES
  (1,'老闆','admin','boss','x'),
  (2,'阿明','driver','ming','x'),
  (3,'阿強','driver','chiang','x');

INSERT INTO customers (id,name,phone,address,billing_type,refill_days) VALUES
  (1,'王小明','0912000001','中正路 1 號','cash',NULL),
  (2,'陳記餐廳','0912000002','中山路 2 號','monthly',7),
  (3,'林太太','0912000003','民生路 3 號','cash',30),
  (4,'張先生','0912000004','復興路 4 號','cash',NULL);

INSERT INTO products (id,sku,name,category,sale_price,cost_price,track_stock,empty_product_id) VALUES
  (2,'EMPTY20','20kg 空瓶','empty_cylinder',0,0,1,NULL),
  (1,'GAS20','20kg 家用瓦斯','gas',900,700,1,2),
  (3,'STOVE-A1','雙口嵌入爐 A1','stove',2200,1500,1,NULL),
  (4,'HOSE','瓦斯管','consumable',450,200,1,NULL);

-- 原本店裡有 40 個空瓶（盤點）
INSERT INTO stock_movements (product_id,qty_change,reason,note) VALUES (2,40,'adjust','期初盤點');

-- 進貨
INSERT INTO suppliers (id,name) VALUES (1,'大華瓦斯分裝場'), (2,'五金行');
INSERT INTO purchases (id,supplier_id,purchased_at) VALUES (1,1,'2026-09-30 09:00:00'), (2,2,'2026-09-30 10:00:00');
INSERT INTO purchase_items (purchase_id,product_id,qty,unit_cost,amount,empties_returned) VALUES
  (1,1,50,700,35000,30),
  (1,3,5,1500,7500,0),
  (2,4,10,200,2000,0);
INSERT INTO supplier_payments (supplier_id,purchase_id,amount) VALUES (1,1,40000), (2,2,2000);

-- 訂單 4：林太太 10/1 阿強送 1 瓶
INSERT INTO orders (id,order_no,customer_id,billing_type,address,driver_id,status,ordered_at)
  VALUES (4,'20261001-001',3,'cash','民生路 3 號',3,'dispatched','2026-10-01 08:00:00');
INSERT INTO order_items (order_id,product_id,qty,unit_price,amount,empties_returned) VALUES (4,1,1,900,900,1);
UPDATE orders SET status='delivered', delivered_at='2026-10-01 09:00:00' WHERE id=4;
INSERT INTO payments (customer_id,order_id,amount,collected_by,received_at) VALUES (3,4,900,3,'2026-10-01 09:00:00');
INSERT INTO cash_handovers (driver_id,work_date,amount,received_by) VALUES (3,'2026-10-01',900,1);

-- 11/6 阿明：訂單 1（現結）、2（月結）、3（現結、少收 100）
INSERT INTO orders (id,order_no,customer_id,billing_type,address,driver_id,status) VALUES
  (1,'20261106-001',4,'cash','復興路 4 號',2,'dispatched'),
  (2,'20261106-002',2,'monthly','中山路 2 號',2,'dispatched'),
  (3,'20261106-003',1,'cash','中正路 1 號',2,'dispatched');
INSERT INTO order_items (order_id,product_id,qty,unit_price,amount,discount,empties_returned) VALUES
  (1,1,2,900,1800,0,2),
  (2,1,2,900,1800,0,2),
  (3,1,2,900,1750,50,0),
  (3,4,1,450,450,0,0);
UPDATE orders SET status='delivered', delivered_at='2026-11-06 10:00:00' WHERE id IN (1,2,3);
INSERT INTO payments (customer_id,order_id,amount,collected_by,received_at) VALUES
  (4,1,1800,2,'2026-11-06 10:00:00'),
  (1,3,2100,2,'2026-11-06 11:00:00');
INSERT INTO cash_handovers (driver_id,work_date,amount,received_by) VALUES (2,'2026-11-06',3900,1);

-- 11/6 阿強：訂單 5 賣一台爐具
INSERT INTO orders (id,order_no,customer_id,billing_type,address,driver_id,status)
  VALUES (5,'20261106-004',4,'cash','復興路 4 號',3,'dispatched');
INSERT INTO order_items (order_id,product_id,qty,unit_price,amount) VALUES (5,3,1,2200,2200);
UPDATE orders SET status='delivered', delivered_at='2026-11-06 14:00:00' WHERE id=5;
INSERT INTO payments (customer_id,order_id,amount,collected_by,received_at) VALUES (4,5,2200,3,'2026-11-06 14:00:00');
INSERT INTO cash_handovers (driver_id,work_date,amount,received_by) VALUES (3,'2026-11-06',2200,1);

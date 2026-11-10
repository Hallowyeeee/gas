-- =====================================================================
-- 第 6 版：派單通知確實跳到師傅手機
--   orders.seen_at：師傅幾點看到這張單（點開通知或打開我的單）
--   orders.reminded_at：沒看到時，幾點再響過一次
--   push_subscriptions.platform：哪種手機（iphone / samsung / android / other）
--   push_subscriptions.last_sent_at / last_ack_at / last_error：通知送出、手機收到、失敗原因
--   （欄位由程式先加好）
-- =====================================================================
CREATE INDEX IF NOT EXISTS idx_orders_unseen ON orders(status, seen_at, reminded_at);

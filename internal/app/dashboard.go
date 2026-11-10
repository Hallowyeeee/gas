package app

import (
	"context"
	"fmt"
)

// ---------------------------------------------------------------------
// 儀表板：一眼看懂今天、本週、本月
// 營業額＝已送達訂單的金額，日期看送達那天。
// ---------------------------------------------------------------------

func (a *App) scalar(ctx context.Context, q string, args ...any) (int64, error) {
	var n int64
	err := a.DB.QueryRowContext(ctx, "SELECT COALESCE(("+q+"),0)", args...).Scan(&n)
	return n, err
}

func (a *App) dashboard(ctx context.Context, period string) (map[string]any, error) {
	const sales = `SELECT SUM(total_amount) FROM orders WHERE status = 'delivered' AND date(delivered_at) BETWEEN ? AND ?`
	out := map[string]any{}
	var today, yesterday, weekStart, lastWeekStart, monthStart, lastMonthStart, d30 string
	if err := a.DB.QueryRowContext(ctx, `SELECT date('now','localtime'), date('now','localtime','-1 day'),
		date('now','localtime','-6 days','weekday 1'), date('now','localtime','-6 days','weekday 1','-7 days'),
		date('now','localtime','start of month'), date('now','localtime','start of month','-1 month'),
		date('now','localtime','-30 days')`).Scan(&today, &yesterday, &weekStart, &lastWeekStart, &monthStart, &lastMonthStart, &d30); err != nil {
		return nil, err
	}
	nums := []struct {
		key  string
		q    string
		args []any
	}{
		{"sales_today", sales, []any{today, today}},
		{"sales_yesterday", sales, []any{yesterday, yesterday}},
		{"sales_30d", sales, []any{d30, yesterday}},
		{"orders_today", `SELECT COUNT(*) FROM orders WHERE status <> 'cancelled' AND date(ordered_at) = ?`, []any{today}},
		{"orders_yesterday", `SELECT COUNT(*) FROM orders WHERE status <> 'cancelled' AND date(ordered_at) = ?`, []any{yesterday}},
		{"dispatched_today", `SELECT COUNT(*) FROM orders WHERE status IN ('dispatched','delivered') AND date(dispatched_at) = ?`, []any{today}},
		{"due_today", `SELECT COUNT(*) FROM orders WHERE status <> 'cancelled' AND delivery_date = ?`, []any{today}},
		{"done_today", `SELECT COUNT(*) FROM orders WHERE status = 'delivered' AND delivery_date = ?`, []any{today}},
		{"overdue", `SELECT COUNT(*) FROM orders WHERE status IN ('new','dispatched') AND delivery_date < ?`, []any{today}},
		{"sales_week", sales, []any{weekStart, today}},
		{"sales_month", sales, []any{monthStart, today}},
	}
	for _, n := range nums {
		v, err := a.scalar(ctx, n.q, n.args...)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.key, err)
		}
		out[n.key] = v
	}
	// 上週、上月只算到「跟今天同一天」為止，比較才公平
	var lwEnd, lmEnd string
	a.DB.QueryRowContext(ctx, `SELECT date('now','localtime','-7 days'), date('now','localtime','-1 month')`).Scan(&lwEnd, &lmEnd)
	out["sales_last_week"], _ = a.scalar(ctx, sales, lastWeekStart, lwEnd)
	out["sales_last_month"], _ = a.scalar(ctx, sales, lastMonthStart, lmEnd)
	out["today"], out["week_start"], out["month_start"] = today, weekStart, monthStart

	// 今天每小時接幾張單
	hourly := make([]int64, 24)
	hs, err := rows(ctx, a.DB, `SELECT CAST(strftime('%H', ordered_at) AS INTEGER) AS h, COUNT(*) AS n FROM orders
		WHERE status <> 'cancelled' AND date(ordered_at) = ? GROUP BY h`, today)
	if err != nil {
		return nil, err
	}
	for _, h := range hs {
		if i, ok := h["h"].(int64); ok && i >= 0 && i < 24 {
			hourly[i] = h["n"].(int64)
		}
	}
	out["hourly"] = hourly

	// 瓦斯品項銷售（當日／當週／當月）
	from := today
	switch period {
	case "week":
		from = weekStart
	case "month":
		from = monthStart
	}
	out["period"] = period
	if out["gas_sales"], err = rows(ctx, a.DB, `SELECT p.name, SUM(oi.qty) AS qty, SUM(oi.amount) AS amount
		FROM order_items oi JOIN orders o ON o.id = oi.order_id JOIN products p ON p.id = oi.product_id
		WHERE o.status = 'delivered' AND p.category = 'gas' AND date(o.delivered_at) BETWEEN ? AND ?
		GROUP BY p.id ORDER BY qty DESC`, from, today); err != nil {
		return nil, err
	}

	// 今年每個月營業額
	months := make([]int64, 12)
	ms, err := rows(ctx, a.DB, `SELECT CAST(strftime('%m', delivered_at) AS INTEGER) AS m, SUM(total_amount) AS s FROM orders
		WHERE status = 'delivered' AND strftime('%Y', delivered_at) = strftime('%Y', 'now', 'localtime') GROUP BY m`)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if i, ok := m["m"].(int64); ok && i >= 1 && i <= 12 {
			months[i-1], _ = m["s"].(int64)
		}
	}
	out["months"] = months
	return out, nil
}

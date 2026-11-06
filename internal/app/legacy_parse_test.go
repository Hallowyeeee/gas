package app

import (
	"os"
	"testing"
)

func loadLegacySample(t *testing.T) *LegacyPage {
	t.Helper()
	b, err := os.ReadFile("testdata/easy88_order_form.html")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseLegacyPage(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseLegacyPage(t *testing.T) {
	p := loadLegacySample(t)
	eq := func(name string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v，應該是 %v", name, got, want)
		}
	}
	eq("客戶編號", p.LegacyID, "86000")
	eq("名稱", p.Name, "測試客戶甲")
	eq("電話", p.Phone, "0912000111")
	eq("地址", p.Address, "中壢區測試路1號※複合桶，範例街2號(送一般)")
	eq("區域", p.Area, "中壢區")
	eq("注意事項", p.Note, "@8+，目前無寄桶，樓層2樓30.3樓50")
	eq("要不要發票", p.NeedsInvoice != nil && !*p.NeedsInvoice, true)
	eq("統編", p.TaxID, "")
	eq("規格數", len(p.Sizes), 7)
	eq("16kg 欠桶", p.Balance["16kg"], -2)
	eq("欠桶只有一種", len(p.Balance), 1)
	eq("20kg 累計", p.Totals["20kg"], 38)
	eq("20kg 售價", p.Prices["20kg"], 770)
	eq("4kg 售價", p.Prices["4kg"], 240)
	eq("送貨筆數", len(p.Deliveries), 33)
	d := p.Deliveries[2]
	eq("第3筆時間", d.At, "2026-09-10 16:46:13")
	eq("第3筆品項", d.ItemsText(), "20kg×1(770)、16kg×1(660)")
	eq("第3筆金額", d.Amount, 1430)
	eq("第3筆司機", d.Driver, "師傅甲")
	eq("第3筆付款", d.PayStatus, "已付款")
	eq("沒有已付欄", d.Paid == nil, true)
	eq("寄桶", p.Deliveries[3].Cylinder, "寄桶")
	last := p.Deliveries[len(p.Deliveries)-1]
	eq("最後一筆", last.At+" "+last.ItemsText(), "2023-09-12 12:00:02 16kg×1(550)")
	eq("出桶鋼瓶數", len(p.CylindersOut), 4)
	c := p.CylindersOut[0]
	eq("鋼瓶條碼", c.Barcode, "AW05113650")
	eq("鋼瓶規格", c.Size, "16kg")
	eq("下次檢驗", c.NextInspection, "2025-06-21")
	eq("出桶時間", c.OutAt, "2024-12-06 15:16:28")
	eq("桶齡", c.AgeYears != nil && *c.AgeYears == 27, true)
	if len(p.Warnings) != 0 {
		t.Errorf("不應該有警告：%v", p.Warnings)
	}
}

func TestParseLegacyNotCustomerPage(t *testing.T) {
	if _, err := ParseLegacyPage(`<html><body><table><tr><td>名稱</td><td>x</td></tr></table></body></html>`); err != errNotCustomerPage {
		t.Fatalf("應該說不是客戶頁，結果 %v", err)
	}
}

func TestLegacyHelpers(t *testing.T) {
	if a, b := splitPhones("03-4561234、0912345678"); a != "03-4561234" || b != "0912345678" {
		t.Errorf("兩支電話 %q %q", a, b)
	}
	if a := areaOf("桃園市平鎮區中豐路1號"); a != "平鎮區" {
		t.Errorf("區域 %q", a)
	}
	if d := rocDate("116-02-21"); d != "2027-02-21" {
		t.Errorf("民國 %q", d)
	}
}

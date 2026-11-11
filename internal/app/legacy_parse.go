package app

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// ---------------------------------------------------------------------
// 讀舊系統（捷程 easy88 瓦斯行 3.0）的「訂單輸入表」網頁
// 網頁是「標題格＋內容格」的表格，所以照標題字找內容，不靠位置。
// ---------------------------------------------------------------------

// LegacyPage 是從一頁讀出來的資料。
type LegacyPage struct {
	LegacyID     string           `json:"legacy_id"`     // 客戶編號
	Name         string           `json:"name"`          // 客戶名稱
	Phone        string           `json:"phone"`         // 客戶電話
	Phone2       string           `json:"phone2"`        // 第二支電話（電話欄有兩支時）
	Address      string           `json:"address"`       // 客戶住址
	Area         string           `json:"area"`          // 從地址抓出來的區（中壢區）
	Invoice      string           `json:"invoice"`       // 統一發票欄（無須發票…）
	NeedsInvoice *bool            `json:"needs_invoice"` // 看不出來是 nil
	TaxID        string           `json:"tax_id"`        // 統一編號
	Note         string           `json:"note"`          // 注意事項
	CylinderType string           `json:"cylinder_type"` // 瓦斯桶別
	Sizes        []string         `json:"sizes"`         // 規格：50丙、20丙、50kg、20kg…
	Balance      map[string]int   `json:"balance"`       // 欠桶數量（負數 = 寄桶在店裡）
	Totals       map[string]int   `json:"totals"`        // 累計購買
	Prices       map[string]int   `json:"prices"`        // 銷售價格
	Deliveries   []LegacyDelivery `json:"deliveries"`    // 送貨紀錄
	CylindersOut []LegacyCylinder `json:"cylinders_out"` // 出桶鋼瓶
	Warnings     []string         `json:"warnings"`
}

// LegacyDelivery 是一筆舊送貨紀錄。
type LegacyDelivery struct {
	At        string       `json:"at"` // 2026-10-21 15:29:31
	Items     []LegacyItem `json:"items"`
	StoredGas int          `json:"stored_gas"`
	Amount    int          `json:"amount"`
	Paid      *int         `json:"paid"`
	Cylinder  string       `json:"cylinder"` // 欠桶 / 寄桶 / 還桶
	PayStatus string       `json:"pay_status"`
	Driver    string       `json:"driver"`
	Position  string       `json:"position"`
	Note      string       `json:"note"`
}

// LegacyItem 一筆送貨裡的一種規格。
type LegacyItem struct {
	Size  string `json:"size"`
	Qty   int    `json:"qty"`
	Price *int   `json:"price"`
}

// LegacyCylinder 出桶鋼瓶清單的一支。
type LegacyCylinder struct {
	No             string `json:"no"`
	Barcode        string `json:"barcode"`
	Size           string `json:"size"`
	NextInspection string `json:"next_inspection"`
	AgeYears       *int   `json:"age_years"`
	Driver         string `json:"driver"`
	OutAt          string `json:"out_at"`
}

// ItemsText 給人看的品項：20kg×1(770)、16kg×1(660)
func (d LegacyDelivery) ItemsText() string {
	parts := make([]string, 0, len(d.Items))
	for _, it := range d.Items {
		s := fmt.Sprintf("%s×%d", it.Size, it.Qty)
		if it.Price != nil {
			s += fmt.Sprintf("(%d)", *it.Price)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "、")
}

var legacyTelRe = regexp.MustCompile(`customer_tel=([^&"'\s<>]+)`)

var errNotCustomerPage = errors.New("這一頁不是客戶的訂單頁（找不到「客戶編號」），請先打開客戶的訂單輸入表再按 F12")

// 舊系統常見的規格，「規格」那一列讀不到時用
var defaultLegacySizes = []string{"50丙", "20丙", "50kg", "20kg", "16kg", "10kg", "4kg"}

// ParseLegacyPage 讀一頁 HTML。
func ParseLegacyPage(src string) (*LegacyPage, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("網頁讀不懂：%w", err)
	}
	p := &LegacyPage{Balance: map[string]int{}, Totals: map[string]int{}, Prices: map[string]int{}}
	tables := findAll(doc, atom.Table)

	// 1. 標題格＋內容格
	fields := map[string]*string{
		"客戶編號": &p.LegacyID, "客戶名稱": &p.Name, "客戶電話": &p.Phone, "客戶住址": &p.Address,
		"統一發票": &p.Invoice, "統一編號": &p.TaxID, "注意事項": &p.Note, "瓦斯桶別": &p.CylinderType,
	}
	found := map[string]bool{}
	for _, t := range tables {
		for _, tr := range tableRows(t) {
			cells := rowCells(tr)
			for i := 0; i+1 < len(cells); i++ {
				label := squash(cellText(cells[i]))
				if dst, ok := fields[label]; ok && !found[label] {
					*dst = cleanText(cellText(cells[i+1]))
					found[label] = true
				}
			}
		}
	}
	if !found["客戶編號"] || p.LegacyID == "" {
		return nil, errNotCustomerPage
	}

	// 2. 規格／欠桶數量／累計購買／銷售價格（同一張表，一欄一個規格）
	for _, t := range tables {
		rs := tableRows(t)
		var sizes []string
		for _, tr := range rs {
			cells := rowCells(tr)
			if len(cells) < 2 {
				continue
			}
			label := squash(cellText(cells[0]))
			if label == "規格" {
				sizes = nil
				for _, c := range cells[1:] {
					sizes = append(sizes, squash(cellText(c)))
				}
				continue
			}
			if sizes == nil {
				continue
			}
			var dst map[string]int
			switch label {
			case "欠桶數量":
				dst = p.Balance
			case "累計購買":
				dst = p.Totals
			case "銷售價格":
				dst = p.Prices
			default:
				continue
			}
			for i, c := range cells[1:] {
				if i >= len(sizes) || sizes[i] == "" {
					break
				}
				if n, ok := atoiLoose(cellText(c)); ok && n != 0 {
					dst[sizes[i]] = n
				}
			}
		}
		if sizes != nil && p.Sizes == nil {
			p.Sizes = sizes
		}
	}
	if p.Sizes == nil {
		p.Sizes = defaultLegacySizes
	}
	sizeSet := map[string]bool{}
	for _, s := range p.Sizes {
		sizeSet[s] = true
	}

	// 3. 送貨紀錄、出桶鋼瓶（先找標題列，再讀下面的資料列）
	// 多抓回來的其他頁可能沒有標題列，就沿用上一個送貨紀錄的標題。
	var lastHead map[string]int
	var lastHeads []string
	var lastSizeCols []int
	seenDelivery := map[string]bool{}
	for _, t := range tables {
		head, kind, heads, sizeCols := lastHead, "", lastHeads, lastSizeCols
		if head != nil {
			kind = "delivery"
		}
		for _, tr := range tableRows(t) {
			cells := rowCells(tr)
			texts := make([]string, len(cells))
			for i, c := range cells {
				texts[i] = squash(cellText(c))
			}
			if len(texts) >= 4 && texts[0] == "送貨日期" && texts[1] == "叫貨時間" {
				head, kind, heads, sizeCols = indexOf(texts), "delivery", texts, nil
				for i, h := range texts {
					if sizeSet[h] {
						sizeCols = append(sizeCols, i)
					}
				}
				lastHead, lastHeads, lastSizeCols = head, heads, sizeCols
				continue
			}
			if len(texts) >= 4 && contains(texts, "條碼") && contains(texts, "規格") && contains(texts, "編號") {
				head, kind, heads = indexOf(texts), "cylinder", texts
				continue
			}
			if head == nil || len(texts) < len(heads)-1 {
				continue
			}
			get := func(name string) string {
				if i, ok := head[name]; ok && i < len(cells) {
					return cleanText(cellText(cells[i]))
				}
				return ""
			}
			switch kind {
			case "delivery":
				day := legacyDate(texts[0])
				if day == "" {
					continue
				}
				d := LegacyDelivery{At: strings.TrimSpace(day + " " + legacyTime(get("叫貨時間")))}
				for _, i := range sizeCols {
					if i >= len(cells) {
						continue
					}
					if it, ok := parseLegacyItem(heads[i], cellText(cells[i])); ok {
						d.Items = append(d.Items, it)
					}
				}
				d.StoredGas, _ = atoiLoose(get("存氣"))
				d.Amount, _ = atoiLoose(get("金額"))
				if n, ok := atoiLoose(get("已付")); ok {
					d.Paid = &n
				}
				d.Cylinder = get("鋼瓶")
				d.PayStatus = get("是否付款")
				d.Driver = get("送貨員")
				d.Position = get("位置")
				d.Note = get("備註")
				// 同一筆在不同頁重複出現只算一次
				key := d.At
				if seenDelivery[key] {
					continue
				}
				seenDelivery[key] = true
				if sum, ok := itemsSum(d.Items); ok && sum != d.Amount && d.StoredGas == 0 {
					p.Warnings = append(p.Warnings, fmt.Sprintf("%s 金額 %d 跟 數量×單價 %d 不一樣", d.At[:10], d.Amount, sum))
				}
				p.Deliveries = append(p.Deliveries, d)
			case "cylinder":
				bc := get("條碼")
				if bc == "" {
					continue
				}
				c := LegacyCylinder{No: get("編號"), Barcode: bc, Size: squash(get("規格")),
					NextInspection: rocDate(get("下次檢驗")), Driver: get("司機")}
				if n, ok := atoiLoose(get("桶齡")); ok {
					c.AgeYears = &n
				}
				c.OutAt = strings.TrimSpace(legacyDate(get("日期")) + " " + legacyTime(get("時間")))
				p.CylindersOut = append(p.CylindersOut, c)
			}
		}
	}

	// 4. 整理
	// 有些頁面同一格裡編號會出現兩次（「87981 87981」），只留一個
	if f := strings.Fields(p.LegacyID); len(f) > 1 && f[0] == f[len(f)-1] {
		p.LegacyID = f[0]
	}
	// 有些頁面畫面上沒有「客戶電話」，但網頁程式裡藏著 customer_tel=0917...（換頁用的變數）
	if strings.TrimSpace(p.Phone) == "" {
		if m := legacyTelRe.FindStringSubmatch(src); m != nil {
			if v, err := url.QueryUnescape(m[1]); err == nil && strings.TrimSpace(v) != "" {
				p.Phone = v
				p.Warnings = append(p.Warnings, "畫面上沒有客戶電話，是從網頁裡藏的資料找到的，請看一下對不對")
			}
		}
	}
	p.Phone, p.Phone2 = splitPhones(p.Phone)
	p.Area = areaOf(p.Address)
	p.TaxID = strings.TrimSpace(p.TaxID)
	if p.TaxID != "" && !regexp.MustCompile(`^\d{8}$`).MatchString(p.TaxID) {
		p.Warnings = append(p.Warnings, "統一編號「"+p.TaxID+"」不是 8 碼數字，沒有存")
		p.TaxID = ""
	}
	switch inv := squash(p.Invoice); {
	case inv == "":
	case strings.Contains(inv, "無") || strings.Contains(inv, "不"):
		f := false
		p.NeedsInvoice = &f
	default:
		t := true
		p.NeedsInvoice = &t
	}
	if p.TaxID != "" {
		t := true
		p.NeedsInvoice = &t
	}
	if p.Name == "" {
		p.Warnings = append(p.Warnings, "沒讀到客戶名稱")
	}
	if p.Address == "" {
		p.Warnings = append(p.Warnings, "舊系統沒有地址（存檔時會先寫「舊系統沒有地址」，之後再補）")
	}
	return p, nil
}

// ---------------------------------------------------------------------
// HTML 小工具
// ---------------------------------------------------------------------

func findAll(n *html.Node, a atom.Atom) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == a {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// tableRows 只拿這張表自己的列（不含表中表）。
func tableRows(t *html.Node) []*html.Node {
	var out []*html.Node
	for c := t.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.DataAtom {
		case atom.Tr:
			out = append(out, c)
		case atom.Tbody, atom.Thead, atom.Tfoot:
			for r := c.FirstChild; r != nil; r = r.NextSibling {
				if r.Type == html.ElementNode && r.DataAtom == atom.Tr {
					out = append(out, r)
				}
			}
		}
	}
	return out
}

func rowCells(tr *html.Node) []*html.Node {
	var out []*html.Node
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
			out = append(out, c)
		}
	}
	return out
}

// cellText 格子裡看得到的字；輸入框用它的值；下拉選單、程式碼不算。
func cellText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Script, atom.Style, atom.Select, atom.Option, atom.Button, atom.Textarea:
				return
			case atom.Input:
				typ := strings.ToLower(attr(n, "type"))
				if typ == "" || typ == "text" || typ == "number" || typ == "tel" {
					b.WriteString(" " + attr(n, "value") + " ")
				}
				return
			case atom.Br:
				b.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

var spaceRe = regexp.MustCompile(`[\s\x{00a0}\x{3000}]+`)

// cleanText 前後空白去掉、中間連續空白變一個。
func cleanText(s string) string { return strings.TrimSpace(spaceRe.ReplaceAllString(s, " ")) }

// squash 把空白全部拿掉（「規 格」→「規格」）。
func squash(s string) string { return spaceRe.ReplaceAllString(s, "") }

func indexOf(texts []string) map[string]int {
	m := map[string]int{}
	for i, t := range texts {
		if _, ok := m[t]; !ok && t != "" {
			m[t] = i
		}
	}
	return m
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

var intRe = regexp.MustCompile(`-?\d[\d,]*`)

func atoiLoose(s string) (int, bool) {
	m := intRe.FindString(squash(s))
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m, ",", ""))
	return n, err == nil
}

var itemRe = regexp.MustCompile(`^(\d+)(?:\((\d+)\))?$`)

// parseLegacyItem 「1 (770)」→ 1 瓶、單價 770
func parseLegacyItem(size, s string) (LegacyItem, bool) {
	m := itemRe.FindStringSubmatch(squash(strings.NewReplacer("（", "(", "）", ")").Replace(s)))
	if m == nil {
		return LegacyItem{}, false
	}
	qty, _ := strconv.Atoi(m[1])
	if qty == 0 {
		return LegacyItem{}, false
	}
	it := LegacyItem{Size: size, Qty: qty}
	if m[2] != "" {
		n, _ := strconv.Atoi(m[2])
		it.Price = &n
	}
	return it, true
}

func itemsSum(items []LegacyItem) (int, bool) {
	sum := 0
	for _, it := range items {
		if it.Price == nil {
			return 0, false
		}
		sum += it.Qty * *it.Price
	}
	return sum, len(items) > 0
}

var dateRe = regexp.MustCompile(`(\d{4})[/-](\d{1,2})[/-](\d{1,2})`)

func legacyDate(s string) string {
	m := dateRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return fmt.Sprintf("%s-%02d-%02d", m[1], mo, d)
}

var timeRe = regexp.MustCompile(`\d{1,2}:\d{2}(:\d{2})?`)

func legacyTime(s string) string {
	t := timeRe.FindString(s)
	if len(t) == 4 || len(t) == 7 {
		t = "0" + t
	}
	return t
}

var rocRe = regexp.MustCompile(`^(\d{2,3})[-/.](\d{1,2})[-/.](\d{1,2})$`)

// rocDate 民國 114-06-21 → 2025-06-21；看不懂就照原字。
func rocDate(s string) string {
	s = squash(s)
	m := rocRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return fmt.Sprintf("%04d-%02d-%02d", y+1911, mo, d)
}

var phoneSplitRe = regexp.MustCompile(`[0-9][0-9\-()#]{5,}[0-9]`)

// splitPhones 電話欄可能有兩支（用、/ 空白分開）。
func splitPhones(s string) (string, string) {
	ms := phoneSplitRe.FindAllString(s, -1)
	clean := func(x string) string { return strings.TrimSpace(x) }
	switch len(ms) {
	case 0:
		return strings.TrimSpace(s), ""
	case 1:
		return clean(ms[0]), ""
	default:
		return clean(ms[0]), clean(ms[1])
	}
}

// areaOf 「桃園市中壢區後寮一路…」→「中壢區」；「平鎮區」不會被切成「平鎮」。
func areaOf(addr string) string {
	r := []rune(strings.TrimSpace(addr))
	start := 0
	if len(r) > 3 && (r[2] == '市' || r[2] == '縣') {
		start = 3
	}
	for _, mark := range []rune{'區', '鄉', '鎮', '市'} {
		for i := start + 1; i < len(r) && i <= start+3; i++ {
			if r[i] == mark {
				return string(r[start : i+1])
			}
		}
	}
	return ""
}

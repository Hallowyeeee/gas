# 瓦斯行管理系統

店裡一台 Windows 電腦執行 `gas.exe`：內含後端、SQLite 資料庫、網頁，並自動開 Cloudflare 通道讓手機在外面也能連。

- `cmd/gas/`：主程式
- `internal/app/`：後端 API（登入、客戶、商品、訂單、派單、送達回報、收款、押瓶、進貨、庫存、報表、備份、通道）
- `web/static/`：網頁
- `db/schema.sql`：資料庫結構；`db/test_schema.py` 檢查
- `docs/資料庫設計.md`、`docs/試跑教學.md`

開發：`go test ./...`、`python3 db/test_schema.py`、`go run ./cmd/gas -no-tunnel`
打包：推到 GitHub 後 Actions 會自動產生 Windows 版 `gas.exe` 放在 Releases「最新版」。

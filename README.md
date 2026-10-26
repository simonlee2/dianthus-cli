# Dianthus CLI + MCP server（禾馨醫療公開 API）

Go stdlib only，無外部依賴。唯讀工具：只打公開 GET 端點，不登入、不帶
token、不寫入任何資料。

## 建置

```sh
cd dianthus-cli
go build -o dianthus ./cmd/dianthus
go build -o dianthus-mcp ./cmd/dianthus-mcp
```

## CLI 用法

```
dianthus <command> [flags]
```

| 命令 | 說明 |
| --- | --- |
| `branches` | 院區＋診所清單 |
| `progress [--branch B] [--doctor D] [--clinic C]` | 即時診間進度（目前／下一位／等待人數），預設全部院區 |
| `doctors --department D` | 某科別的醫師清單（D 可為 id 或科別名） |
| `departments` | 科別清單 |
| `schedule [--start][--end][--doctor][--department][--clinic][--available-only]` | 掛號時刻表（預設今天～今天+6天，台北時間） |
| `clinic --code CODE` / `clinic --name NAME` | 單一診所詳情（二選一） |
| `districts` | 地區＋院區 |
| `location` | 診所地址電話 |
| `schema [command]` | 查詢各命令的參數與輸出 schema（永遠輸出 JSON） |

通用旗標（每個命令都接受）：`--json`、`--raw`、`--quiet`、`--stdin`、
`--fields F`。旗標一律放在命令之後。

```sh
# 人類可讀（預設）
dianthus progress --branch 民權 --doctor 羅佳怡

# 結構化 JSON（給 agent）
dianthus progress --branch 1 --doctor 羅佳怡 --json

# 單行 JSON，省 token
dianthus progress --branch 1 --doctor 羅佳怡 --raw

# 只取需要的欄位
dianthus progress --branch 1 --json --fields clinic_name,doctor_name,current_number,waiting_number

# stdin 傳參數（明確旗標優先）
echo '{"branch":1,"doctor":"羅佳怡"}' | dianthus progress --stdin --json

# 查詢 schema，不用試錯
dianthus schema progress
```

輸出契約：stdout 只放結果資料；錯誤與提示一律 stderr 的 JSON
（含 code、message、suggestion）；exit code：0 成功、1 上游錯誤、
2 用法錯誤（未知旗標、缺參數、值不合法）。詳見 [AGENTS.md](AGENTS.md)。

## MCP server

stdio JSON-RPC，工具與 CLI 同名同義：`branches`、`progress`、`doctors`、
`departments`、`schedule`、`clinic`、`districts`、`location`。

```json
{
  "mcpServers": {
    "dianthus": {
      "command": "/path/to/dianthus-mcp",
      "args": []
    }
  }
}
```

`tools/call` 回傳的 content 是 CLI `--json` 的同一份 envelope，
另附 `structuredContent`；工具層錯誤以 `isError: true` 回傳，
不以 JSON-RPC 錯誤掩蓋。

## API 清單（原始匯入文件記錄，未於本次發佈重新連線驗證）

Base：`https://api.dianthus.com.tw`。回應皆為
`{"status","message","data":{"data": ...}}`。

### 原始文件記錄的公開端點（免登入）

| 端點 | 說明 | 關鍵欄位 |
| --- | --- | --- |
| `GET /api/v1/hospital/branche-list` | 院區＋診所清單 | id、name、clinics[{id, code, name, phone_1, address}] |
| `GET /api/v1/hospital/branche/{id}` | 院區詳情 | id、name、intro（含看診時段） |
| `GET /api/v1/hospital/branche/{id}/clinic-progress` | 該院區即時診間進度 | clinic_name、vid、department_name、doctor_name、time_period、current_number、current_number_memo、next_number、waiting_number、nurse_name |
| `GET /api/v1/hospital/district` | 地區清單 | id、name |
| `GET /api/v1/hospital/district-branches` | 地區＋院區 | 同上，含 branches |
| `GET /api/v1/hospital/district/{id}/branches` | 單一地區＋院區 | 同上 |
| `GET /api/v1/hospital/department-list` | 科別清單 | id、name、icon_code |
| `GET /api/v1/hospital/doctor-list/{department_id}` | 某科醫師清單 | id、name、name_en、title、hospital_departments_id、photo、timetable、edu、exp、skill |
| `GET /api/v1/hospital/clinic/{code}` | 診所詳情（code 如 3501114186，**不是數字 id**；數字 id 會 404） | id、name、code、branch_name、address、phone_1、time_info |
| `GET /api/v1/hospital/location` | 診所位置清單 | name、address、phone_1、google_map_url |
| `GET /api/v1/online-registration/clinic-schedule?start_date&end_date` | 掛號時刻表 | date、department、vid、time_period、doctor_name、hospital_clinic_code、can_register、registered_number |

院區 id：1 民權、2 懷寧、3 士林、6 新生、8 桃園、11 安和、16 眼科。
未知院區的 clinic-progress 回 200 空陣列，不報錯。

### 需登入（本工具不碰）

| 端點 | 原因 |
| --- | --- |
| `PUT /api/v1/hospital/clinic-progress` | 院方更新叫號，寫入操作 |
| `PUT /api/v1/online-registration`、`PUT /api/v1/online-registration/cancel` | 掛號寫入／取消，需本人帳號 |
| `GET /api/v1/online-registration/record` | 個人掛號紀錄；未帶帳號時 500 |

### 院區／科別雙向查詢：已找到前端枚舉

`GET /api/v1/hospital/branche-department-ref` 的 `type` 不是時段，而是查詢方向：

| type（前端使用的大寫值） | 搭配參數 | 前端用途 |
| --- | --- | --- |
| `BRANCH` | `branch_id` | 取得該院區的科別；前端讀取結果首筆的 `departments` |
| `DEPARTMENT` | `department_id` | 取得包含該科別的院區；前端使用結果的院區 `id` |

例如 `?type=BRANCH&branch_id=1` 或 `?type=DEPARTMENT&department_id=2`。
這些值來自官方公開 JavaScript 的實際呼叫程式，不是猜測；尚未加入 CLI/MCP。
2026-10-26 直接驗證第一個範例時收到 HTTP 403，因此停止該驗證路徑，
未測第二個範例，也未宣稱目前可匿名直接呼叫。來源與限制見
[API type investigation](docs/api-types.md)。

### 仍未確認／未包裝

- `GET /api/v1/hospital/clinic-progress?branch_id&date&type`：目前公開 bundle
  只找到生成的 API 方法宣告，未找到實際呼叫或 `type` 的枚舉定義。
  不應把上面的 `BRANCH`／`DEPARTMENT` 自動套用到這支不同的端點。
  [官方公開看板](https://web.dianthus.com.tw/medical-progress) 使用
  `branche/{id}/clinic-progress`；CLI/MCP 繼續使用這個不需要 `type` 的路徑。
- `GET /api/v1/hospital/location-map`：原始文件記錄為 HTML 圖片內容，非結構化資料，未包進工具。

## 已知限制

- 即時數字是快照：叫號隨時在變，看到數字後要行動前請重查一次。
- `schedule` 一週可回 600+ 筆，務必用 `--doctor`／`--clinic`／
  `--available-only` 縮小範圍，或用 `--fields` 遮罩。
- 本工具不處理掛號、取消、報到：那些要登入本人帳號，不在本工具範圍。


## 驗證與相容性

本次發佈以 Go 1.27.3（macOS ARM64）建置 CLI 與 MCP，並執行
`go test ./...`、`go vet ./...` 及離線 CLI/MCP smoke checks。
測試使用合成 HTTP fixtures，不連線真實醫療 API；未驗證目前上游端點的
可用性，也未完成任何特定 MCP 客戶端的互通測試。

MCP 使用換行分隔的 stdio JSON-RPC，初始化固定回報協定版本
`2024-11-05`。請確認客戶端接受該版本；`structuredContent` 為額外輸出，
JSON 文字結果同時保留在 `content`。
MCP 的 branch、department、code 等 ID 參數是字串，例如
`{"branch":"1"}`；CLI 的 `--stdin` 另外接受數字型 ID。
MCP 的 `available_only` 對應 CLI 旗標 `--available-only`。
兩種介面的成功及錯誤 envelope 結構一致，但提示文字與結果排序可能不同。
錯誤 envelope 的 `error` 位於頂層，不含空白的成功資料或 metadata。

`progress` 逐一查詢院區；任一院區失敗時，整個呼叫回報上游錯誤，
不回傳先前已取得的部分結果。需要縮小失敗範圍時請指定單一院區。

## License

[MIT](LICENSE) — Copyright (c) 2026 Simon Lee.

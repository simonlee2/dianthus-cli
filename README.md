# Dianthus CLI + MCP server（禾馨醫療公開 API）

Go stdlib only，無外部依賴。唯讀工具：只打公開 GET 端點，不登入、不帶
token、不寫入任何資料。

## 建置

```sh
cd ~/workspace/dianthus
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

## API 清單（2026-10-26 實測）

Base：`https://api.dianthus.com.tw`。回應皆為
`{"status","message","data":{"data": ...}}`。

### 確定可用（免登入）

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

### 未解

- `GET /api/v1/hospital/clinic-progress?branch_id&date&type`：`type` 為必填枚舉，
  已試 morning、早診、1、am、clinic、門診、診間、看診、掛號、general、
  health_check、postpartum、S 皆 422「The selected type is invalid」。
  公開看板走 `branche/{id}/clinic-progress` 即可，不需要這支。
- `GET /api/v1/hospital/branche-department-ref`：同樣要求 `type`，值未解。
- `GET /api/v1/hospital/location-map`：回 HTML 圖片內容，非結構化資料，未包進工具。

## 已知限制

- 即時數字是快照：叫號隨時在變，看到數字後要行動前請重查一次。
- `schedule` 一週可回 600+ 筆，務必用 `--doctor`／`--clinic`／
  `--available-only` 縮小範圍，或用 `--fields` 遮罩。
- 本工具不處理掛號、取消、報到：那些要登入本人帳號，不在本工具範圍。

# AGENTS.md — Dianthus CLI / MCP（寫給 AI agent）

## 這是什麼

禾馨醫療公開 API 的唯讀工具。Go stdlib，無外部依賴。
**只能讀，不能掛號、取消、報到，也沒有任何帳號登入。**
掛號類操作需要使用者本人登入，不在本工具範圍。

## 怎麼用（優先順序）

1. 需要結構化資料時，一律加 `--json`；要省 token 時用 `--raw`
   （同一份 envelope，壓成單行）。
2. 欄位太多時加 `--fields a,b,c` 遮罩，只拿需要的欄位。
3. 不確定參數時先跑 `dianthus schema <command>`，不要試錯。
4. 組合命令時用 `--stdin` 餵 JSON：`echo '{"branch":1}' |
   dianthus progress --stdin --json`（明確旗標優先於 stdin）。

## 輸出契約

- **stdout 只放結果資料**，log 與錯誤一律 stderr；`--quiet` 保證
  stdout 只有結果（錯誤仍會印到 stderr，不會靜默失敗）。
- `--json`／`--raw` 輸出固定 envelope（`schemaVersion` 目前為 1，
  同一大版本內欄位與 error code 不會變）：

```json
{
  "schemaVersion": 1,
  "command": "progress",
  "ok": true,
  "data": [ { "clinic_name": "...", "doctor_name": "羅佳怡",
              "current_number": 4, "waiting_number": 11 } ],
  "meta": { "count": 1, "source": "https://...", "fetchedAt": "2026-10-26T10:00:00+08:00" }
}
```

- `data` 永遠是陣列（即使只有一筆，如 `clinic`）。
- 錯誤時 stderr 是 JSON，exit code 非零：

```json
{ "schemaVersion": 1, "ok": false,
  "error": { "code": "usage_error", "message": "...", "suggestion": "..." } }
```

- exit code：`0` 成功、`1` 上游 API 錯誤、`2` 用法錯誤
  （未知旗標、缺必填參數、值不合法、名稱對應到 0 或多筆）。
  **絕對不要互動式提問**：缺參數就直接報錯離開。
- 未知旗標直接拒絕（exit 2），不會默默接受。

## 常用 pattern

```sh
# 查某醫師現在叫到幾號（先確認院區，再盯診間）
dianthus branches --json --fields id,name
dianthus progress --branch 民權 --doctor 羅佳怡 --json \
  --fields clinic_name,vid,doctor_name,current_number,next_number,waiting_number

# 查某科醫師（department 可用 id 或科別名）
dianthus departments --json --fields id,name
dianthus doctors --department 小兒科 --json --fields id,name,title

# 查某醫師未來一週門診（schedule 預設今天～今天+6天，台北時間）
dianthus schedule --doctor 羅佳怡 --available-only --json

# 查診所資訊（code 優先；不知道 code 時用名稱解析）
dianthus clinic --code 3501114186 --json
dianthus clinic --name 小禾馨民權 --json
```

## 語意：讀完再決定

這是唯讀工具，設計上就是「讀完再決定」：你先用它查即時數字、
時刻表、診所資訊，整理成選項給使用者，**由使用者自己決定並執行**
掛號、報到、取消。不要把讀到的數字當成已預約，也不要替使用者
按任何寫入操作（本工具根本沒有寫入功能）。

## 容易踩的坑

- **診所 code ≠ 數字 id**：`clinic/{code}` 吃的是 `3501114186`
  這種代碼；拿 branche-list 裡的數字 id 去打會 404。
  不知道 code 時用 `clinic --name` 或 `branches` 查。
- **醫師清單吃科別 id**：`doctor-list/{department_id}`，例如小兒科是 2；
  `doctors --department 小兒科` 會自動解析，不用自己查 id。
- **`--branch` 不填 = 全部院區**：會打 7 支 API，數字量大，
  務必配 `--doctor`／`--clinic`／`--fields` 縮小。
- **叫號是快照**：`progress` 的數字下一秒就可能變，行動前重查。
- **`clinic-progress?branch_id&date&type` 這支不要用**：`type` 枚舉值
  未解（已試一輪皆 422）；公開看板走 `branche/{id}/clinic-progress`。
- **MCP 與 CLI 同義**：工具名、參數、envelope 完全一致；
  `tools/call` 失敗時看 `isError` 與回傳 envelope 內的 error，
  不是 JSON-RPC 錯誤。

// Command dianthus-mcp is an MCP server (stdio JSON-RPC) for the public
// Dianthus (禾馨醫療) API. It shares the read-only client in
// dianthus/internal/api and mirrors the CLI's tools: branches, progress,
// doctors, departments, schedule, clinic, districts, location.
//
// Protocol (minimal MCP over newline-delimited JSON-RPC 2.0):
//
//	initialize            -> protocolVersion, capabilities, serverInfo
//	notifications/initialized, notifications/cancelled -> no reply
//	ping                  -> {}
//	tools/list            -> tool definitions with inputSchema
//	tools/call            -> runs the tool; result content is the same JSON
//	                         envelope the CLI emits with --json, plus the
//	                         envelope as structuredContent
//	anything else         -> JSON-RPC error -32601 (method not found)
//
// Tool-level failures (bad arguments, upstream errors) are returned as
// isError results with the structured error envelope as text, not as
// JSON-RPC errors, so the client can surface them.
// Read-only: only public GET requests, no credentials, never a write.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"dianthus/internal/api"
)

const schemaVersion = 1
const protocolVersion = "2024-11-05"

// ---------- JSON-RPC plumbing ----------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func decodeID(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func isNotification(req rpcRequest) bool {
	return len(req.ID) == 0 || string(req.ID) == "null"
}

// ---------- envelope (same shape as the CLI's --json output) ----------

type errorBody struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

type envelope struct {
	SchemaVersion int    `json:"schemaVersion"`
	Command       string `json:"command"`
	OK            bool   `json:"ok"`
	Data          any    `json:"data"`
	Meta          meta   `json:"meta,omitempty"`
}

type meta struct {
	Count     int    `json:"count"`
	Source    string `json:"source"`
	FetchedAt string `json:"fetchedAt"`
}

type toolError struct {
	code       string
	msg        string
	suggestion string
}

func (e *toolError) Error() string { return e.msg }

func usageErr(msg, suggestion string) *toolError {
	return &toolError{code: "usage_error", msg: msg, suggestion: suggestion}
}

func upstreamErr(err error) *toolError {
	return &toolError{code: "upstream_error", msg: err.Error()}
}

func nowTaipei() string {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		loc = time.Local
	}
	return time.Now().In(loc).Format(time.RFC3339)
}

func okEnv(cmd string, data any, count int, source string) envelope {
	return envelope{SchemaVersion: schemaVersion, Command: cmd, OK: true,
		Data: data, Meta: meta{Count: count, Source: source, FetchedAt: nowTaipei()}}
}

func errEnv(e *toolError) envelope {
	// Reuse the envelope shape with ok:false; the error rides in meta-free
	// form so clients can parse it uniformly.
	return envelope{SchemaVersion: schemaVersion, OK: false,
		Data: map[string]any{"error": map[string]any{
			"code": e.code, "message": e.msg, "suggestion": e.suggestion}}}
}

// ---------- tool arguments ----------

type toolArgs struct {
	Branch        string `json:"branch"`
	Doctor        string `json:"doctor"`
	Clinic        string `json:"clinic"`
	Department    string `json:"department"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Start         string `json:"start"`
	End           string `json:"end"`
	AvailableOnly bool   `json:"available_only"`
	Fields        string `json:"fields"`
}

func parseArgs(raw json.RawMessage) (toolArgs, *toolError) {
	var a toolArgs
	if len(raw) == 0 || string(raw) == "null" {
		return a, nil
	}
	var obj struct {
		Arguments json.RawMessage `json:"arguments"`
	}
	// tools/call wraps arguments in params.arguments; accept both shapes.
	if err := json.Unmarshal(raw, &obj); err == nil && len(obj.Arguments) > 0 {
		raw = obj.Arguments
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, usageErr("arguments are not a JSON object: "+err.Error(),
			"pass an object, e.g. {\"branch\": 1, \"doctor\": \"羅佳怡\"}")
	}
	return a, nil
}

func fieldList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := []string{}
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------- tool implementations (shared logic with the CLI) ----------

func runBranches(c *api.Client, a toolArgs) (envelope, *toolError) {
	rows, src, err := c.Branches()
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	return okEnv("branches", api.Mask(rows, fieldList(a.Fields)), len(rows), src), nil
}

func runProgress(c *api.Client, a toolArgs) (envelope, *toolError) {
	branches, _, err := c.Branches()
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	var ids []int64
	names := map[int64]string{}
	if a.Branch != "" {
		id, candidates, err := api.ResolveBranchID(branches, a.Branch)
		if err != nil {
			return envelope{}, &toolError{code: "usage_error",
				msg:        fmt.Sprintf("branch %q did not match exactly one branch (%d matches)", a.Branch, len(candidates)),
				suggestion: "call branches and use an id, or a more specific name substring"}
		}
		ids = []int64{id}
	} else {
		for _, b := range branches {
			ids = append(ids, api.Num(b, "id"))
		}
	}
	for _, b := range branches {
		names[api.Num(b, "id")] = api.Str(b, "name")
	}
	all := []api.Row{}
	sources := []string{}
	for _, id := range ids {
		rows, src, err := c.Progress(id)
		if err != nil {
			return envelope{}, upstreamErr(err)
		}
		sources = append(sources, src)
		for _, r := range rows {
			r["branch_id"] = id
			r["branch_name"] = names[id]
			all = append(all, r)
		}
	}
	cond := map[string]string{}
	if a.Doctor != "" {
		cond["doctor_name"] = a.Doctor
	}
	if a.Clinic != "" {
		cond["clinic_name"] = a.Clinic
	}
	all = api.Filter(all, cond)
	return okEnv("progress", api.Mask(all, fieldList(a.Fields)), len(all), strings.Join(sources, ",")), nil
}

func runDoctors(c *api.Client, a toolArgs) (envelope, *toolError) {
	if a.Department == "" {
		return envelope{}, usageErr("department is required for doctors",
			"call departments first to list ids, e.g. department \"小兒科\"")
	}
	depts, _, err := c.Departments()
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	id, candidates, err := api.ResolveDepartmentID(depts, a.Department)
	if err != nil {
		return envelope{}, &toolError{code: "usage_error",
			msg:        fmt.Sprintf("department %q did not match exactly one department (%d matches)", a.Department, len(candidates)),
			suggestion: "call departments and use an id, or a more specific name substring"}
	}
	rows, src, err := c.Doctors(id)
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	return okEnv("doctors", api.Mask(rows, fieldList(a.Fields)), len(rows), src), nil
}

func runDepartments(c *api.Client, a toolArgs) (envelope, *toolError) {
	rows, src, err := c.Departments()
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	return okEnv("departments", api.Mask(rows, fieldList(a.Fields)), len(rows), src), nil
}

func runSchedule(c *api.Client, a toolArgs) (envelope, *toolError) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		loc = time.Local
	}
	start := a.Start
	if start == "" {
		start = time.Now().In(loc).Format("2006-01-02")
	}
	end := a.End
	if end == "" {
		end = time.Now().In(loc).AddDate(0, 0, 6).Format("2006-01-02")
	}
	sd, err := time.Parse("2006-01-02", start)
	if err != nil {
		return envelope{}, usageErr(fmt.Sprintf("start %q is not YYYY-MM-DD", start), "e.g. start \"2026-10-26\"")
	}
	ed, err := time.Parse("2006-01-02", end)
	if err != nil {
		return envelope{}, usageErr(fmt.Sprintf("end %q is not YYYY-MM-DD", end), "e.g. end \"2026-11-01\"")
	}
	if ed.Before(sd) {
		return envelope{}, usageErr(fmt.Sprintf("end %s is before start %s", end, start), "swap the dates")
	}
	clinicCode := a.Clinic
	var branches []api.Row
	if clinicCode != "" && !api.IsCode(clinicCode) {
		var err error
		branches, _, err = c.Branches()
		if err != nil {
			return envelope{}, upstreamErr(err)
		}
		code, candidates, err := api.ResolveClinicCode(api.BranchClinics(branches), clinicCode)
		if err != nil {
			return envelope{}, &toolError{code: "usage_error",
				msg:        fmt.Sprintf("clinic %q did not match exactly one clinic (%d matches)", clinicCode, len(candidates)),
				suggestion: "call branches and use a clinic code, or a more specific name substring"}
		}
		clinicCode = code
	}
	rows, src, err := c.Schedule(start, end)
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	cond := map[string]string{}
	if a.Doctor != "" {
		cond["doctor_name"] = a.Doctor
	}
	if a.Department != "" {
		cond["department"] = a.Department
	}
	if clinicCode != "" {
		cond["hospital_clinic_code"] = clinicCode
	}
	rows = api.Filter(rows, cond)
	if a.AvailableOnly {
		kept := []api.Row{}
		for _, r := range rows {
			if api.Str(r, "can_register") == "Y" {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	// Resolve clinic codes to names (non-fatal if branches fetch fails).
	if len(rows) > 0 {
		if branches == nil {
			if b, _, err := c.Branches(); err == nil {
				branches = b
			}
		}
		if branches != nil {
			rows = api.EnrichScheduleClinicNames(rows, api.BranchClinics(branches))
		}
	}
	return okEnv("schedule", api.Mask(rows, fieldList(a.Fields)), len(rows), src), nil
}

func runClinic(c *api.Client, a toolArgs) (envelope, *toolError) {
	if (a.Code == "") == (a.Name == "") {
		return envelope{}, usageErr("clinic needs exactly one of code or name",
			"e.g. code \"3501114186\", or name \"小禾馨民權\"")
	}
	code := a.Code
	if a.Name != "" {
		branches, _, err := c.Branches()
		if err != nil {
			return envelope{}, upstreamErr(err)
		}
		resolved, candidates, err := api.ResolveClinicCode(api.BranchClinics(branches), a.Name)
		if err != nil {
			return envelope{}, &toolError{code: "usage_error",
				msg:        fmt.Sprintf("clinic %q did not match exactly one clinic (%d matches)", a.Name, len(candidates)),
				suggestion: "call branches and use a clinic code, or a more specific name substring"}
		}
		code = resolved
	}
	row, src, err := c.ClinicByCode(code)
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	return okEnv("clinic", api.Mask([]api.Row{row}, fieldList(a.Fields)), 1, src), nil
}

func runDistricts(c *api.Client, a toolArgs) (envelope, *toolError) {
	rows, src, err := c.Districts()
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	return okEnv("districts", api.Mask(rows, fieldList(a.Fields)), len(rows), src), nil
}

func runLocation(c *api.Client, a toolArgs) (envelope, *toolError) {
	rows, src, err := c.Locations()
	if err != nil {
		return envelope{}, upstreamErr(err)
	}
	return okEnv("location", api.Mask(rows, fieldList(a.Fields)), len(rows), src), nil
}

// ---------- tool definitions ----------

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func schemaFor(props map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func toolList() []toolDef {
	fields := strProp("Comma-separated field mask for data rows (same as CLI --fields).")
	return []toolDef{
		{"branches", "List Dianthus branches and their clinics.",
			schemaFor(map[string]any{"fields": fields}, nil)},
		{"progress", "Live in-session rooms for a branch: current/next/waiting numbers. Default all branches.",
			schemaFor(map[string]any{
				"branch": strProp("Branch id or name substring; omit for all branches."),
				"doctor": strProp("Doctor name substring filter."),
				"clinic": strProp("Clinic name substring filter."),
				"fields": fields,
			}, nil)},
		{"doctors", "Doctors of one department.",
			schemaFor(map[string]any{
				"department": strProp("REQUIRED: department id or name substring (call departments first)."),
				"fields":     fields,
			}, []string{"department"})},
		{"departments", "List departments.",
			schemaFor(map[string]any{"fields": fields}, nil)},
		{"schedule", "Registration schedule between dates (defaults: today to today+6 days, Asia/Taipei).",
			schemaFor(map[string]any{
				"start":          strProp("YYYY-MM-DD, default today."),
				"end":            strProp("YYYY-MM-DD, default today + 6 days."),
				"doctor":         strProp("Doctor name substring filter."),
				"department":     strProp("Department name substring filter."),
				"clinic":         strProp("Clinic code, or clinic name substring."),
				"available_only": boolProp("Only rows with can_register = Y."),
				"fields":         fields,
			}, nil)},
		{"clinic", "One clinic's detail. Exactly one of code or name.",
			schemaFor(map[string]any{
				"code":   strProp("Clinic code, e.g. 3501114186."),
				"name":   strProp("Clinic name substring, unique match."),
				"fields": fields,
			}, nil)},
		{"districts", "Districts with their branches.",
			schemaFor(map[string]any{"fields": fields}, nil)},
		{"location", "Clinic addresses and phones.",
			schemaFor(map[string]any{"fields": fields}, nil)},
	}
}

var runners = map[string]func(*api.Client, toolArgs) (envelope, *toolError){
	"branches":    runBranches,
	"progress":    runProgress,
	"doctors":     runDoctors,
	"departments": runDepartments,
	"schedule":    runSchedule,
	"clinic":      runClinic,
	"districts":   runDistricts,
	"location":    runLocation,
}

// ---------- main loop ----------

func main() {
	c := api.New()
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	enc := json.NewEncoder(out)
	respond := func(id any, result any, rerr *rpcError) {
		resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rerr}
		enc.Encode(resp)
		out.Flush()
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			respond(nil, nil, &rpcError{Code: -32700, Message: "parse error: " + err.Error()})
			continue
		}
		if isNotification(req) {
			// notifications/initialized, notifications/cancelled: no reply.
			continue
		}
		id := decodeID(req.ID)
		switch req.Method {
		case "initialize":
			respond(id, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "dianthus-mcp", "version": "1.0.0"},
			}, nil)
		case "ping":
			respond(id, map[string]any{}, nil)
		case "tools/list":
			respond(id, map[string]any{"tools": toolList()}, nil)
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				respond(id, nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()})
				continue
			}
			runner, ok := runners[params.Name]
			if !ok {
				respond(id, nil, &rpcError{Code: -32602, Message: "unknown tool: " + params.Name})
				continue
			}
			args, terr := parseArgs(params.Arguments)
			if terr != nil {
				env := errEnv(terr)
				text, _ := json.Marshal(env)
				respond(id, map[string]any{
					"content":           []any{map[string]any{"type": "text", "text": string(text)}},
					"structuredContent": env,
					"isError":           true,
				}, nil)
				continue
			}
			env, terr := runner(c, args)
			if terr != nil {
				env = errEnv(terr)
				text, _ := json.Marshal(env)
				respond(id, map[string]any{
					"content":           []any{map[string]any{"type": "text", "text": string(text)}},
					"structuredContent": env,
					"isError":           true,
				}, nil)
				continue
			}
			text, _ := json.Marshal(env)
			respond(id, map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": string(text)}},
				"structuredContent": env,
			}, nil)
		default:
			respond(id, nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method})
		}
	}
}

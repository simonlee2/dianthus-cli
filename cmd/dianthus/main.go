// Command dianthus is an agent-first CLI for the public Dianthus (禾馨醫療) API.
//
// Agent-first contract (see AGENTS.md):
//   - Default output is human-readable; --json emits a structured JSON
//     envelope, --raw the same envelope compacted to one line.
//   - stdout carries only result data. All diagnostics and errors go to
//     stderr; --quiet suppresses informational stderr (errors always print).
//   - Never interactive: missing/invalid arguments produce a structured
//     JSON error on stderr and a non-zero exit (2 = usage, 1 = upstream).
//   - JSON output carries schemaVersion (currently 1) and keeps its shape
//     stable within a major version.
//   - --stdin accepts a JSON object of arguments (explicit flags win).
//   - --fields masks data rows to the requested fields.
//   - `dianthus schema [command]` introspects commands, flags, outputs.
//   - Read-only: this tool only issues public GET requests, never writes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"dianthus/internal/api"
)

const schemaVersion = 1

// ---------- output envelope ----------

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

type errEnvelope struct {
	SchemaVersion int       `json:"schemaVersion"`
	OK            bool      `json:"ok"`
	Error         errorBody `json:"error"`
}

// ---------- global options ----------

type globals struct {
	jsonOut bool
	raw     bool
	quiet   bool
	stdin   bool
	fields  string
}

func (g *globals) register(fs *flag.FlagSet) {
	fs.BoolVar(&g.jsonOut, "json", false, "output structured JSON envelope")
	fs.BoolVar(&g.raw, "raw", false, "output compact single-line JSON envelope (implies JSON)")
	fs.BoolVar(&g.quiet, "quiet", false, "suppress informational stderr; stdout stays result-only")
	fs.BoolVar(&g.stdin, "stdin", false, "read arguments as a JSON object from stdin (explicit flags win)")
	fs.StringVar(&g.fields, "fields", "", "comma-separated field mask for data rows")
}

func (g *globals) fieldList() []string {
	if strings.TrimSpace(g.fields) == "" {
		return nil
	}
	parts := strings.Split(g.fields, ",")
	out := []string{}
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------- errors ----------

type cliError struct {
	code       string
	msg        string
	suggestion string
	exitCode   int
}

func (e *cliError) Error() string { return e.msg }

func usageErr(msg, suggestion string) *cliError {
	return &cliError{code: "usage_error", msg: msg, suggestion: suggestion, exitCode: 2}
}

func upstreamErr(err error) *cliError {
	return &cliError{code: "upstream_error", msg: err.Error(), exitCode: 1}
}

func fail(e *cliError) {
	enc := json.NewEncoder(os.Stderr)
	enc.Encode(errEnvelope{SchemaVersion: schemaVersion, OK: false,
		Error: errorBody{Code: e.code, Message: e.msg, Suggestion: e.suggestion}})
	os.Exit(e.exitCode)
}

func logf(g *globals, format string, args ...any) {
	if g.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// ---------- output ----------

func nowTaipei() string {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		loc = time.Local
	}
	return time.Now().In(loc).Format(time.RFC3339)
}

func emit(g *globals, cmd string, data any, count int, source string) {
	if g.raw || g.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		if !g.raw {
			enc.SetIndent("", "  ")
		}
		enc.Encode(envelope{SchemaVersion: schemaVersion, Command: cmd, OK: true,
			Data: data, Meta: meta{Count: count, Source: source, FetchedAt: nowTaipei()}})
		return
	}
	printHuman(cmd, data, g.fieldList())
}

// printHuman renders rows (or a single row) as text. With a field mask it
// prints key=value lines; otherwise it uses the per-command pretty format.
func printHuman(cmd string, data any, fields []string) {
	rows, isList := toRows(data)
	if len(fields) > 0 {
		for _, r := range rows {
			parts := []string{}
			for _, f := range fields {
				if v, ok := r[f]; ok {
					parts = append(parts, f+"="+api.Str(r, f))
					_ = v
				}
			}
			fmt.Println(strings.Join(parts, " "))
		}
		return
	}
	switch cmd {
	case "branches":
		for _, b := range rows {
			fmt.Printf("%s %s\n", api.Str(b, "id"), api.Str(b, "name"))
			if clinics, ok := b["clinics"].([]any); ok {
				for _, ci := range clinics {
					cm, _ := ci.(map[string]any)
					if cm == nil {
						continue
					}
					fmt.Printf("  - %s (code %s)\n", api.Str(cm, "name"), api.Str(cm, "code"))
				}
			}
		}
	case "progress":
		for _, r := range rows {
			memo := api.Str(r, "current_number_memo")
			if memo != "" {
				memo = " [" + memo + "]"
			}
			fmt.Printf("%s %s %s %s：目前 %s 號，下一位 %s 號，等待 %s 人%s\n",
				api.Str(r, "branch_name"), api.Str(r, "clinic_name"), api.Str(r, "vid"),
				api.Str(r, "doctor_name"), api.Str(r, "current_number"),
				api.Str(r, "next_number"), api.Str(r, "waiting_number"), memo)
		}
	case "doctors":
		for _, r := range rows {
			fmt.Printf("%s %s %s %s\n", api.Str(r, "id"), api.Str(r, "name"),
				api.Str(r, "name_en"), api.Str(r, "title"))
		}
	case "departments":
		for _, r := range rows {
			fmt.Printf("%s %s\n", api.Str(r, "id"), api.Str(r, "name"))
		}
	case "schedule":
		for _, r := range rows {
			clinicName := api.Str(r, "clinic_name")
			if clinicName == "" {
				clinicName = api.Str(r, "hospital_clinic_code")
			}
			fmt.Printf("%s %s %s %s %s %s 可掛號 %s 已掛 %s\n",
				api.Str(r, "date"), api.Str(r, "department"), api.Str(r, "vid"),
				api.Str(r, "time_period"), api.Str(r, "doctor_name"),
				clinicName, api.Str(r, "can_register"),
				api.Str(r, "registered_number"))
		}
	case "clinic":
		if len(rows) == 1 {
			r := rows[0]
			fmt.Printf("%s (code %s)\n", api.Str(r, "name"), api.Str(r, "code"))
			fmt.Printf("院區：%s\n", api.Str(r, "branch_name"))
			fmt.Printf("電話：%s\n", api.Str(r, "phone_1"))
			fmt.Printf("地址：%s\n", api.Str(r, "address"))
			fmt.Printf("看診時段：%s\n", stripHTML(api.Str(r, "time_info")))
		}
	case "districts":
		for _, d := range rows {
			fmt.Printf("%s %s\n", api.Str(d, "id"), api.Str(d, "name"))
			if bs, ok := d["branches"].([]any); ok {
				for _, bi := range bs {
					bm, _ := bi.(map[string]any)
					if bm == nil {
						continue
					}
					fmt.Printf("  - %s %s\n", api.Str(bm, "id"), api.Str(bm, "name"))
				}
			}
		}
	case "location":
		for _, r := range rows {
			fmt.Printf("%s %s %s\n", api.Str(r, "name"), api.Str(r, "address"), api.Str(r, "phone_1"))
		}
	default:
		// Generic fallback: one JSON row per line.
		enc := json.NewEncoder(os.Stdout)
		for _, r := range rows {
			enc.Encode(r)
		}
	}
	_ = isList
}

func toRows(data any) ([]api.Row, bool) {
	switch v := data.(type) {
	case []api.Row:
		return v, true
	case api.Row:
		return []api.Row{v}, false
	default:
		return nil, false
	}
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	s = htmlTag.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	return strings.TrimSpace(s)
}

// ---------- flag parsing ----------

// parseFlags parses args with a FlagSet that never prints usage itself.
// Unknown flags, -h/--help, and trailing positionals are handled by the
// caller via the returned values.
func parseFlags(fs *flag.FlagSet, args []string) (help bool, err error) {
	fs.SetOutput(io.Discard)
	err = fs.Parse(args)
	if err == flag.ErrHelp {
		return true, nil
	}
	return false, err
}

func checkNoPositionals(fs *flag.FlagSet, cmd string) *cliError {
	if fs.NArg() > 0 {
		return usageErr(fmt.Sprintf("unexpected argument %q for %s", fs.Arg(0), cmd),
			"this command takes flags only; see `dianthus "+cmd+" --help`")
	}
	return nil
}

// applyStdin fills data arguments from a JSON object on stdin. Keys are flag
// names; a value is only applied when the flag was not explicitly set.
func applyStdin(fs *flag.FlagSet, g *globals, set map[string]*string, setBool map[string]*bool) *cliError {
	if !g.stdin {
		return nil
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return &cliError{code: "stdin_error", msg: "failed to read stdin: " + err.Error(), exitCode: 2}
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return &cliError{code: "stdin_error", msg: "--stdin given but stdin is empty",
			suggestion: `pipe a JSON object, e.g. echo '{"branch":1}' | dianthus progress --stdin --json`, exitCode: 2}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return &cliError{code: "stdin_error", msg: "stdin is not a JSON object: " + err.Error(), exitCode: 2}
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	for k, rawVal := range obj {
		if p, ok := set[k]; ok && !explicit[k] {
			var s string
			if err := json.Unmarshal(rawVal, &s); err == nil {
				*p = s
				continue
			}
			var f float64
			if err := json.Unmarshal(rawVal, &f); err == nil {
				if f == float64(int64(f)) {
					*p = fmt.Sprintf("%d", int64(f))
				} else {
					*p = fmt.Sprintf("%v", f)
				}
				continue
			}
			return &cliError{code: "stdin_error", msg: fmt.Sprintf("stdin field %q must be a string or number", k), exitCode: 2}
		}
		if p, ok := setBool[k]; ok && !explicit[k] {
			var b bool
			if err := json.Unmarshal(rawVal, &b); err == nil {
				*p = b
				continue
			}
			var s string
			if err := json.Unmarshal(rawVal, &s); err == nil && (s == "true" || s == "false") {
				*p = s == "true"
				continue
			}
			return &cliError{code: "stdin_error", msg: fmt.Sprintf("stdin field %q must be a boolean", k), exitCode: 2}
		}
		// Unknown stdin keys are ignored: stdin is input, not a schema.
	}
	return nil
}

// ---------- command help ----------

func helpText(cmd string) string {
	common := `  --json        structured JSON envelope on stdout
  --raw         compact single-line JSON envelope (implies JSON)
  --quiet       suppress informational stderr
  --stdin       read arguments as a JSON object from stdin (explicit flags win)
  --fields F    comma-separated field mask for data rows
`
	switch cmd {
	case "branches":
		return "dianthus branches — list branches and their clinics\n\nflags:\n" + common
	case "progress":
		return `dianthus progress — live in-session rooms (current/next/waiting numbers)

flags:
  --branch B    branch id or name substring (default: all branches)
  --doctor D    doctor name substring filter
  --clinic C    clinic name substring filter
` + common
	case "doctors":
		return `dianthus doctors — doctors of one department

flags:
  --department D   REQUIRED: department id or name substring
                   (run 'dianthus departments' to list ids)
` + common
	case "departments":
		return "dianthus departments — list departments\n\nflags:\n" + common
	case "schedule":
		return `dianthus schedule — registration schedule between dates

flags:
  --start DATE    YYYY-MM-DD (default: today, Asia/Taipei)
  --end DATE      YYYY-MM-DD (default: today + 6 days)
  --doctor D      doctor name substring filter
  --department D  department name substring filter
  --clinic C      clinic code, or clinic name substring (resolved via branches)
  --available-only  only rows with can_register = Y
` + common
	case "clinic":
		return `dianthus clinic — one clinic's detail

flags:
  --code CODE     clinic code, e.g. 3501114186 (exactly one of --code/--name)
  --name NAME     clinic name substring (resolved via branches, unique match)
` + common
	case "districts":
		return "dianthus districts — districts with their branches\n\nflags:\n" + common
	case "location":
		return "dianthus location — clinic addresses and phones\n\nflags:\n" + common
	case "schema":
		return `dianthus schema [command] — introspect commands, flags, output fields

With no argument, prints every command's schema as JSON.
With a command name, prints only that command's schema.
Output is always JSON (pretty by default, compact with --raw).
`
	default:
		return "dianthus — read-only CLI for the public Dianthus (禾馨醫療) API\n\ncommands:\n  branches     list branches and their clinics\n  progress     live in-session rooms (current/next/waiting numbers)\n  doctors      doctors of one department\n  departments  list departments\n  schedule     registration schedule between dates\n  clinic       one clinic's detail\n  districts    districts with their branches\n  location     clinic addresses and phones\n  schema       introspect commands, flags, output fields\n\nrun `dianthus <command> --help` for a command's flags.\n"
	}
}

// ---------- commands ----------

func cmdBranches(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("branches", flag.ContinueOnError)
	g.register(fs)
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("branches"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for branches: "+err.Error(), "see `dianthus branches --help`")
	}
	if ce := checkNoPositionals(fs, "branches"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"fields": &g.fields}, nil); ce != nil {
		return ce
	}
	rows, src, err := c.Branches()
	if err != nil {
		return upstreamErr(err)
	}
	emit(g, "branches", api.Mask(rows, g.fieldList()), len(rows), src)
	return nil
}

func cmdProgress(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("progress", flag.ContinueOnError)
	g.register(fs)
	var branch, doctor, clinic string
	fs.StringVar(&branch, "branch", "", "branch id or name substring (default: all)")
	fs.StringVar(&doctor, "doctor", "", "doctor name substring filter")
	fs.StringVar(&clinic, "clinic", "", "clinic name substring filter")
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("progress"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for progress: "+err.Error(), "see `dianthus progress --help`")
	}
	if ce := checkNoPositionals(fs, "progress"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"branch": &branch, "doctor": &doctor, "clinic": &clinic, "fields": &g.fields}, nil); ce != nil {
		return ce
	}
	branches, _, err := c.Branches()
	if err != nil {
		return upstreamErr(err)
	}
	var ids []int64
	var names map[int64]string
	names = map[int64]string{}
	if branch != "" {
		id, candidates, err := api.ResolveBranchID(branches, branch)
		if err != nil {
			return &cliError{code: "usage_error",
				msg:        fmt.Sprintf("branch %q did not match exactly one branch (%d matches)", branch, len(candidates)),
				suggestion: "run `dianthus branches` and use an id, or a more specific name substring",
				exitCode:   2}
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
			return upstreamErr(err)
		}
		sources = append(sources, src)
		for _, r := range rows {
			r["branch_id"] = id
			r["branch_name"] = names[id]
			all = append(all, r)
		}
	}
	cond := map[string]string{}
	if doctor != "" {
		cond["doctor_name"] = doctor
	}
	if clinic != "" {
		cond["clinic_name"] = clinic
	}
	all = api.Filter(all, cond)
	sort.Slice(all, func(i, j int) bool {
		if api.Str(all[i], "branch_name") != api.Str(all[j], "branch_name") {
			return api.Str(all[i], "branch_name") < api.Str(all[j], "branch_name")
		}
		return api.Str(all[i], "clinic_name") < api.Str(all[j], "clinic_name")
	})
	emit(g, "progress", api.Mask(all, g.fieldList()), len(all), strings.Join(sources, ","))
	return nil
}

func cmdDoctors(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("doctors", flag.ContinueOnError)
	g.register(fs)
	var dept string
	fs.StringVar(&dept, "department", "", "REQUIRED: department id or name substring")
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("doctors"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for doctors: "+err.Error(), "see `dianthus doctors --help`")
	}
	if ce := checkNoPositionals(fs, "doctors"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"department": &dept, "fields": &g.fields}, nil); ce != nil {
		return ce
	}
	if dept == "" {
		return usageErr("--department is required for doctors",
			"run `dianthus departments` to list ids, e.g. dianthus doctors --department 小兒科")
	}
	depts, _, err := c.Departments()
	if err != nil {
		return upstreamErr(err)
	}
	id, candidates, err := api.ResolveDepartmentID(depts, dept)
	if err != nil {
		return &cliError{code: "usage_error",
			msg:        fmt.Sprintf("department %q did not match exactly one department (%d matches)", dept, len(candidates)),
			suggestion: "run `dianthus departments` and use an id, or a more specific name substring",
			exitCode:   2}
	}
	rows, src, err := c.Doctors(id)
	if err != nil {
		return upstreamErr(err)
	}
	emit(g, "doctors", api.Mask(rows, g.fieldList()), len(rows), src)
	return nil
}

func cmdDepartments(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("departments", flag.ContinueOnError)
	g.register(fs)
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("departments"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for departments: "+err.Error(), "see `dianthus departments --help`")
	}
	if ce := checkNoPositionals(fs, "departments"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"fields": &g.fields}, nil); ce != nil {
		return ce
	}
	rows, src, err := c.Departments()
	if err != nil {
		return upstreamErr(err)
	}
	emit(g, "departments", api.Mask(rows, g.fieldList()), len(rows), src)
	return nil
}

func cmdSchedule(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("schedule", flag.ContinueOnError)
	g.register(fs)
	var start, end, doctor, dept, clinic string
	var availableOnly bool
	fs.StringVar(&start, "start", "", "YYYY-MM-DD (default: today, Asia/Taipei)")
	fs.StringVar(&end, "end", "", "YYYY-MM-DD (default: today + 6 days)")
	fs.StringVar(&doctor, "doctor", "", "doctor name substring filter")
	fs.StringVar(&dept, "department", "", "department name substring filter")
	fs.StringVar(&clinic, "clinic", "", "clinic code or clinic name substring")
	fs.BoolVar(&availableOnly, "available-only", false, "only rows with can_register = Y")
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("schedule"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for schedule: "+err.Error(), "see `dianthus schedule --help`")
	}
	if ce := checkNoPositionals(fs, "schedule"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g,
		map[string]*string{"start": &start, "end": &end, "doctor": &doctor, "department": &dept, "clinic": &clinic, "fields": &g.fields},
		map[string]*bool{"available_only": &availableOnly}); ce != nil {
		return ce
	}
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		loc = time.Local
	}
	today := time.Now().In(loc).Format("2006-01-02")
	if start == "" {
		start = today
	}
	if end == "" {
		end = time.Now().In(loc).AddDate(0, 0, 6).Format("2006-01-02")
	}
	sd, err := time.Parse("2006-01-02", start)
	if err != nil {
		return usageErr(fmt.Sprintf("--start %q is not YYYY-MM-DD", start), "e.g. --start 2026-10-26")
	}
	ed, err := time.Parse("2006-01-02", end)
	if err != nil {
		return usageErr(fmt.Sprintf("--end %q is not YYYY-MM-DD", end), "e.g. --end 2026-11-01")
	}
	if ed.Before(sd) {
		return usageErr(fmt.Sprintf("--end %s is before --start %s", end, start), "swap the dates")
	}
	// Resolve a clinic name to its code (a code passes through untouched).
	clinicCode := clinic
	var branches []api.Row
	if clinic != "" && !api.IsCode(clinic) {
		var err error
		branches, _, err = c.Branches()
		if err != nil {
			return upstreamErr(err)
		}
		code, candidates, err := api.ResolveClinicCode(api.BranchClinics(branches), clinic)
		if err != nil {
			return &cliError{code: "usage_error",
				msg:        fmt.Sprintf("clinic %q did not match exactly one clinic (%d matches)", clinic, len(candidates)),
				suggestion: "run `dianthus branches` and use a clinic code, or a more specific name substring",
				exitCode:   2}
		}
		clinicCode = code
	}
	rows, src, err := c.Schedule(start, end)
	if err != nil {
		return upstreamErr(err)
	}
	cond := map[string]string{}
	if doctor != "" {
		cond["doctor_name"] = doctor
	}
	if dept != "" {
		cond["department"] = dept
	}
	if clinicCode != "" {
		cond["hospital_clinic_code"] = clinicCode
	}
	rows = api.Filter(rows, cond)
	if availableOnly {
		kept := []api.Row{}
		for _, r := range rows {
			if api.Str(r, "can_register") == "Y" {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	// Resolve clinic codes to names so human output shows names, not codes.
	// A branches fetch failure is non-fatal: rows keep clinic_name = code.
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
	emit(g, "schedule", api.Mask(rows, g.fieldList()), len(rows), src)
	return nil
}

func cmdClinic(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("clinic", flag.ContinueOnError)
	g.register(fs)
	var code, name string
	fs.StringVar(&code, "code", "", "clinic code, e.g. 3501114186")
	fs.StringVar(&name, "name", "", "clinic name substring (unique match)")
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("clinic"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for clinic: "+err.Error(), "see `dianthus clinic --help`")
	}
	if ce := checkNoPositionals(fs, "clinic"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"code": &code, "name": &name, "fields": &g.fields}, nil); ce != nil {
		return ce
	}
	if (code == "") == (name == "") {
		return usageErr("clinic needs exactly one of --code or --name",
			"e.g. dianthus clinic --code 3501114186, or dianthus clinic --name 小禾馨民權")
	}
	if name != "" {
		branches, _, err := c.Branches()
		if err != nil {
			return upstreamErr(err)
		}
		resolved, candidates, err := api.ResolveClinicCode(api.BranchClinics(branches), name)
		if err != nil {
			return &cliError{code: "usage_error",
				msg:        fmt.Sprintf("clinic %q did not match exactly one clinic (%d matches)", name, len(candidates)),
				suggestion: "run `dianthus branches` and use a clinic code, or a more specific name substring",
				exitCode:   2}
		}
		code = resolved
	}
	row, src, err := c.ClinicByCode(code)
	if err != nil {
		return upstreamErr(err)
	}
	rows := []api.Row{row}
	emit(g, "clinic", api.Mask(rows, g.fieldList()), 1, src)
	return nil
}

func cmdDistricts(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("districts", flag.ContinueOnError)
	g.register(fs)
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("districts"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for districts: "+err.Error(), "see `dianthus districts --help`")
	}
	if ce := checkNoPositionals(fs, "districts"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"fields": &g.fields}, nil); ce != nil {
		return ce
	}
	rows, src, err := c.Districts()
	if err != nil {
		return upstreamErr(err)
	}
	emit(g, "districts", api.Mask(rows, g.fieldList()), len(rows), src)
	return nil
}

func cmdLocation(c *api.Client, g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("location", flag.ContinueOnError)
	g.register(fs)
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("location"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for location: "+err.Error(), "see `dianthus location --help`")
	}
	if ce := checkNoPositionals(fs, "location"); ce != nil {
		return ce
	}
	if ce := applyStdin(fs, g, map[string]*string{"fields": &g.fields}, nil); ce != nil {
		return ce
	}
	rows, src, err := c.Locations()
	if err != nil {
		return upstreamErr(err)
	}
	emit(g, "location", api.Mask(rows, g.fieldList()), len(rows), src)
	return nil
}

// ---------- schema (introspection) ----------

type flagSchema struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

type commandSchema struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Flags       []flagSchema `json:"flags"`
	Output      []string     `json:"outputFields"`
}

var globalFlags = []flagSchema{
	{"json", "bool", false, "structured JSON envelope on stdout"},
	{"raw", "bool", false, "compact single-line JSON envelope (implies JSON)"},
	{"quiet", "bool", false, "suppress informational stderr"},
	{"stdin", "bool", false, "read arguments as a JSON object from stdin (explicit flags win)"},
	{"fields", "string", false, "comma-separated field mask for data rows"},
}

func schemaTable() map[string]commandSchema {
	mk := func(name, desc string, extra []flagSchema, out []string) commandSchema {
		flags := append(append([]flagSchema{}, globalFlags...), extra...)
		return commandSchema{Name: name, Description: desc, Flags: flags, Output: out}
	}
	return map[string]commandSchema{
		"branches": mk("branches", "List branches and their clinics", nil,
			[]string{"id", "name", "color", "intro", "weight", "clinics"}),
		"progress": mk("progress", "Live in-session rooms (current/next/waiting numbers)", []flagSchema{
			{"branch", "string", false, "branch id or name substring; default all branches"},
			{"doctor", "string", false, "doctor name substring filter"},
			{"clinic", "string", false, "clinic name substring filter"},
		}, []string{"branch_id", "branch_name", "id", "clinic_name", "vid", "department_name", "doctor_name", "time_period", "current_number", "current_number_memo", "next_number", "waiting_number", "nurse_name", "color"}),
		"doctors": mk("doctors", "Doctors of one department", []flagSchema{
			{"department", "string", true, "department id or name substring"},
		}, []string{"id", "name", "name_en", "title", "title_en", "hospital_departments_id", "photo", "timetable", "edu", "exp", "exp2", "serve", "skill", "book", "cert", "cert2", "say"}),
		"departments": mk("departments", "List departments", nil,
			[]string{"id", "name", "icon_code", "mapping_keywords", "intro"}),
		"schedule": mk("schedule", "Registration schedule between dates", []flagSchema{
			{"start", "string", false, "YYYY-MM-DD, default today (Asia/Taipei)"},
			{"end", "string", false, "YYYY-MM-DD, default today + 6 days"},
			{"doctor", "string", false, "doctor name substring filter"},
			{"department", "string", false, "department name substring filter"},
			{"clinic", "string", false, "clinic code or clinic name substring"},
			{"available_only", "bool", false, "only rows with can_register = Y (stdin key; CLI flag is --available-only)"},
		}, []string{"branch_id", "can_first_diagnosis", "can_register", "clinic_name", "date", "department", "doctor_code", "doctor_name", "floor_code", "hospital_clinic_code", "limit", "manufacturer", "medical_treatment_number", "original_schedule_code", "phone_1", "phone_2", "registered_number", "remark", "schedule_code", "special_handle", "substitute_doctor_code", "substitute_doctor_name", "time_period", "time_period_code", "timestamp", "vid", "vid_code"}),
		"clinic": mk("clinic", "One clinic's detail", []flagSchema{
			{"code", "string", false, "clinic code, e.g. 3501114186 (exactly one of --code/--name)"},
			{"name", "string", false, "clinic name substring, unique match (exactly one of --code/--name)"},
		}, []string{"id", "name", "code", "branch_id", "branch_name", "address", "phone_1", "phone_2", "time_info", "traffic_bus", "traffic_drive", "traffic_metro", "parking_location", "google_map_url", "departments", "manufacturer", "weight"}),
		"districts": mk("districts", "Districts with their branches", nil,
			[]string{"id", "name", "weight", "branches"}),
		"location": mk("location", "Clinic addresses and phones", nil,
			[]string{"id", "name", "address", "phone_1", "phone_2", "google_map_url", "web_url", "weight"}),
	}
}

func cmdSchema(g *globals, args []string) *cliError {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	g.register(fs)
	help, err := parseFlags(fs, args)
	if help {
		fmt.Print(helpText("schema"))
		return nil
	}
	if err != nil {
		return usageErr("invalid flags for schema: "+err.Error(), "see `dianthus schema --help`")
	}
	if fs.NArg() > 1 {
		return usageErr("schema takes at most one argument (a command name)", "e.g. dianthus schema progress")
	}
	table := schemaTable()
	if fs.NArg() == 1 {
		name := fs.Arg(0)
		cs, ok := table[name]
		if !ok {
			names := []string{}
			for k := range table {
				names = append(names, k)
			}
			sort.Strings(names)
			return usageErr(fmt.Sprintf("unknown command %q for schema", name),
				"known commands: "+strings.Join(names, ", "))
		}
		out := map[string]any{"schemaVersion": schemaVersion, "command": cs}
		emitJSON(g, out)
		return nil
	}
	names := []string{}
	for k := range table {
		names = append(names, k)
	}
	sort.Strings(names)
	cmds := []commandSchema{}
	for _, n := range names {
		cmds = append(cmds, table[n])
	}
	out := map[string]any{"schemaVersion": schemaVersion, "commands": cmds}
	emitJSON(g, out)
	return nil
}

// emitJSON always writes JSON (schema is machine introspection by nature).
func emitJSON(g *globals, v any) {
	enc := json.NewEncoder(os.Stdout)
	if !g.raw {
		enc.SetIndent("", "  ")
	}
	enc.Encode(v)
}

// ---------- dispatch ----------

func main() {
	log := &globals{}
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Print(helpText(""))
		return
	}
	cmd, rest := args[0], args[1:]
	c := api.New()
	var ce *cliError
	switch cmd {
	case "branches":
		ce = cmdBranches(c, log, rest)
	case "progress":
		ce = cmdProgress(c, log, rest)
	case "doctors":
		ce = cmdDoctors(c, log, rest)
	case "departments":
		ce = cmdDepartments(c, log, rest)
	case "schedule":
		ce = cmdSchedule(c, log, rest)
	case "clinic":
		ce = cmdClinic(c, log, rest)
	case "districts":
		ce = cmdDistricts(c, log, rest)
	case "location":
		ce = cmdLocation(c, log, rest)
	case "schema":
		ce = cmdSchema(log, rest)
	default:
		ce = usageErr(fmt.Sprintf("unknown command %q", cmd),
			"known commands: branches, progress, doctors, departments, schedule, clinic, districts, location, schema")
	}
	if ce != nil {
		fail(ce)
	}
}

// Package api is a read-only client for the public Dianthus (禾馨醫療)
// API. It uses only the Go standard library, sends no credentials, and
// never calls a mutating endpoint.
//
// Verified public GET endpoints (base https://api.dianthus.com.tw):
//   - /api/v1/hospital/branche-list
//   - /api/v1/hospital/branche/{id}
//   - /api/v1/hospital/branche/{id}/clinic-progress
//   - /api/v1/hospital/district
//   - /api/v1/hospital/district-branches
//   - /api/v1/hospital/district/{id}/branches
//   - /api/v1/hospital/department-list
//   - /api/v1/hospital/doctor-list/{department_id}
//   - /api/v1/hospital/clinic/{code}   (code, e.g. 3501114186, not the numeric id)
//   - /api/v1/hospital/location
//   - /api/v1/online-registration/clinic-schedule?start_date&end_date
//
// The API wraps every response as {"status","message","data":{"data": ...}}.
// Rows are decoded as generic maps so the --fields mask and JSON output can
// pass fields through without a per-endpoint struct.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Base is the public API host. It is intentionally not configurable:
// this tool only ever talks to the public Dianthus API.
const Base = "https://api.dianthus.com.tw"

// Client is a thin HTTP client for the Dianthus API.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with a 15s timeout and a descriptive User-Agent.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

type envelope struct {
	Status    int             `json:"status"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	ErrorCode string          `json:"error_code"`
}

// Row is one decoded record (object) from an API list/detail response.
type Row map[string]any

// Get fetches path (with query) and returns the inner data payload decoded
// as a generic value: []Row for lists, Row for single objects.
func (c *Client) Get(path string, query url.Values) (any, string, error) {
	u := Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, u, err
	}
	req.Header.Set("User-Agent", "dianthus-cli/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, u, fmt.Errorf("dianthus api request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, u, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, u, fmt.Errorf("dianthus api http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, u, fmt.Errorf("dianthus api bad json: %w", err)
	}
	if env.Status != 200 {
		return nil, u, fmt.Errorf("dianthus api status %d: %s", env.Status, env.Message)
	}
	var inner struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(env.Data, &inner); err != nil {
		return nil, u, fmt.Errorf("dianthus api bad data envelope: %w", err)
	}
	// Try a list of objects first, then a single object.
	var rows []Row
	if err := json.Unmarshal(inner.Data, &rows); err == nil {
		if rows == nil {
			rows = []Row{}
		}
		return rows, u, nil
	}
	var row Row
	if err := json.Unmarshal(inner.Data, &row); err != nil {
		return nil, u, fmt.Errorf("dianthus api unexpected data shape")
	}
	return row, u, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- typed accessors over generic rows ----

// IsCode reports whether s looks like a clinic code: all digits, no sign
// or whitespace. Clinic codes (e.g. 3501114186) are numeric strings, while
// clinic names are not.
func IsCode(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Str returns the row's value for key as a string ("" when absent).
func Str(r Row, key string) string {
	switch v := r[key].(type) {
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

// Num returns the row's value for key as an int64 (-1 when absent/non-numeric).
func Num(r Row, key string) int64 {
	switch v := r[key].(type) {
	case float64:
		return int64(v)
	case string:
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return -1
}

// Has reports whether key exists and is non-empty.
func Has(r Row, key string) bool {
	v, ok := r[key]
	if !ok || v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return s != ""
	}
	return true
}

// ContainsFold reports whether hay contains needle (case-insensitive for
// Latin text; Chinese substring match is exact by nature).
func ContainsFold(hay, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// Filter returns rows where all given key/substring conditions match.
func Filter(rows []Row, cond map[string]string) []Row {
	if len(cond) == 0 {
		return rows
	}
	out := []Row{}
	for _, r := range rows {
		ok := true
		for k, needle := range cond {
			if !ContainsFold(Str(r, k), needle) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

// Mask returns rows keeping only the requested fields (in request order).
// A requested field absent from a row is simply omitted from that row.
func Mask(rows []Row, fields []string) []Row {
	if len(fields) == 0 {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		m := Row{}
		for _, f := range fields {
			if v, ok := r[f]; ok {
				m[f] = v
			}
		}
		out = append(out, m)
	}
	return out
}

// ---- endpoint operations (each returns rows + source URL) ----

// Branches returns the branch list; each branch carries a nested "clinics" list.
func (c *Client) Branches() ([]Row, string, error) {
	v, u, err := c.Get("/api/v1/hospital/branche-list", nil)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

// BranchClinics flattens the branch list into clinic rows, each annotated
// with its branch id and name.
func BranchClinics(branches []Row) []Row {
	out := []Row{}
	for _, b := range branches {
		clinics, _ := b["clinics"].([]any)
		for _, ci := range clinics {
			cm, _ := ci.(map[string]any)
			if cm == nil {
				continue
			}
			row := Row{}
			for k, v := range cm {
				row[k] = v
			}
			row["branch_id"] = b["id"]
			row["branch_name"] = b["name"]
			out = append(out, row)
		}
	}
	return out
}

// EnrichScheduleClinicNames resolves each schedule row's hospital_clinic_code
// to a human-readable clinic_name using the branches list. Rows whose code
// has no match keep clinic_name equal to the code, so callers always get a
// displayable name. It never mutates the input rows.
func EnrichScheduleClinicNames(rows []Row, clinics []Row) []Row {
	byCode := map[string]string{}
	for _, cm := range clinics {
		if code := Str(cm, "code"); code != "" {
			byCode[code] = Str(cm, "name")
		}
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		nr := Row{}
		for k, v := range r {
			nr[k] = v
		}
		code := Str(r, "hospital_clinic_code")
		if name, ok := byCode[code]; ok && name != "" {
			nr["clinic_name"] = name
		} else {
			nr["clinic_name"] = code
		}
		out = append(out, nr)
	}
	return out
}

// ResolveBranchID maps a --branch value to a numeric branch id: a numeric
// value is used as-is; otherwise it must uniquely substring-match a branch
// name. It returns (id, candidates, error); candidates is non-empty when the
// name matched zero or several branches.
func ResolveBranchID(branches []Row, value string) (int64, []Row, error) {
	if value == "" {
		return 0, nil, nil
	}
	var n int64
	if _, err := fmt.Sscanf(value, "%d", &n); err == nil && fmt.Sprintf("%d", n) == strings.TrimSpace(value) {
		return n, nil, nil
	}
	matched := []Row{}
	for _, b := range branches {
		if ContainsFold(Str(b, "name"), value) {
			matched = append(matched, b)
		}
	}
	if len(matched) == 1 {
		return Num(matched[0], "id"), nil, nil
	}
	return 0, matched, fmt.Errorf("branch %q matched %d branches", value, len(matched))
}

// Progress returns the live in-session rooms for one branch id.
func (c *Client) Progress(branchID int64) ([]Row, string, error) {
	v, u, err := c.Get(fmt.Sprintf("/api/v1/hospital/branche/%d/clinic-progress", branchID), nil)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

// Departments returns the department list.
func (c *Client) Departments() ([]Row, string, error) {
	v, u, err := c.Get("/api/v1/hospital/department-list", nil)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

// ResolveDepartmentID maps a --department value to a numeric department id,
// with the same rules as ResolveBranchID.
func ResolveDepartmentID(depts []Row, value string) (int64, []Row, error) {
	if value == "" {
		return 0, nil, nil
	}
	var n int64
	if _, err := fmt.Sscanf(value, "%d", &n); err == nil && fmt.Sprintf("%d", n) == strings.TrimSpace(value) {
		return n, nil, nil
	}
	matched := []Row{}
	for _, d := range depts {
		if ContainsFold(Str(d, "name"), value) {
			matched = append(matched, d)
		}
	}
	if len(matched) == 1 {
		return Num(matched[0], "id"), nil, nil
	}
	return 0, matched, fmt.Errorf("department %q matched %d departments", value, len(matched))
}

// Doctors returns doctors for one department id.
func (c *Client) Doctors(departmentID int64) ([]Row, string, error) {
	v, u, err := c.Get(fmt.Sprintf("/api/v1/hospital/doctor-list/%d", departmentID), nil)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

// ClinicByCode returns one clinic's detail by its code (e.g. 3501114186).
func (c *Client) ClinicByCode(code string) (Row, string, error) {
	v, u, err := c.Get("/api/v1/hospital/clinic/"+code, nil)
	if err != nil {
		return nil, u, err
	}
	row, _ := v.(Row)
	return row, u, nil
}

// ResolveClinicCode maps a clinic name substring to a unique clinic code,
// using the branch list's nested clinics.
func ResolveClinicCode(clinics []Row, name string) (string, []Row, error) {
	matched := []Row{}
	for _, cl := range clinics {
		if ContainsFold(Str(cl, "name"), name) {
			matched = append(matched, cl)
		}
	}
	if len(matched) == 1 {
		return Str(matched[0], "code"), nil, nil
	}
	return "", matched, fmt.Errorf("clinic %q matched %d clinics", name, len(matched))
}

// Districts returns districts with their nested branches.
func (c *Client) Districts() ([]Row, string, error) {
	v, u, err := c.Get("/api/v1/hospital/district-branches", nil)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

// Locations returns clinic locations (address, phone, map URL).
func (c *Client) Locations() ([]Row, string, error) {
	v, u, err := c.Get("/api/v1/hospital/location", nil)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

// Schedule returns the registration schedule between start and end
// (YYYY-MM-DD, inclusive). The API returns one row per clinic room per day.
func (c *Client) Schedule(start, end string) ([]Row, string, error) {
	q := url.Values{}
	q.Set("start_date", start)
	q.Set("end_date", end)
	v, u, err := c.Get("/api/v1/online-registration/clinic-schedule", q)
	if err != nil {
		return nil, u, err
	}
	rows, _ := v.([]Row)
	return rows, u, nil
}

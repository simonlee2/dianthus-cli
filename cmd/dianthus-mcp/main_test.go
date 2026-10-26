package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestParseErrorResponseIncludesNullID(t *testing.T) {
	b, err := json.Marshal(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	if string(obj["id"]) != "null" || obj["result"] != nil {
		t.Fatalf("invalid parse error response: %s", b)
	}
}

func TestResponsePreservesRequestID(t *testing.T) {
	for _, raw := range []string{`9007199254740993`, `0`, `"request-1"`} {
		t.Run(raw, func(t *testing.T) {
			b, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: decodeID(json.RawMessage(raw)), Result: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(b, &obj); err != nil {
				t.Fatal(err)
			}
			if string(obj["id"]) != raw {
				t.Fatalf("id changed: want %s got %s", raw, obj["id"])
			}
		})
	}
}

func TestBranchArgumentTyping(t *testing.T) {
	a, err := parseArgs(json.RawMessage(`{"branch":"1"}`))
	if err != nil || a.Branch != "1" {
		t.Fatalf("args=%v err=%v", a, err)
	}
	if _, err := parseArgs(json.RawMessage(`{"branch":1}`)); err == nil {
		t.Fatal("numeric branch unexpectedly accepted")
	} else {
		example := strings.TrimPrefix(err.suggestion, "pass an object, e.g. ")
		if a, err := parseArgs(json.RawMessage(example)); err != nil || a.Branch != "1" {
			t.Fatalf("invalid suggested example: %s", example)
		}
	}
}

func TestMCPErrorEnvelopeMatchesCLI(t *testing.T) {
	b, err := json.Marshal(errEnv(usageErr("fixture", "fixture suggestion")))
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["error"] == nil || obj["data"] != nil || obj["meta"] != nil || obj["command"] != nil || obj["ok"] != false {
		t.Fatalf("unexpected envelope: %s", b)
	}
}

func TestInitializeAndPingOffline(t *testing.T) {
	in, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"ping"}
`
	if _, err := in.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, out
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	main()
	b, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses=%s", b)
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil {
		t.Fatal(err)
	}
	if init.Result.ProtocolVersion != "2024-11-05" {
		t.Fatalf("version=%q", init.Result.ProtocolVersion)
	}
	var ping struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &ping); err != nil {
		t.Fatal(err)
	}
	if ping.ID != 2 || ping.Result == nil || len(ping.Result) != 0 {
		t.Fatalf("ping=%s", lines[1])
	}
}

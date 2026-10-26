package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"dianthus/internal/api"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProgressPartialFailure(t *testing.T) {
	requests := 0
	c := &api.Client{HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"status":200,"data":{"data":[{"id":1,"name":"One"},{"id":2,"name":"Two"}]}}`
		switch r.URL.Path {
		case "/api/v1/hospital/branche-list":
		case "/api/v1/hospital/branche/1/clinic-progress":
			body = `{"status":200,"data":{"data":[{"doctor_name":"Fixture doctor"}]}}`
		case "/api/v1/hospital/branche/2/clinic-progress":
			return nil, errors.New("fixture offline failure")
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	err := cmdProgress(c, &globals{}, []string{"--json"})
	if err == nil || err.code != "upstream_error" || err.exitCode != 1 || requests != 3 {
		t.Fatalf("requests=%d err=%v", requests, err)
	}
}

func TestCLIErrorEnvelope(t *testing.T) {
	b, err := json.Marshal(errEnvelope{SchemaVersion: 1, OK: false, Error: errorBody{Code: "usage_error", Message: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["error"] == nil || obj["ok"] != false {
		t.Fatalf("unexpected envelope: %s", b)
	}
}

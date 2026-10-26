package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBranchesOffline(t *testing.T) {
	c := &Client{HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/hospital/branche-list" || r.Header.Get("Authorization") != "" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":200,"data":{"data":[{"id":16,"name":"Fixture branch"}]}}`))}, nil
	})}}
	rows, _, err := c.Branches()
	if err != nil || len(rows) != 1 || Num(rows[0], "id") != 16 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestUpstreamEnvelopeFailure(t *testing.T) {
	c := &Client{HTTP: &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":500,"message":"fixture failure"}`))}, nil
	})}}
	if _, _, err := c.Branches(); err == nil {
		t.Fatal("expected upstream error")
	}
}

func TestAmbiguousBranchRejected(t *testing.T) {
	_, candidates, err := ResolveBranchID([]Row{{"id": float64(1), "name": "North One"}, {"id": float64(2), "name": "North Two"}}, "North")
	if err == nil || len(candidates) != 2 {
		t.Fatalf("candidates=%v err=%v", candidates, err)
	}
}

func TestProgressUsesPublicDashboardRoute(t *testing.T) {
	c := &Client{HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/hospital/branche/1/clinic-progress" || r.URL.RawQuery != "" {
			t.Fatalf("unexpected progress request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":200,"data":{"data":[{"current_number":3}]}}`))}, nil
	})}}
	rows, _, err := c.Progress(1)
	if err != nil || len(rows) != 1 || Num(rows[0], "current_number") != 3 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

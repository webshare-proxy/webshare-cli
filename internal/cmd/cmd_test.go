package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// runCLI executes the CLI in-process against args and returns captured
// stdout and the exit code.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	code := run(context.Background(), args)
	os.Stdout = old
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading stdout: %v", err)
	}
	return string(out), code
}

// newAPIServer serves canned JSON responses keyed by "METHOD path".
func newAPIServer(t *testing.T, responses map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func envelope(results ...any) map[string]any {
	if results == nil {
		results = []any{}
	}
	return map[string]any{"count": len(results), "next": nil, "previous": nil, "results": results}
}

func proxyFixture(id, address string, port int) map[string]any {
	return map[string]any{
		"id": id, "username": "user", "password": "pass",
		"proxy_address": address, "port": port, "valid": true,
		"country_code": "US", "city_name": "Dallas",
	}
}

func TestProxiesListTxt(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	server := newAPIServer(t, map[string]any{
		"GET /api/v2/proxy/list/": envelope(proxyFixture("d-1", "10.0.0.1", 8000), proxyFixture("d-2", "10.0.0.2", 8001)),
	})
	out, code := runCLI(t, "proxies", "list", "--base-url", server.URL, "--format", "txt")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	want := "10.0.0.1:8000:user:pass\n10.0.0.2:8001:user:pass\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestProxiesListCSV(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	server := newAPIServer(t, map[string]any{
		"GET /api/v2/proxy/list/": envelope(proxyFixture("d-1", "10.0.0.1", 8000)),
	})
	out, code := runCLI(t, "proxies", "list", "--base-url", server.URL, "--format", "csv")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want header + 1 row: %q", len(lines), out)
	}
	if lines[0] != "address,port,username,password,country_code,city_name,valid,id" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[1] != "10.0.0.1,8000,user,pass,US,Dallas,true,d-1" {
		t.Errorf("row = %q", lines[1])
	}
}

func TestProxiesListJSONFlag(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	server := newAPIServer(t, map[string]any{
		"GET /api/v2/proxy/list/": envelope(proxyFixture("d-1", "10.0.0.1", 8000)),
	})
	out, code := runCLI(t, "proxies", "list", "--base-url", server.URL, "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var proxies []map[string]any
	if err := json.Unmarshal([]byte(out), &proxies); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(proxies) != 1 || proxies[0]["id"] != "d-1" {
		t.Errorf("unexpected JSON: %v", proxies)
	}
}

func TestWhoami(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	server := newAPIServer(t, map[string]any{
		"GET /api/v2/profile/": map[string]any{"id": 1, "email": "user@webshare.io"},
	})
	out, code := runCLI(t, "whoami", "--base-url", server.URL)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if out != "user@webshare.io\n" {
		t.Errorf("output = %q", out)
	}
}

func TestMissingAPIKeyIsFriendly(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "")
	_, code := runCLI(t, "whoami")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	if _, code := runCLI(t, "plans", "show", "abc"); code != 2 {
		t.Errorf("plans show abc: exit code = %d, want 2", code)
	}
}

func TestUnknownFormat(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	server := newAPIServer(t, map[string]any{
		"GET /api/v2/proxy/list/": envelope(),
	})
	_, code := runCLI(t, "proxies", "list", "--base-url", server.URL, "--format", "yaml")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestIPAuthAddCurrent(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/proxy/ipauthorization/whatsmyip/":
			io.WriteString(w, `{"ip_address":"203.0.113.7"}`)
		case "POST /api/v2/proxy/ipauthorization/":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decoding body: %v", err)
			}
			io.WriteString(w, `{"id":9,"ip_address":"203.0.113.7"}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	out, code := runCLI(t, "ipauth", "add", "--current", "--base-url", server.URL)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if created["ip_address"] != "203.0.113.7" {
		t.Errorf("created ip = %v, want the detected IP", created["ip_address"])
	}
	if !strings.Contains(out, "authorized 203.0.113.7") {
		t.Errorf("output = %q", out)
	}
}

func TestRefreshRefusesWithoutConfirmation(t *testing.T) {
	t.Setenv("WEBSHARE_API_KEY", "k")
	_, code := runCLI(t, "proxies", "refresh")
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (non-interactive without --yes)", code)
	}
}

func TestParseSince(t *testing.T) {
	if got, err := parseSince(""); err != nil || got != nil {
		t.Errorf("parseSince(\"\") = (%v, %v), want (nil, nil)", got, err)
	}
	from, err := parseSince("7d")
	if err != nil {
		t.Fatalf("parseSince(7d): %v", err)
	}
	want := time.Now().Add(-7 * 24 * time.Hour)
	if from.Sub(want) > time.Minute || want.Sub(*from) > time.Minute {
		t.Errorf("parseSince(7d) = %v, want about %v", from, want)
	}
	if _, err := parseSince("2h"); err != nil {
		t.Errorf("parseSince(2h): %v", err)
	}
	if _, err := parseSince("soon"); err == nil {
		t.Error("parseSince(soon) succeeded, want an error")
	}
}

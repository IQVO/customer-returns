package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inboundmcp "github.com/IQVO/customer-returns/internal/adapters/inbound/mcp"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/application/usecases"
)

// Stateless (Agentic Standard v2, AS-4): no Mcp-Session-Id is issued, so
// per-request clients (the 2026-07-28 revision's server/discover) work and a
// legacy initialize handshake works too.

func newHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		Authorizations: memory.NewAuthorizationRepository(store),
		Groups:         memory.NewGroupViewRepository(store),
		Clock:          memory.NewClockAtNow(),
	}, usecases.Config{})
	return httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: svc})))
}

func post(t *testing.T, srv *httptest.Server, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

func TestInitializeIssuesNoSessionID(t *testing.T) {
	srv := newHTTPServer(t)
	status, header, body := post(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if status != http.StatusOK {
		t.Fatalf("initialize status = %d (%s)", status, body)
	}
	if sid := header.Get("Mcp-Session-Id"); sid != "" {
		t.Fatalf("a stateless server must not issue Mcp-Session-Id, got %q", sid)
	}
	var res struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(sseData(body)), &res); err != nil {
		t.Fatalf("initialize response: %v (%s)", err, body)
	}
	if !strings.Contains(res.Result.Instructions, "get_return_authorization") {
		t.Fatalf("instructions do not name the tools: %s", res.Result.Instructions)
	}
}

func TestToolsListOverHTTP(t *testing.T) {
	srv := newHTTPServer(t)
	status, _, body := post(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d (%s)", status, body)
	}
	data := sseData(body)
	if !strings.Contains(data, "get_return_authorization") || !strings.Contains(data, "list_return_authorizations") {
		t.Fatalf("tools/list is missing a tool: %s", data)
	}
}

// sseData extracts the first data: payload of an SSE body.
func sseData(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if d, ok := strings.CutPrefix(line, "data: "); ok {
			return d
		}
	}
	return body
}

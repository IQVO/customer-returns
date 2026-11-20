package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// wantTools is the curated, READ-ONLY tool set (docs/adr/0006): one tool per
// read GET of apis/openapi.yaml, alphabetical like the SDK lists them.
var wantTools = []string{
	"get_return_authorization", "list_return_authorizations",
}

func listTools(t *testing.T, h *harness) []*sdk.Tool {
	t.Helper()
	listed, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	return listed.Tools
}

func callTool(t *testing.T, h *harness, name string, args string) (*sdk.CallToolResult, error) {
	t.Helper()
	return h.session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      name,
		Arguments: json.RawMessage(args),
	})
}

func TestToolSurface(t *testing.T) {
	h := newHarness(t)
	tools := listTools(t, h)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("advertised tools %v, want exactly %v", names, wantTools)
	}
}

// writeVerbViolations is the write-verb sensor: no tool name may start with a
// write verb (the surface is read-only by construction; this pins the names).
func writeVerbViolations(names []string) []string {
	verbs := []string{"create", "authorize", "cancel", "issue", "link", "apply", "delete", "update", "put", "post", "record", "mark"}
	var out []string
	for _, n := range names {
		for _, v := range verbs {
			if strings.HasPrefix(n, v+"_") || n == v {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

func TestWriteVerbSensorFailsOnWriteNames(t *testing.T) {
	if got := writeVerbViolations(wantTools); len(got) != 0 {
		t.Fatalf("write verbs on the read-only surface: %v", got)
	}
	if got := writeVerbViolations([]string{"get_x", "authorize_return"}); len(got) != 1 {
		t.Fatalf("sensor missed the write verb: %v", got)
	}
}

func TestEveryToolIsReadOnlyAnnotatedL1(t *testing.T) {
	h := newHarness(t)
	for _, tool := range listTools(t, h) {
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != true {
			t.Fatalf("tool %s is not annotated read-only", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %s is not closed-world", tool.Name)
		}
		meta, ok := tool.Meta["com.warehouse/authority"].(string)
		if !ok || meta != "L1" {
			t.Fatalf("tool %s authority = %v, want L1", tool.Name, tool.Meta)
		}
	}
}

func TestGetReturnAuthorizationTool(t *testing.T) {
	h := newHarness(t)
	id := h.seedReturn(t, "ord-1", "NEAREST_RETURNS_CENTER")
	res, err := callTool(t, h, "get_return_authorization", `{"authorizationId":"`+string(id)+`"}`)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %+v", res)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), string(id)) || !strings.Contains(string(raw), "NEAREST_RETURNS_CENTER") {
		t.Fatalf("payload missing facts: %s", raw)
	}
}

func TestGetReturnAuthorizationNotFound(t *testing.T) {
	h := newHarness(t)
	res, err := callTool(t, h, "get_return_authorization", `{"authorizationId":"ret-none"}`)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("unknown id must be an isError result")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not-found") {
		t.Fatalf("error slug missing: %s", raw)
	}
}

func TestListReturnAuthorizationsTool(t *testing.T) {
	h := newHarness(t)
	id := h.seedReturn(t, "ord-1", "NEAREST_RETURNS_CENTER")
	res, err := callTool(t, h, "list_return_authorizations", `{"orderId":"ord-1"}`)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), string(id)) {
		t.Fatalf("payload missing the authorization: %s", raw)
	}
}

func TestOptionalInputsAreNotRequired(t *testing.T) {
	h := newHarness(t)
	for _, tool := range listTools(t, h) {
		if tool.Name == "list_return_authorizations" {
			if tool.InputSchema == nil {
				continue
			}
			raw, _ := json.Marshal(tool.InputSchema)
			var schema struct {
				Required []string `json:"required"`
			}
			_ = json.Unmarshal(raw, &schema)
			for _, req := range schema.Required {
				if req == "cursor" || req == "limit" {
					t.Fatalf("optional input %s is required: %s", req, raw)
				}
			}
		}
	}
}

// listPage calls list_return_authorizations and decodes its structured page.
func listPage(t *testing.T, h *harness, args string) (ids []string, nextCursor string) {
	t.Helper()
	res, err := callTool(t, h, "list_return_authorizations", args)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		t.Fatalf("tool errored: %s", raw)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var page struct {
		Items []struct {
			AuthorizationID string `json:"authorizationId"`
		} `json:"items"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode page %s: %v", raw, err)
	}
	for _, item := range page.Items {
		ids = append(ids, item.AuthorizationID)
	}
	return ids, page.NextCursor
}

func TestListReturnAuthorizationsCursorReachesTheNextPage(t *testing.T) {
	h := newHarness(t)
	first := h.seedReturn(t, "ord-1", "NEAREST_RETURNS_CENTER")
	second := h.seedReturn(t, "ord-1", "NEAREST_RETURNS_CENTER")

	page1, next := listPage(t, h, `{"orderId":"ord-1","limit":1}`)
	if len(page1) != 1 || next == "" {
		t.Fatalf("page 1 = %v next %q, want one item and a nextCursor", page1, next)
	}
	cursor, _ := json.Marshal(next)
	page2, next2 := listPage(t, h, `{"orderId":"ord-1","limit":1,"cursor":`+string(cursor)+`}`)
	if len(page2) != 1 || page2[0] == page1[0] {
		t.Fatalf("page 2 = %v after page 1 %v: the cursor was not applied", page2, page1)
	}
	got := map[string]bool{page1[0]: true, page2[0]: true}
	if !got[string(first)] || !got[string(second)] || next2 != "" {
		t.Fatalf("pages %v + %v (next %q) do not cover %s and %s exactly", page1, page2, next2, first, second)
	}
}

func TestListReturnAuthorizationsMalformedCursorIsValidationFailed(t *testing.T) {
	h := newHarness(t)
	h.seedReturn(t, "ord-1", "NEAREST_RETURNS_CENTER")
	for _, cursor := range []string{"broken", "not-a-time|ret-1", "2026-11-14T10:00:00Z|"} {
		res, err := callTool(t, h, "list_return_authorizations", `{"orderId":"ord-1","cursor":"`+cursor+`"}`)
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		raw, _ := json.Marshal(res.Content)
		if !res.IsError || !strings.Contains(string(raw), "validation-failed") {
			t.Fatalf("cursor %q answered %s, want an isError validation-failed", cursor, raw)
		}
	}
}

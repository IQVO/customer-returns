package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP surface beyond tools (Agentic Standard v2, AS-3): ONE resource
// template — the return authorization read model — and ONE prompt — this
// context's operator SOP.

func TestResourceTemplateAdvertised(t *testing.T) {
	h := newHarness(t)
	listed, err := h.session.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	found := false
	for _, rt := range listed.ResourceTemplates {
		if strings.Contains(rt.URITemplate, "warehouse://customer-returns/return-authorizations/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("authorization resource template not advertised: %+v", listed.ResourceTemplates)
	}
}

func TestResourceReadsAuthorization(t *testing.T) {
	h := newHarness(t)
	id := h.seedReturn(t, "ord-2", "NEAREST_RETURNS_CENTER")
	res, err := h.session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "warehouse://customer-returns/return-authorizations/" + string(id),
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d", len(res.Contents))
	}
	text := res.Contents[0].Text
	if !strings.Contains(text, string(id)) || !strings.Contains(text, "NEAREST_RETURNS_CENTER") {
		t.Fatalf("resource text is not the authorization JSON: %s", text)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("resource text is not JSON: %v (%s)", err, text)
	}
}

func TestResourceUnknownIdIsAnError(t *testing.T) {
	h := newHarness(t)
	if _, err := h.session.ReadResource(context.Background(), &sdk.ReadResourceParams{
		URI: "warehouse://customer-returns/return-authorizations/ret-none",
	}); err == nil {
		t.Fatal("read of an unknown authorization must error")
	}
}

func TestPromptHandsOutTheSOP(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.GetPrompt(context.Background(), &sdk.GetPromptParams{
		Name:      "review_return_authorization",
		Arguments: map[string]string{"authorizationId": "ret-0001"},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	raw, _ := json.Marshal(res.Messages)
	if !strings.Contains(string(raw), "ret-0001") || !strings.Contains(string(raw), "get_return_authorization") {
		t.Fatalf("prompt does not carry the SOP for the authorization: %s", raw)
	}
}

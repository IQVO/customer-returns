//go:build integration

package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type rawAnswer struct {
	status   int
	body     string
	replayed string
}

// send performs one request with the given Idempotency-Key and reports the
// status, body and Idempotency-Replayed header.
func (s *service) send(t *testing.T, method, path, body, key string) rawAnswer {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return rawAnswer{resp.StatusCode, string(raw), resp.Header.Get("Idempotency-Replayed")}
}

// phaseIdempotency proves the fleet REST guideline section 2 on the real router
// and the real Postgres store: global key, byte-identical replay with
// Idempotency-Replayed, 409 reuse (other body and other route), 400 for a
// missing or malformed key, a released (RETRYABLE) refusal that is not stored,
// and one effect under concurrency.
func phaseIdempotency(t *testing.T, s *service, w *world) {
	t.Helper()
	const authorize = `{"orderId":"ord-idem","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":100}],"reason":"NOT_NEEDED"}`
	first := s.send(t, http.MethodPost, "/return-authorizations", authorize, "idem-1")
	if first.status != http.StatusCreated || first.replayed != "" {
		t.Fatalf("first = %d replayed=%q %s", first.status, first.replayed, first.body)
	}
	again := s.send(t, http.MethodPost, "/return-authorizations", authorize, "idem-1")
	if again.status != first.status || again.body != first.body || again.replayed != "true" {
		t.Fatalf("replay = %d replayed=%q, want the first answer byte for byte with Idempotency-Replayed: true", again.status, again.replayed)
	}
	idemID := jsonField(t, first.body, "authorizationId")
	if n := w.db.count(`SELECT count(*) FROM return_authorizations WHERE authorization_id=$1`, idemID); n != 1 {
		t.Fatalf("rows after a replay = %d, want 1", n)
	}
	// Same key, different body: 409 idempotency-key-reused.
	diff := s.send(t, http.MethodPost, "/return-authorizations",
		`{"orderId":"ord-idem","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":200}],"reason":"NOT_NEEDED"}`, "idem-1")
	if diff.status != http.StatusConflict || diff.replayed != "" {
		t.Fatalf("different body = %d %s, want 409", diff.status, diff.body)
	}
	assertProblem(t, diff.body, "idempotency-key-reused")
	// The key is global: the same key on another route is a reuse, not a new key.
	other := s.send(t, http.MethodPost, "/return-authorizations/"+idemID+"/cancellations", "", "idem-1")
	if other.status != http.StatusConflict {
		t.Fatalf("same key on another route = %d %s, want 409", other.status, other.body)
	}
	assertProblem(t, other.body, "idempotency-key-reused")
	// Missing and malformed keys are 400.
	if got := s.send(t, http.MethodPost, "/return-authorizations", authorize, ""); got.status != http.StatusBadRequest {
		t.Fatalf("missing key = %d", got.status)
	}
	assertProblem(t, s.send(t, http.MethodPost, "/return-authorizations", authorize, "").body, "idempotency-key-required")
	for _, key := range []string{strings.Repeat("k", 129), "café"} {
		got := s.send(t, http.MethodPost, "/return-authorizations", authorize, key)
		if got.status != http.StatusBadRequest {
			t.Fatalf("key %q = %d, want 400", key, got.status)
		}
		assertProblem(t, got.body, "idempotency-key-invalid")
	}
	// A RETRYABLE refusal (the group is unknown to the view, which may lag) is NOT
	// stored: no key row, the retry executes again and is not a replay.
	ghost := `{"orderId":"ord-idem","groupNo":99,"lines":[{"lineNo":1}],"reason":"NOT_NEEDED"}`
	bad := s.send(t, http.MethodPost, "/return-authorizations", ghost, "idem-404")
	if bad.status != http.StatusUnprocessableEntity || bad.replayed != "" {
		t.Fatalf("ghost group = %d replayed=%q", bad.status, bad.replayed)
	}
	assertProblem(t, bad.body, "group-not-shipped")
	if c := w.db.count(`SELECT count(*) FROM idempotency_keys WHERE key='idem-404'`); c != 0 {
		t.Fatalf("idempotency rows after a RETRYABLE refusal = %d, want 0 (the key is released)", c)
	}
	if again := s.send(t, http.MethodPost, "/return-authorizations", ghost, "idem-404"); again.status != bad.status || again.replayed != "" {
		t.Fatalf("the retry of a released refusal = %d replayed=%q, want a fresh execution", again.status, again.replayed)
	}
	// N concurrent requests with one key: exactly one effect, the rest replay.
	const n = 8
	var wg sync.WaitGroup
	answers := make([]rawAnswer, n)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = s.send(t, http.MethodPost, "/return-authorizations", authorize, "idem-conc")
		}()
	}
	wg.Wait()
	replays := 0
	for _, a := range answers {
		if a.status != http.StatusCreated || a.body != answers[0].body {
			t.Fatalf("concurrent answer = %d %s", a.status, a.body)
		}
		if a.replayed == "true" {
			replays++
		}
	}
	if replays != n-1 {
		t.Fatalf("replays = %d, want %d (exactly one execution)", replays, n-1)
	}
	if c := w.db.count(`SELECT count(*) FROM return_authorizations WHERE order_id='ord-idem'`); c != 2 {
		t.Fatalf("authorizations of ord-idem after %d concurrent requests = %d, want 2 (one new, the replayed idem-1 one)", n, c)
	}
	if c := w.db.count(`SELECT count(*) FROM idempotency_keys WHERE key='idem-conc'`); c != 1 {
		t.Fatalf("idempotency rows for one key = %d, want 1", c)
	}
}

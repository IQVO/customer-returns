//go:build integration

// The replay-class rule (docs/adr/0013-idempotency-replay-classes.md) through the REAL chi router,
// the real use cases and a real Postgres 16: a refusal that nothing committed and that depends on
// state that can change (RETRYABLE) is NOT stored, so the retry under the same Idempotency-Key
// executes; a refusal that committed state (OUTCOME) is stored together with its writes.
package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/IQVO/customer-returns/internal/adapters/inbound/http"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/ids"
	outboundkafka "github.com/IQVO/customer-returns/internal/adapters/outbound/kafka"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres"
	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/application/usecases"
	"github.com/IQVO/customer-returns/internal/domain/network"
	"github.com/IQVO/customer-returns/internal/domain/returnauthorization"
)

// replayClock is a clock a test moves forward between two requests.
type replayClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *replayClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *replayClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// unavailableLabels is a reverse-label route that is not live (the shipped runtime has none).
type unavailableLabels struct{}

func (unavailableLabels) IssueReturnLabel(context.Context, requests.IssueReturnLabel) (requests.ReturnLabel, error) {
	return requests.ReturnLabel{}, ports.ErrLabelUnavailable
}

// racingAuthorizations stands for a competing writer: while armed, every Save of an existing
// authorization is preceded by a COMMITTED version bump on a second connection, so the request's
// own save finds zero rows (ports.ErrConcurrentUpdate), exactly as a lost optimistic-lock race.
type racingAuthorizations struct {
	ports.AuthorizationRepository
	pool      *pgxpool.Pool
	competing atomic.Int32 // how many upcoming saves lose the race
}

func (r *racingAuthorizations) Save(ctx context.Context, a *returnauthorization.ReturnAuthorization) error {
	if a.Version() > 1 && r.competing.Load() > 0 {
		r.competing.Add(-1)
		if _, err := r.pool.Exec(context.Background(),
			`UPDATE return_authorizations SET version = version + 1 WHERE authorization_id = $1`, string(a.ID())); err != nil {
			return err
		}
	}
	return r.AuthorizationRepository.Save(ctx, a)
}

// replayFixture is the shipped wiring (cmd/api wire_adapters.go) over a fresh Postgres.
type replayFixture struct {
	pool   *pgxpool.Pool
	router http.Handler
	clock  *replayClock
	races  *racingAuthorizations
	groups *postgres.GroupViewRepo
	net    *postgres.NetworkViewRepo
}

func newReplayFixture(t *testing.T) replayFixture {
	t.Helper()
	pool := poisonPool(t)
	races := &racingAuthorizations{AuthorizationRepository: postgres.NewAuthorizationRepo(pool), pool: pool}
	clk := &replayClock{now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	fx := replayFixture{
		pool: pool, clock: clk, races: races,
		groups: postgres.NewGroupViewRepo(pool), net: postgres.NewNetworkViewRepo(pool),
	}
	svc := usecases.New(usecases.Deps{
		UoW: postgres.NewUnitOfWork(pool), Authorizations: races, Groups: fx.groups, Network: fx.net,
		Outcomes: postgres.NewOutcomeFactsRepo(pool), Labels: unavailableLabels{},
		Outbox: postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), outboundkafka.NewEncoder()),
		Inbox:  postgres.NewProcessedEventRepo(pool), Clock: clk, IDs: ids.UUID{},
	}, usecases.Config{})
	fx.router = inboundhttp.NewRouter(&inboundhttp.Server{
		Service: svc, Idempotency: inboundhttp.RequireIdempotencyKey(pool, pgtx.With),
	}, nil, "")
	return fx
}

func (f replayFixture) post(path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f replayFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (f replayFixture) keyRows(t *testing.T, key string) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

func (f replayFixture) seedGroup(t *testing.T, orderID string, status network.GroupStatus) {
	t.Helper()
	err := f.groups.SaveGroup(context.Background(), network.OrderGroup{
		OrderID: orderID, GroupNo: 1, Status: status, UpdatedAt: f.clock.Now(),
		Lines: []network.GroupLine{{LineNo: 1, CatalogItemID: "item-1", Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("seed the order group view: %v", err)
	}
}

func (f replayFixture) seedOrigin(t *testing.T, orderID string) {
	t.Helper()
	err := f.net.SaveOrigin(context.Background(), network.GroupOrigin{
		OrderID: orderID, GroupNo: 1, NodeID: "node-origin-1", UpdatedAt: f.clock.Now(),
	})
	if err != nil {
		t.Fatalf("seed the group origin: %v", err)
	}
}

func problemType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem document: %q: %v", rec.Body.String(), err)
	}
	return p.Type
}

func notReplayed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get(inboundhttp.IdempotencyReplayedHeader); got != "" {
		t.Fatalf("a fresh execution carries %s: %q", inboundhttp.IdempotencyReplayedHeader, got)
	}
}

func replayOf(t *testing.T, first, again *httptest.ResponseRecorder) {
	t.Helper()
	if again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s, want the stored %d %s", again.Code, again.Body.String(), first.Code, first.Body.String())
	}
	if got := again.Header().Get(inboundhttp.IdempotencyReplayedHeader); got != "true" {
		t.Fatalf("replay %s = %q, want true", inboundhttp.IdempotencyReplayedHeader, got)
	}
}

// keepBody is a returnless authorization: one line under the KEEP threshold, so no destination
// is needed and the group view is the only fact the request depends on.
const keepBody = `{"orderId":"ord-late","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":100}],"reason":"NOT_NEEDED"}`

// RETRYABLE (the motivating case): the OrderGroupView lags, so the group is unknown and the
// request is refused 422 group-not-shipped, whose own detail says "retry later with the same
// Idempotency-Key". The refusal must NOT be stored: once the view catches up the SAME key succeeds.
func TestReplayClass_GroupNotShippedIsReleasedAndTheSameKeySucceedsOnceTheViewCatchesUp(t *testing.T) {
	fx := newReplayFixture(t)

	first := fx.post("/return-authorizations", "late-view", keepBody)
	if first.Code != http.StatusUnprocessableEntity || problemType(t, first) != "group-not-shipped" {
		t.Fatalf("first = %d %s, want 422 group-not-shipped", first.Code, first.Body.String())
	}
	notReplayed(t, first)
	if n := fx.keyRows(t, "late-view"); n != 0 {
		t.Fatalf("key rows after a RETRYABLE refusal = %d, want 0: the key must be released", n)
	}

	// The group is still PROMISED: the same refusal, still not stored.
	fx.seedGroup(t, "ord-late", network.GroupPromised)
	if again := fx.post("/return-authorizations", "late-view", keepBody); again.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a PROMISED group = %d %s, want 422 group-not-shipped", again.Code, again.Body.String())
	}
	if n := fx.keyRows(t, "late-view"); n != 0 {
		t.Fatalf("key rows after the second refusal = %d, want 0", n)
	}

	// customer-ordering's shipped fact reaches the view: the SAME key now executes.
	fx.seedGroup(t, "ord-late", network.GroupShipped)
	retry := fx.post("/return-authorizations", "late-view", keepBody)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry under the same key = %d %s, want 201: the retry must execute", retry.Code, retry.Body.String())
	}
	notReplayed(t, retry)
	if n := fx.keyRows(t, "late-view"); n != 1 {
		t.Fatalf("key rows after the success = %d, want 1: a 2xx is stored", n)
	}
	replayOf(t, retry, fx.post("/return-authorizations", "late-view", keepBody))
	if n := fx.count(t, `SELECT count(*) FROM return_authorizations WHERE order_id = 'ord-late'`); n != 1 {
		t.Fatalf("authorizations after the retry and its replay = %d, want 1", n)
	}

	// A released key is forgotten: a different body under it is a NEW request, never a reuse.
	other := `{"orderId":"ord-other","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":100}],"reason":"NOT_NEEDED"}`
	if refused := fx.post("/return-authorizations", "late-view-2", other); refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("setup: %d %s", refused.Code, refused.Body.String())
	}
	fx.seedGroup(t, "ord-other", network.GroupDelivered)
	if got := fx.post("/return-authorizations", "late-view-2", keepBody); got.Code != http.StatusCreated {
		t.Fatalf("another body under a released key = %d %s, want a new request (201), not idempotency-key-reused", got.Code, got.Body.String())
	}
}

// authorizedDropOff authorizes a DROP_OFF return (price above the threshold, an origin node known)
// and returns its id.
func (f replayFixture) authorizedDropOff(t *testing.T, orderID string) string {
	t.Helper()
	f.seedGroup(t, orderID, network.GroupShipped)
	f.seedOrigin(t, orderID)
	body := `{"orderId":"` + orderID + `","groupNo":1,"lines":[{"lineNo":1,"unitPriceCents":5000}],"reason":"NOT_NEEDED"}`
	rec := f.post("/return-authorizations", "authorize-"+orderID, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("authorize = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID    string `json:"authorizationId"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.State != "AUTHORIZED" {
		t.Fatalf("authorize answer %s: %v", rec.Body.String(), err)
	}
	return out.ID
}

// RETRYABLE: the save loses an optimistic-lock race twice (the use case re-reads and retries once
// before it answers 409 concurrent-update). Nothing was written, so the key row is gone; the retry
// under the SAME key executes, succeeds, and only then is stored and replayed, with one effect.
func TestReplayClass_ConcurrentUpdateLoserIsReleasedAndItsRetryExecutes(t *testing.T) {
	fx := newReplayFixture(t)
	id := fx.authorizedDropOff(t, "ord-race")
	events := fx.count(t, `SELECT count(*) FROM outbox_events`)
	cancel := "/return-authorizations/" + id + "/cancellations"

	fx.races.competing.Store(2)
	loser := fx.post(cancel, "race-loser", "")
	if loser.Code != http.StatusConflict || problemType(t, loser) != "concurrent-update" {
		t.Fatalf("loser = %d %s, want 409 concurrent-update", loser.Code, loser.Body.String())
	}
	notReplayed(t, loser)
	if n := fx.keyRows(t, "race-loser"); n != 0 {
		t.Fatalf("key rows of the loser = %d, want 0: a RETRYABLE refusal is not stored", n)
	}
	if n := fx.count(t, `SELECT count(*) FROM outbox_events`); n != events {
		t.Fatalf("outbox rows after the refusal = %d, want %d: nothing committed", n, events)
	}

	retry := fx.post(cancel, "race-loser", "")
	if retry.Code != http.StatusOK {
		t.Fatalf("retry under the same key = %d %s, want 200: the retry must execute", retry.Code, retry.Body.String())
	}
	notReplayed(t, retry)
	if n := fx.keyRows(t, "race-loser"); n != 1 {
		t.Fatalf("key rows after the successful retry = %d, want 1", n)
	}
	replayOf(t, retry, fx.post(cancel, "race-loser", ""))
	if n := fx.count(t, `SELECT count(*) FROM return_authorizations WHERE authorization_id = $1 AND state = 'CANCELLED'`, id); n != 1 {
		t.Fatalf("cancelled authorizations = %d, want 1", n)
	}
	if n := fx.count(t, `SELECT count(*) FROM outbox_events`); n != events+1 {
		t.Fatalf("outbox rows after the retry and its replay = %d, want %d (one cancellation event, once)", n, events+1)
	}
}

// OUTCOME: the label request finds the authorization past its 30-day TTL. The use case cancels it
// in place (the CANCELLED authorization and its outbox event, usecases/labels.go IssueLabel ->
// expireAndReject -> expireLazily) and THEN refuses with 422 authorization-expired. The refusal is
// stored AND the cancellation commits with it: after the refusal the authorization is CANCELLED
// and the outbox holds its event, a replay answers from the key row and records nothing new, and a
// fresh key meets a plain refusal on the now-cancelled authorization that records nothing either.
func TestReplayClass_AuthorizationExpiredCommitsTheCancellationAndIsReplayed(t *testing.T) {
	fx := newReplayFixture(t)
	id := fx.authorizedDropOff(t, "ord-expiry")
	labels := "/return-authorizations/" + id + "/labels"
	body := `{"packageNo":1}`
	eventsOf := func() int { return fx.count(t, `SELECT count(*) FROM outbox_events WHERE subject = $1`, id) }
	if n := eventsOf(); n != 1 {
		t.Fatalf("setup: outbox rows of the authorization = %d, want 1 (ReturnAuthorized)", n)
	}

	fx.clock.Advance(returnauthorization.ReturnAuthorizationTTL + time.Hour)
	first := fx.post(labels, "expired-label", body)
	if first.Code != http.StatusUnprocessableEntity || problemType(t, first) != "authorization-expired" {
		t.Fatalf("first = %d %s, want 422 authorization-expired", first.Code, first.Body.String())
	}
	notReplayed(t, first)
	if n := fx.count(t, `SELECT count(*) FROM return_authorizations WHERE authorization_id = $1 AND state = 'CANCELLED'`, id); n != 1 {
		t.Fatalf("cancelled authorizations after the refusal = %d, want 1: the OUTCOME commits the lazy expiry", n)
	}
	if n := eventsOf(); n != 2 {
		t.Fatalf("outbox rows after the refusal = %d, want 2 (ReturnAuthorized + ReturnCancelled)", n)
	}
	if n := fx.count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1 AND status_code = 422`, "expired-label"); n != 1 {
		t.Fatalf("stored answers under the key = %d, want 1: an OUTCOME is stored", n)
	}

	for i := 0; i < 2; i++ {
		replayOf(t, first, fx.post(labels, "expired-label", body))
	}
	if n := eventsOf(); n != 2 {
		t.Fatalf("outbox rows after two replays = %d, want 2: a replay records nothing new", n)
	}

	other := fx.post(labels, "expired-label-2", body)
	if other.Code != http.StatusConflict || problemType(t, other) != "illegal-transition" {
		t.Fatalf("a fresh key on the cancelled authorization = %d %s, want 409 illegal-transition", other.Code, other.Body.String())
	}
	if n := eventsOf(); n != 2 {
		t.Fatalf("outbox rows after the second refusal = %d, want 2: nothing new", n)
	}
	if n := fx.keyRows(t, "expired-label-2"); n != 0 {
		t.Fatalf("key rows of a RETRYABLE illegal-transition = %d, want 0", n)
	}
}

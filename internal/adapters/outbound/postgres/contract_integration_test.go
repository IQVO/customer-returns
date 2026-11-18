//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/postgres"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
	"github.com/IQVO/customer-returns/internal/testing/repocontract"
)

// The Postgres adapters keep the same contract as the memory doubles: the
// suite below also runs against the doubles (memory/contract_test.go).
func TestNetworkNodesContract(t *testing.T) {
	repocontract.NetworkNodes(t, postgres.NewNetworkViewRepo(pgtest.NewPool(t)))
}

// forgetClaimsSQL is, word for word, the statement the runbook gives the
// operator before a rebuild of a read model (docs/docs/operations/runbook.md).
const forgetClaimsSQL = `DELETE FROM processed_events WHERE event_type = $1`

func TestInboxContract(t *testing.T) {
	pool := pgtest.NewPool(t)
	forget := func(eventType string) int {
		tag, err := pool.Exec(context.Background(), forgetClaimsSQL, eventType)
		if err != nil {
			t.Fatalf("forget %s: %v", eventType, err)
		}
		return int(tag.RowsAffected())
	}
	repocontract.Inbox(t, postgres.NewProcessedEventRepo(pool), postgres.NewUnitOfWork(pool), forget)
}

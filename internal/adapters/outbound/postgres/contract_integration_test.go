//go:build integration

package postgres_test

import (
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

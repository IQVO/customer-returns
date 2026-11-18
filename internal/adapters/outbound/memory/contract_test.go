package memory_test

import (
	"testing"

	"github.com/IQVO/customer-returns/internal/adapters/outbound/memory"
	"github.com/IQVO/customer-returns/internal/testing/repocontract"
)

// The memory doubles keep the same contract as the Postgres adapters: the
// suite below also runs against real Postgres
// (postgres/contract_integration_test.go).
func TestNetworkNodesContract(t *testing.T) {
	repocontract.NetworkNodes(t, memory.NewNetworkViewRepository(memory.NewStore()))
}

//go:build integration

package kafka_test

import (
	"os"
	"testing"

	"github.com/IQVO/customer-returns/internal/testing/kafkatest"
	"github.com/IQVO/customer-returns/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

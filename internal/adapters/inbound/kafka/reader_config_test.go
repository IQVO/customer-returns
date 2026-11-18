package kafka

import (
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Reader.Close waits for the in-flight fetch: with five consumers the default
// MaxWait (10s) would blow the graceful-stop budget, so it stays short.
func TestReaderConfigKeepsFetchesShortAndCommitsSynchronously(t *testing.T) {
	cfg := readerConfig([]string{"b:9092"}, "topic", "group")
	if cfg.MaxWait != 500*time.Millisecond {
		t.Errorf("MaxWait = %v, want 500ms", cfg.MaxWait)
	}
	if cfg.Topic != "topic" || cfg.GroupID != "group" || len(cfg.Brokers) != 1 || cfg.CommitInterval != 0 {
		t.Errorf("config = %+v", cfg)
	}
}

// A consumer group with no committed offset starts at the EARLIEST retained
// offset: kafka-go's default when StartOffset is left unset (FirstOffset). The
// runbook's replay and rebuild procedures rest on it: a new consumer group id
// re-reads the whole topic. Setting StartOffset to LastOffset would make a new
// group skip every fact already on the topic.
func TestANewConsumerGroupReadsTheTopicFromItsStart(t *testing.T) {
	cfg := readerConfig([]string{"b:9092"}, "topic", "group")
	if cfg.StartOffset != 0 && cfg.StartOffset != kafkago.FirstOffset {
		t.Fatalf("StartOffset = %d: a new group would not start at the earliest offset", cfg.StartOffset)
	}
}

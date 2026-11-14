package main

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// quiet is the discard logger tests use.
func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://u:p@h:5432/customer-returns",
		"KAFKA_BROKERS":          "b1:9092, b2:9092",
		"ORDER_CONSUMER_GROUP":    "g-order",
		"TOPOLOGY_CONSUMER_GROUP": "g-topology",
		"SOURCING_CONSUMER_GROUP": "g-sourcing",
		"VENDOR_CONSUMER_GROUP":   "g-vendor",
	}
}

func lookup(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestLoadRuntimeValid(t *testing.T) {
	rt, err := loadRuntime(lookup(baseEnv()))
	if err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	if len(rt.brokers) != 2 || rt.brokers[1] != "b2:9092" ||
		rt.groups["ORDER_CONSUMER_GROUP"] != "g-order" || rt.groups["VENDOR_CONSUMER_GROUP"] != "g-vendor" {
		t.Fatalf("runtime = %+v", rt)
	}
	if rt.labelsURL != "" {
		t.Fatalf("labelsURL = %q, want empty when unset", rt.labelsURL)
	}
}

func TestLoadRuntimeOptionalLabelURL(t *testing.T) {
	env := baseEnv()
	env["SHIPPING_LABELS_URL"] = "http://shipping-and-tracking:8080/ "
	rt, err := loadRuntime(lookup(env))
	if err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	if rt.labelsURL != "http://shipping-and-tracking:8080" {
		t.Fatalf("labelsURL = %q", rt.labelsURL)
	}
}

func TestLoadRuntimeRefuses(t *testing.T) {
	cases := []struct {
		name string
		mut  func(env map[string]string)
		key  string
	}{
		{"no database", func(e map[string]string) { e["DATABASE_URL"] = "" }, "DATABASE_URL"},
		{"no brokers", func(e map[string]string) { e["KAFKA_BROKERS"] = "" }, "KAFKA_BROKERS"},
		{"order group", func(e map[string]string) { e["ORDER_CONSUMER_GROUP"] = "" }, "ORDER_CONSUMER_GROUP"},
		{"topology group", func(e map[string]string) { e["TOPOLOGY_CONSUMER_GROUP"] = "" }, "TOPOLOGY_CONSUMER_GROUP"},
		{"sourcing group", func(e map[string]string) { e["SOURCING_CONSUMER_GROUP"] = "" }, "SOURCING_CONSUMER_GROUP"},
		{"vendor group", func(e map[string]string) { e["VENDOR_CONSUMER_GROUP"] = "" }, "VENDOR_CONSUMER_GROUP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			tc.mut(env)
			_, err := loadRuntime(lookup(env))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, errMissingDatabaseURL) && !errors.Is(err, errMissingBrokers) &&
				!strings.Contains(err.Error(), tc.key) {
				t.Fatalf("err = %v, want it to name %s", err, tc.key)
			}
		})
	}
}

func TestSplitBrokers(t *testing.T) {
	if got := splitBrokers(" a , b ,, "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("splitBrokers = %v", got)
	}
}

func TestParsePublisherMode(t *testing.T) {
	if err := parsePublisherMode(""); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if err := parsePublisherMode("kafka"); err != nil {
		t.Fatalf("kafka: %v", err)
	}
	if err := parsePublisherMode("log"); err == nil {
		t.Fatal("log must be refused")
	}
	if err := parsePublisherMode("carrier-pigeon"); err == nil {
		t.Fatal("unknown mode must be refused")
	}
}

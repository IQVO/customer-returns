# Pinned producer contracts

Byte copies of the producer specs customer-returns depends on. The registry is `pinned.json`; the CI job
`pinned-contracts` (`scripts/harness/pinned_contract_gate.py`) fails when a copy differs from its producer's
`develop`. Never edit a file in this directory: refresh it by copying the producer's file over it, then fix what
the contract tests report. The full procedure, the tests that read these files and the reason they live here and
not under `internal/` are in `docs/docs/development/contract-testing.md`.

## Provenance

The producer commit each copy was taken from (the head of the producer's `develop` at the time, and the last
commit that changed the file). Update the row in the commit that refreshes the pin.

| Pin | Producer file | Producer `develop` | File last changed | Copied |
| --- | --- | --- | --- | --- |
| `customer-ordering.asyncapi.yaml` | `IQVO/customer-ordering` `apis/asyncapi.yaml` | `c1a126b` | `c1a126b` | 2026-11-18 |
| `node-topology.asyncapi.yaml` | `IQVO/node-topology` `apis/asyncapi.yaml` | `d3e6211` | `a71c81e` | 2026-11-18 |
| `order-sourcing.asyncapi.yaml` | `IQVO/order-sourcing` `apis/asyncapi.yaml` | `f352218` | `74ca067` | 2026-11-18 |
| `vendor-connectivity.asyncapi.yaml` | `IQVO/vendor-connectivity` `apis/asyncapi.yaml` | `c1187f5` | `c1187f5` | 2026-11-18 |
| `shipping-and-tracking.openapi.yaml` | `IQVO/shipping-and-tracking` `apis/openapi.yaml` | `15b61d0` | `15b61d0` | 2026-11-18 |

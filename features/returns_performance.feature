Feature: Returns performance, the analytics read side
  The projector folds ONLY this context's own events topic (ReturnAuthorized and
  ReturnCompleted, ADR 0012) and GET /reports/returns-performance answers per
  UTC day: the returns authorized, the returns received, the p50/p95 time to
  receive and the reason Pareto. The events here are the ones the service
  really published, encoded by the real outbox encoder.

  Scenario: An authorized and received return shows up per UTC day
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is DELIVERED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 9900 cents
    And the vendor submits an outcome of 1 restockable "sku-1" units at "node-rc-1"
    And the projector folds the events the service published
    And the returns performance report is requested from "2026-11-14" to "2026-11-18"
    Then the report has 2 days
    And day "2026-11-14" shows 1 authorized and 0 received
    And day "2026-11-18" shows 0 authorized and 1 received
    And day "2026-11-18" took 345600 seconds to receive over 1 samples
    And the reason Pareto is "DEFECTIVE=1"
    And the freshness is set

  Scenario: A cancelled return stays counted as authorized and never as received
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is DELIVERED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 9900 cents
    And the authorization is cancelled by the shopper
    And the projector folds the events the service published
    And the returns performance report is requested from "2026-11-14" to "2026-11-14"
    Then the report has 1 days
    And day "2026-11-14" shows 1 authorized and 0 received
    And day "2026-11-14" has no time to receive

  Scenario: A day without any returns activity is an empty report
    When the returns performance report is requested from "2026-11-14" to "2026-11-14"
    Then the report has 0 days
    And the reason Pareto is empty
    And the freshness is null

  Scenario: A legacy receipt whose authorization is not projected counts as received without a timing
    When a ReturnCompleted for "ret-legacy" received at "2026-11-18T10:00:00Z" arrives on the events topic
    And the returns performance report is requested from "2026-11-18" to "2026-11-18"
    Then the report has 1 days
    And day "2026-11-18" shows 0 authorized and 1 received
    And day "2026-11-18" has no time to receive
    And the freshness is set

  Scenario: A legacy flat message on the events topic is skipped and changes nothing
    When a legacy flat message that is not a CloudEvent is on the events topic
    And the returns performance report is requested from "2026-11-14" to "2026-11-18"
    Then the report has 0 days
    And the freshness is null

  Scenario: A range the contract refuses is a problem
    When the returns performance report is requested from "2026-11-18" to "2026-11-14"
    Then the report problem is "invalid-report-range" with status 400

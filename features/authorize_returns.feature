Feature: Authorize a return of one order group
  The shopper-facing authorization decides the disposition at authorization
  time from the local read models: returnless KEEP under the value threshold,
  the region's returns centre, or the origin node.

  Scenario: Returnless KEEP under the threshold
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is SHIPPED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 400 cents
    Then the disposition is KEEP
    And the state is CLOSED_KEEP

  Scenario: The nearest returns centre over the threshold
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is DELIVERED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 9900 cents
    Then the disposition is NEAREST_RETURNS_CENTER
    And the state is AUTHORIZED

  Scenario: A group that has not shipped cannot be returned yet
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is PROMISED
    When the shopper authorizes a return with unit price 9900 cents
    Then the problem is "group-not-shipped" with status 422

  Scenario: No destination in the region and no origin node known
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is SHIPPED
    When the shopper authorizes a return with unit price 9900 cents
    Then the problem is "no-return-destination" with status 422

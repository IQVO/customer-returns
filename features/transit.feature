Feature: The return's transit and completion
  The reverse label is issued through the one in-org call to the shipping
  context; the returns centre's graded outcome completes the authorization
  and publishes the restock fact.

  Scenario: Label, outcome, completion
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is DELIVERED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 9900 cents
    And the vendor issued reverse label "trk-rev-1" for the authorization
    Then the authorization carries tracking number "trk-rev-1"
    When the vendor submits an outcome of 1 restockable "sku-1" units at "node-rc-1"
    Then the state is RECEIVED
    And a ReturnCompleted event names "sku-1" with 1 restockable units

  Scenario: A line the authorization never covers is recorded, not restocked
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is DELIVERED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 9900 cents
    When the vendor submits an outcome of 5 restockable "sku-999" units at "node-rc-1"
    Then the outcome fact records line "sku-999" as unexpected

  Scenario: The shopper cancels before transit
    Given the order "ord-1" group 1 was placed with prefix "013" and lines "2"
    And the group is DELIVERED
    And an ACTIVE returns centre "node-rc-1" serves region "region-se" and prefix "013" is in that region
    When the shopper authorizes a return with unit price 9900 cents
    And the authorization is cancelled by the shopper
    Then the state is CANCELLED

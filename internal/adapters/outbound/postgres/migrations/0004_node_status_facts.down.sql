-- Rows the previous schema cannot hold (a lifecycle-only row, or a node type
-- the old list did not name) are dropped: they are event-fed and a replay of
-- the node-topology topic rebuilds them.
DELETE FROM returns_network_nodes
 WHERE node_type NOT IN ('FULFILLMENT_CENTER', 'RETURNS_CENTER', 'INBOUND_CROSS_DOCK', 'DELIVERY_STATION', 'SORTATION_CENTER', 'AIR_HUB', 'PICKUP_POINT');

ALTER TABLE returns_network_nodes ADD CONSTRAINT returns_network_nodes_node_type_check
    CHECK (node_type IN ('FULFILLMENT_CENTER', 'RETURNS_CENTER', 'INBOUND_CROSS_DOCK', 'DELIVERY_STATION', 'SORTATION_CENTER', 'AIR_HUB', 'PICKUP_POINT'));

ALTER TABLE returns_network_nodes DROP COLUMN lifecycle_at;

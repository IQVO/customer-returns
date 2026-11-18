-- node-topology publishes the node's descriptive attributes on NodeRegistered
-- only; NodeActivated, NodeSuspended and NodeRetired carry the node id and an
-- instant. The node half of the ReturnsNetworkView therefore keeps the two
-- kinds of fact apart (ADR 0003, addendum 2026-11-17).
--
-- lifecycle_at is the instant of the status fact that set `lifecycle`: a status
-- fact older than it is a no-op, so a dead-lettered fact replayed after a newer
-- status cannot undo it. NULL means no status fact was applied yet (every row
-- written before this migration, and every node that was only registered).
ALTER TABLE returns_network_nodes ADD COLUMN lifecycle_at TIMESTAMPTZ;

-- node_type is the producer's enumeration, not this context's: the row stores
-- what node-topology published (a tolerant reader), and it is empty on the
-- lifecycle-only row a status fact creates for a node whose registration was
-- not seen yet. Only node_type = 'RETURNS_CENTER' ever influences a
-- disposition, so the list this CHECK enumerated protected nothing here and
-- turned a node type added by the producer into a dead letter.
ALTER TABLE returns_network_nodes DROP CONSTRAINT returns_network_nodes_node_type_check;

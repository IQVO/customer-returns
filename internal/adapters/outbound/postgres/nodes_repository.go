package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// The node half of the ReturnsNetworkView (returns_network_nodes). The two
// writes mirror the two kinds of node fact: the registration owns the
// descriptive columns, a status fact owns the lifecycle columns, and neither
// statement names the other's columns in its UPDATE.

// RegisterNode upserts the node's descriptive attributes (type, region, label
// prefix). A new row starts
// PENDING with no status instant; an existing row keeps lifecycle and
// lifecycle_at, so a registration applied again never demotes the node.
func (r *NetworkViewRepo) RegisterNode(ctx context.Context, n network.Node) error {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO returns_network_nodes (node_id, node_type, region_id, label_postal_prefix, lifecycle, updated_at)
VALUES ($1,$2,$3,$4,'PENDING',$5)
ON CONFLICT (node_id) DO UPDATE
SET node_type = EXCLUDED.node_type, region_id = EXCLUDED.region_id,
    label_postal_prefix = EXCLUDED.label_postal_prefix, updated_at = EXCLUDED.updated_at`,
		n.NodeID, string(n.Type), n.RegionID, n.LabelPostalPrefix, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("register node: %d rows", tag.RowsAffected())
	}
	return nil
}

// SetNodeLifecycle moves the lifecycle and its instant, nothing else. A fact
// older than the stored instant matches no row (a stale no-op). A node never
// seen gets a lifecycle-only row: empty type, region and label prefix (the
// column default), which no lookup of a returns centre ever matches, until
// the registration fills them.
func (r *NetworkViewRepo) SetNodeLifecycle(ctx context.Context, nodeID string, lifecycle network.NodeLifecycle, at time.Time) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO returns_network_nodes (node_id, node_type, region_id, lifecycle, lifecycle_at, updated_at)
VALUES ($1,'','',$2,$3,$4)
ON CONFLICT (node_id) DO UPDATE
SET lifecycle = EXCLUDED.lifecycle, lifecycle_at = EXCLUDED.lifecycle_at, updated_at = EXCLUDED.updated_at
WHERE returns_network_nodes.lifecycle_at IS NULL OR returns_network_nodes.lifecycle_at <= EXCLUDED.lifecycle_at`,
		nodeID, string(lifecycle), at.UTC(), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("set node lifecycle: %w", err)
	}
	return nil
}

// Node returns the node row, or ports.ErrNotFound.
func (r *NetworkViewRepo) Node(ctx context.Context, nodeID string) (network.Node, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx, `
SELECT node_type, region_id, label_postal_prefix, lifecycle, lifecycle_at
FROM returns_network_nodes WHERE node_id = $1`, nodeID)
	var (
		nodeType    string
		lifecycle   string
		lifecycleAt *time.Time
	)
	n := network.Node{NodeID: nodeID}
	if err := row.Scan(&nodeType, &n.RegionID, &n.LabelPostalPrefix, &lifecycle, &lifecycleAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return network.Node{}, ports.ErrNotFound
		}
		return network.Node{}, fmt.Errorf("get node: %w", err)
	}
	n.Type, n.Lifecycle = network.NodeType(nodeType), network.NodeLifecycle(lifecycle)
	if lifecycleAt != nil {
		n.LifecycleAt = lifecycleAt.UTC()
	}
	return n, nil
}

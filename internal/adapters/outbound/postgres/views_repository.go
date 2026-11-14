package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/customer-returns/internal/application/ports"
	"github.com/IQVO/customer-returns/internal/application/requests"
	"github.com/IQVO/customer-returns/internal/domain/network"
)

// GroupViewRepo is the Postgres ports.GroupViewRepository (the OrderGroupView).
type GroupViewRepo struct{ pool *pgxpool.Pool }

// NewGroupViewRepo builds the repository.
func NewGroupViewRepo(pool *pgxpool.Pool) *GroupViewRepo { return &GroupViewRepo{pool: pool} }

var _ ports.GroupViewRepository = (*GroupViewRepo)(nil)

// Group returns the order group row, or ports.ErrNotFound.
func (r *GroupViewRepo) Group(ctx context.Context, orderID string, groupNo int) (network.OrderGroup, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx, `
SELECT status, destination_postal_prefix, witness_node_id, lines, updated_at
FROM order_group_views WHERE order_id = $1 AND group_no = $2`, orderID, groupNo)
	var (
		g      network.OrderGroup
		status string
		lines  []byte
	)
	if err := row.Scan(&status, &g.DestinationPostalPrefix, &g.WitnessNodeID, &lines, &g.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return network.OrderGroup{}, ports.ErrNotFound
		}
		return network.OrderGroup{}, fmt.Errorf("get order group: %w", err)
	}
	g.OrderID, g.GroupNo, g.Status = orderID, groupNo, network.GroupStatus(status)
	if err := json.Unmarshal(lines, &g.Lines); err != nil {
		return network.OrderGroup{}, fmt.Errorf("decode group lines: %w", err)
	}
	return g, nil
}

// SaveGroup upserts the group row.
func (r *GroupViewRepo) SaveGroup(ctx context.Context, g network.OrderGroup) error {
	lines, err := json.Marshal(g.Lines)
	if err != nil {
		return fmt.Errorf("encode group lines: %w", err)
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO order_group_views (order_id, group_no, status, destination_postal_prefix, witness_node_id, lines, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (order_id, group_no) DO UPDATE
SET status = EXCLUDED.status, destination_postal_prefix = EXCLUDED.destination_postal_prefix,
    witness_node_id = EXCLUDED.witness_node_id, lines = EXCLUDED.lines, updated_at = EXCLUDED.updated_at`,
		g.OrderID, g.GroupNo, string(g.Status), g.DestinationPostalPrefix, g.WitnessNodeID, lines, g.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save order group: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("save order group: %d rows", tag.RowsAffected())
	}
	return nil
}

// NetworkViewRepo is the Postgres ports.NetworkViewRepository (the
// ReturnsNetworkView's node, prefix and origin halves).
type NetworkViewRepo struct{ pool *pgxpool.Pool }

// NewNetworkViewRepo builds the repository.
func NewNetworkViewRepo(pool *pgxpool.Pool) *NetworkViewRepo {
	return &NetworkViewRepo{pool: pool}
}

var _ ports.NetworkViewRepository = (*NetworkViewRepo)(nil)

// SaveNode upserts the node row.
func (r *NetworkViewRepo) SaveNode(ctx context.Context, n network.Node) error {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO returns_network_nodes (node_id, node_type, region_id, lifecycle, updated_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (node_id) DO UPDATE
SET node_type = EXCLUDED.node_type, region_id = EXCLUDED.region_id,
    lifecycle = EXCLUDED.lifecycle, updated_at = EXCLUDED.updated_at`,
		n.NodeID, string(n.Type), n.RegionID, string(n.Lifecycle), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save node: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("save node: %d rows", tag.RowsAffected())
	}
	return nil
}

// SavePrefix upserts the prefix row; a stale assignmentVersion is a no-op.
func (r *NetworkViewRepo) SavePrefix(ctx context.Context, p network.PrefixAssignment) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO service_area_prefixes (postal_prefix, region_id, assignment_version)
VALUES ($1,$2,$3)
ON CONFLICT (postal_prefix) DO UPDATE
SET region_id = EXCLUDED.region_id, assignment_version = EXCLUDED.assignment_version
WHERE EXCLUDED.assignment_version > service_area_prefixes.assignment_version`,
		p.PostalPrefix, p.RegionID, p.AssignmentVersion)
	if err != nil {
		return fmt.Errorf("save prefix: %w", err)
	}
	return nil
}

// SaveOrigin upserts the group-origin row.
func (r *NetworkViewRepo) SaveOrigin(ctx context.Context, o network.GroupOrigin) error {
	// A nil OwnersByItem (a lock fact that arrived before any release named
	// the owners) must persist as {}, never null: the column's CHECK demands
	// an object.
	owners := []byte("{}")
	if o.OwnersByItem != nil {
		var err error
		if owners, err = json.Marshal(o.OwnersByItem); err != nil {
			return fmt.Errorf("encode owners: %w", err)
		}
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO group_origins (order_id, group_no, node_id, owners_by_item, updated_at)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (order_id, group_no) DO UPDATE
SET node_id = EXCLUDED.node_id, owners_by_item = EXCLUDED.owners_by_item, updated_at = EXCLUDED.updated_at`,
		o.OrderID, o.GroupNo, o.NodeID, owners, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save origin: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("save origin: %d rows", tag.RowsAffected())
	}
	return nil
}

// ReturnsCenterForRegion answers the region's ACTIVE returns centre with the
// lowest nodeId, or ports.ErrNotFound.
func (r *NetworkViewRepo) ReturnsCenterForRegion(ctx context.Context, regionID string) (network.Node, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx, `
SELECT node_id, node_type, region_id, lifecycle FROM returns_network_nodes
WHERE region_id = $1 AND node_type = 'RETURNS_CENTER' AND lifecycle = 'ACTIVE'
ORDER BY node_id LIMIT 1`, regionID)
	var (
		n         network.Node
		nodeType  string
		lifecycle string
	)
	if err := row.Scan(&n.NodeID, &nodeType, &n.RegionID, &lifecycle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return network.Node{}, ports.ErrNotFound
		}
		return network.Node{}, fmt.Errorf("find returns centre: %w", err)
	}
	n.Type, n.Lifecycle = network.NodeType(nodeType), network.NodeLifecycle(lifecycle)
	return n, nil
}

// RegionOfPrefix answers the prefix's region, or ports.ErrNotFound.
func (r *NetworkViewRepo) RegionOfPrefix(ctx context.Context, prefix string) (string, error) {
	var region string
	err := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT region_id FROM service_area_prefixes WHERE postal_prefix = $1`, prefix).Scan(&region)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ports.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find prefix region: %w", err)
	}
	return region, nil
}

// Origin answers the group's origin row, or ports.ErrNotFound.
func (r *NetworkViewRepo) Origin(ctx context.Context, orderID string, groupNo int) (network.GroupOrigin, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx, `
SELECT node_id, owners_by_item, updated_at FROM group_origins WHERE order_id = $1 AND group_no = $2`,
		orderID, groupNo)
	var (
		o      network.GroupOrigin
		owners []byte
	)
	if err := row.Scan(&o.NodeID, &owners, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return network.GroupOrigin{}, ports.ErrNotFound
		}
		return network.GroupOrigin{}, fmt.Errorf("get origin: %w", err)
	}
	o.OrderID, o.GroupNo = orderID, groupNo
	if err := json.Unmarshal(owners, &o.OwnersByItem); err != nil {
		return network.GroupOrigin{}, fmt.Errorf("decode owners: %w", err)
	}
	return o, nil
}

// OutcomeFactsRepo is the Postgres requests.OutcomeFacts.
type OutcomeFactsRepo struct{ pool *pgxpool.Pool }

// NewOutcomeFactsRepo builds the repository.
func NewOutcomeFactsRepo(pool *pgxpool.Pool) *OutcomeFactsRepo {
	return &OutcomeFactsRepo{pool: pool}
}

var _ ports.OutcomeFacts = (*OutcomeFactsRepo)(nil)

// Record appends the outcome fact.
func (r *OutcomeFactsRepo) Record(ctx context.Context, f requests.OutcomeFact) error {
	lines, err := json.Marshal(f.Lines)
	if err != nil {
		return fmt.Errorf("encode outcome lines: %w", err)
	}
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
INSERT INTO return_outcomes (authorization_id, node_id, received_at, lines)
VALUES ($1,$2,$3,$4)`, string(f.AuthorizationID), f.NodeID, f.ReceivedAt, lines)
	if err != nil {
		return fmt.Errorf("record outcome: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("record outcome: %d rows", tag.RowsAffected())
	}
	return nil
}

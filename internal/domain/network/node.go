package network

import "time"

// WithRegistration returns the row after a NodeRegistered fact: the
// descriptive attributes come from the fact, the lifecycle stays what a status
// fact already set. Only a node never seen before starts PENDING, so a
// registration applied again (a rebuild of the view) never demotes an ACTIVE,
// SUSPENDED or RETIRED node.
func (n Node) WithRegistration(nodeType NodeType, regionID, labelPostalPrefix string) Node {
	n.Type, n.RegionID, n.LabelPostalPrefix = nodeType, regionID, labelPostalPrefix
	if n.Lifecycle == "" {
		n.Lifecycle = NodePending
	}
	return n
}

// WithStatus returns the row after a status fact that happened at the instant
// at, and whether the fact applies. A fact older than the one that set the
// current lifecycle is stale and changes nothing: a dead letter replayed after
// a newer status must not undo it. Only the lifecycle moves; type, region and
// label prefix are never touched.
func (n Node) WithStatus(lifecycle NodeLifecycle, at time.Time) (Node, bool) {
	if at.Before(n.LifecycleAt) {
		return n, false
	}
	n.Lifecycle, n.LifecycleAt = lifecycle, at
	return n, true
}

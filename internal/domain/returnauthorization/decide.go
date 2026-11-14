package returnauthorization

// ReturnlessThresholdCents is the returnless-KEEP ceiling on the caller-
// declared total (the storefront is the only price knower; an absent price
// means KEEP is never chosen).
const ReturnlessThresholdCents int64 = 1500

// DecideInput is everything decide needs: the lines WITH their declared
// prices, the requested method, and the destinations the read models know.
type DecideInput struct {
	Lines         []Line
	Method        Method
	NearestNodeID string
	OriginNodeID  string
}

// Decide is the pure disposition function (ADR 0002). Rules, in order:
//
//  1. KEEP when the method is not PICKUP and every line carries a price and
//     the total is within ReturnlessThresholdCents.
//  2. else NEAREST_RETURNS_CENTER when the region has a returns centre.
//  3. else ORIGIN_NODE when a lock fact named the fulfilling node.
//  4. else no destination is known and the shopper may retry later.
func Decide(in DecideInput) (Disposition, error) {
	if keepEligible(in) {
		return KeepDisposition, nil
	}
	if in.NearestNodeID != "" {
		return NearestReturnsCenter, nil
	}
	if in.OriginNodeID != "" {
		return OriginNode, nil
	}
	return "", ErrNoReturnDestination
}

// keepEligible reports whether the returnless refund applies: no PICKUP, all
// prices known, total within the threshold.
func keepEligible(in DecideInput) bool {
	if in.Method == MethodPickup {
		return false
	}
	var total int64
	for _, l := range in.Lines {
		if l.UnitPriceCents == nil {
			return false
		}
		total += *l.UnitPriceCents * int64(l.Quantity)
	}
	return total <= ReturnlessThresholdCents
}

// DestinationFor resolves the destination node of the decided disposition:
// "" for KEEP, the nearest returns centre or the origin node otherwise.
func DestinationFor(d Disposition, in DecideInput) string {
	switch d {
	case KeepDisposition:
		return ""
	case NearestReturnsCenter:
		return in.NearestNodeID
	case OriginNode:
		return in.OriginNodeID
	default:
		return ""
	}
}

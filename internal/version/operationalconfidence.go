package version

// OperationalConfidenceFloor is the common first-party behavior floor for
// the v0.19 operational-confidence release. Receipt policy, work-v2
// authoring, and carried-set publication alias this one value so their rollout
// cannot drift feature by feature.
const OperationalConfidenceFloor = "0.19.0"

// ClaimReceiptFloor activates first-party producer evaluation receipts. It is
// an alias of the release-wide behavior floor, not a replacement for the older
// ProducerStampFloor that introduced produced_by itself.
const ClaimReceiptFloor = OperationalConfidenceFloor

package operational

const (
	// DefaultTimelineRows bounds the default number of timeline rows.
	DefaultTimelineRows = 100
	// MaximumTimelineRows bounds the largest permitted number of timeline rows.
	MaximumTimelineRows = 1000
	// DefaultWorkPerRow bounds the default work entries shown per timeline row.
	DefaultWorkPerRow = 16
	// MaximumWorkPerRow bounds the largest permitted work entries per row.
	MaximumWorkPerRow = 64
	// DefaultConsistency bounds the default consistency facts shown per row.
	DefaultConsistency = 32
	// MaximumConsistency bounds the largest permitted consistency facts per row.
	MaximumConsistency = 128
	// DefaultEncodedSnapshot bounds the default encoded snapshot size.
	DefaultEncodedSnapshot = 4 << 20
	// MaximumEncodedSnapshot bounds the largest permitted encoded snapshot.
	MaximumEncodedSnapshot = 16 << 20
)

// Limits bounds the encoded operational snapshot and its collections.
type Limits struct {
	TimelineRows      int
	WorkPerRow        int
	ConsistencyPerRow int
	EncodedSnapshot   int
}

// DefaultLimits returns the standard bounded operational projection limits.
func DefaultLimits() Limits {
	return Limits{
		TimelineRows: DefaultTimelineRows, WorkPerRow: DefaultWorkPerRow,
		ConsistencyPerRow: DefaultConsistency, EncodedSnapshot: DefaultEncodedSnapshot,
	}
}

func (l Limits) normalized() (Limits, error) {
	if l.TimelineRows == 0 {
		l.TimelineRows = DefaultTimelineRows
	}
	if l.WorkPerRow == 0 {
		l.WorkPerRow = DefaultWorkPerRow
	}
	if l.ConsistencyPerRow == 0 {
		l.ConsistencyPerRow = DefaultConsistency
	}
	if l.EncodedSnapshot == 0 {
		l.EncodedSnapshot = DefaultEncodedSnapshot
	}
	if l.TimelineRows < 1 || l.TimelineRows > MaximumTimelineRows ||
		l.WorkPerRow < 1 || l.WorkPerRow > MaximumWorkPerRow ||
		l.ConsistencyPerRow < 1 || l.ConsistencyPerRow > MaximumConsistency ||
		l.EncodedSnapshot < 1 || l.EncodedSnapshot > MaximumEncodedSnapshot {
		return Limits{}, ErrInvalidInput
	}
	return l, nil
}

package operational

const (
	DefaultTimelineRows    = 100
	MaximumTimelineRows    = 1000
	DefaultWorkPerRow      = 16
	MaximumWorkPerRow      = 64
	DefaultConsistency     = 32
	MaximumConsistency     = 128
	DefaultEncodedSnapshot = 4 << 20
	MaximumEncodedSnapshot = 16 << 20
)

type Limits struct {
	TimelineRows      int
	WorkPerRow        int
	ConsistencyPerRow int
	EncodedSnapshot   int
}

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

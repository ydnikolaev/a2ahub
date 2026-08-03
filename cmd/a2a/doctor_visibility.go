package main

import (
	"context"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// doctorClassificationReader adapts cache's policy-free scan to doctor's
// consumer-owned visibility seam. It never decides PASS/WARN/UNVERIFIED.
type doctorClassificationReader struct {
	store *cache.Store
}

func (r doctorClassificationReader) ClassificationSummary(ctx context.Context, spaceID string) (cli.DoctorClassificationSummary, error) {
	if r.store == nil {
		return cli.DoctorClassificationSummary{}, fmt.Errorf("classification summary: store is not configured")
	}
	summaries, err := r.store.ClassificationSummaries(ctx)
	if err != nil {
		return cli.DoctorClassificationSummary{}, err
	}
	for _, summary := range summaries {
		if summary.Space != spaceID {
			continue
		}
		paths := make([]string, 0, len(summary.Skipped)+len(summary.IncompletePaths))
		for _, skipped := range summary.Skipped {
			paths = append(paths, skipped.Path)
		}
		paths = append(paths, summary.IncompletePaths...)
		return cli.DoctorClassificationSummary{
			Highest:      summary.Highest,
			SkippedCount: summary.SkippedCount + summary.IncompleteCount,
			SkippedPaths: paths,
		}, nil
	}
	return cli.DoctorClassificationSummary{}, fmt.Errorf("classification summary: space %q is not connected", spaceID)
}

var _ cli.DoctorClassificationSummaryReader = doctorClassificationReader{}

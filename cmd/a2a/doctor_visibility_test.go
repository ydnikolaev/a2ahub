package main

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

func TestDoctorClassificationReaderNamesMissingSpace(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	_, err := (doctorClassificationReader{store: store}).ClassificationSummary(context.Background(), "missing")
	if err == nil {
		t.Fatal("missing space returned a verified empty summary")
	}
}

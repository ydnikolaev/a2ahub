package cache

import "time"

// Clock is the injected "now" source every staleness/TTL computation in
// this package uses instead of a buried time.Now() call (rails: "no
// time.Now() buried in cache logic — take a clock/now for testability").
type Clock func() time.Time

package notification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

type routeLedger struct {
	Schema int              `json:"schema"`
	Routes map[string]Route `json:"routes"`
}

func saveRoutes(ctx context.Context, path string, incoming []Route) error {
	return withFileLock(ctx, path, func() error {
		ledger := routeLedger{Schema: SchemaVersion, Routes: map[string]Route{}}
		if err := readJSON(path, &ledger); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if ledger.Routes == nil {
			ledger.Routes = map[string]Route{}
		}
		for _, route := range incoming {
			ledger.Routes[route.Token] = route
		}
		if len(ledger.Routes) > 1024 {
			type pair struct {
				token string
				at    time.Time
			}
			pairs := make([]pair, 0, len(ledger.Routes))
			for token, route := range ledger.Routes {
				pairs = append(pairs, pair{token: token, at: route.CreatedAt})
			}
			sort.Slice(pairs, func(i, j int) bool {
				if pairs[i].at.Equal(pairs[j].at) {
					return pairs[i].token < pairs[j].token
				}
				return pairs[i].at.Before(pairs[j].at)
			})
			for _, p := range pairs[:len(pairs)-1024] {
				delete(ledger.Routes, p.token)
			}
		}
		return writeJSON(path, ledger)
	})
}

func resolveRoute(path, token string) (Route, error) {
	if len(token) != len("rt_")+32 || token[:3] != "rt_" {
		return Route{}, ErrRouteNotFound
	}
	var ledger routeLedger
	if err := readJSON(path, &ledger); err != nil {
		return Route{}, err
	}
	route, ok := ledger.Routes[token]
	if !ok {
		return Route{}, fmt.Errorf("%w: %s", ErrRouteNotFound, token)
	}
	return route, nil
}

// ErrRouteNotFound rejects unknown or malformed trusted-route tokens.
var ErrRouteNotFound = errors.New("notification route not found")

// Package skillcoverage derives the JSON key set a Go type serializes, by
// reflection over its exported/tagged fields — never a hand-maintained
// list. It exists so `a2a __catalog --surfaces --json` (cmd/a2a/catalog.go)
// can build the prose-coverage ledger's field universe (spec 13
// agent-exchange-2026-08 §3.1) from the SAME types the read surfaces
// actually marshal, the same "ask the binary" discipline
// fold.BuildVocabulary already keeps for outcomes/states/transitions
// (internal/fold/vocabulary.go).
//
// This package owns only the walk. It does not know what a "surface" is —
// cmd/a2a/catalog.go owns the surface-name-to-type mapping — and it is
// deliberately independent of internal/cache, internal/mcp or any other
// domain package: a type from ANY package can be handed to SurfaceKeys.
package skillcoverage

import (
	"reflect"
	"sort"
	"strings"
)

// SurfaceKeys returns the sorted, deduplicated set of every JSON object key
// t — or anything reachable from t through a struct field, a pointer, a
// slice/array element, or a map value — can put on the wire, honouring
// `json:"-"` and encoding/json's own "an untagged embedded struct inlines
// its fields into the parent object" rule.
//
// It is a FLAT set, not a set of paths: a key name reachable at any depth
// is reported once, regardless of how many places in the graph produce it
// or how deep. That is a deliberate simplification — see
// schemas/prose-coverage.yaml's own header for the consequence (a generic
// name like "name" collapses several distinct meanings into one universe
// member) — traded for a universe small enough to hand-classify, matching
// what the ledger this feeds actually needs: "does ANY read surface emit
// this key", not "at what path".
//
// t must be a struct type, or a type reachable to one through the same
// pointer/slice/array/map unwrapping this function itself applies —
// passing anything else (a bare string type, an interface, etc.) returns
// an empty set, never a panic.
//
// A type implementing json.Marshaler is NOT detected or special-cased:
// SurfaceKeys reads struct tags only, and never invokes MarshalJSON. A
// type in the graph with a custom marshaler makes this function's answer
// for that subtree fictional in the general case — but a CONDITIONAL
// OMISSION (a key some values never put on the wire, but the type CAN)
// stays a safe over-approximation rather than a wrong one. None of the
// types cmd/a2a/catalog.go feeds in as of this writing implement
// MarshalJSON (verified by hand: `grep -rn "func.*MarshalJSON"
// internal/cache internal/datapackage internal/provenance
// internal/workreport internal/fold` finds exactly one, datapackage.Report,
// which is not reachable from cache.Item, cache.ThreadResult or
// cache.ShowResult).
//
// internal/cli.ContractVerifyExportResult (fed to this function by
// internal/cli/cmd_contract_p6_test.go's TestRenderLedgerSurfaceDump, the
// render-ledger gate's own driver — answers-that-hold-2026-08 P3) is the
// FIRST caller to feed in a type that DOES implement MarshalJSON: it omits
// "matches" when Outcome is "unmeasured" (AC-9). That is exactly the safe
// case above — the derived set still includes "matches" unconditionally,
// which remains correct as "a key this type can emit", never a key it
// never emits. A caller adding a type whose MarshalJSON RENAMES or ADDS a
// key (rather than only ever conditionally DROPPING one already covered by
// a struct tag) must re-verify this reasoning still holds.
func SurfaceKeys(t reflect.Type) []string {
	seen := map[string]bool{}
	walk(t, seen, map[reflect.Type]bool{})
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// walk unwraps t through pointer/slice/array/map layers to the underlying
// struct (if any) and records every field's JSON key into seen, recursing
// into each field's own (equally unwrapped) type. visiting guards against
// a self-referential type looping forever; it is scoped to one top-level
// SurfaceKeys call — a struct reached twice from two different branches of
// the SAME call is walked only once, since its own key set cannot change
// between visits.
func walk(t reflect.Type, seen map[string]bool, visiting map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	if visiting[t] {
		return
	}
	visiting[t] = true

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported field: encoding/json never reaches it
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue // explicitly excluded from the wire
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			// An untagged embed is INLINED by encoding/json — its own
			// fields land directly in the parent object, under no key of
			// their own — so nothing is added here, but the embedded
			// type's fields are still reachable and must still be walked.
			walk(f.Type, seen, visiting)
			continue
		}
		if name == "" {
			name = f.Name
		}
		seen[name] = true
		walk(f.Type, seen, visiting)
	}
}

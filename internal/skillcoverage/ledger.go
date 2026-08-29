// ledger.go — the surface REGISTRY answers-that-hold-2026-08 P3 spec 03
// §T1's "THE SECOND TRAP" names: cmd/a2a/catalog.go's catalogSurfaces() was
// a HAND-WRITTEN map of five entries, and its own doc comment confessed the
// hole verbatim — "a newly added SURFACE is not caught; only a newly added
// FIELD on an already-listed one is." P7 shipped a new result type
// (internal/cli's ContractVerifyPublishedResult) into exactly that hole.
//
// Register lets a caller enroll a (name, type) pair AT THE POINT the --json
// arm is defined (or, for a type in a package this one must stay
// independent of — see reflect.go's own package doc comment — anywhere
// that already imports it, same-package or not); Registered/WithRegistered
// let cmd/a2a/catalog.go fold every registration into the map it already
// returns, so registering a NEW surface never requires a second edit here.
// This package still does not know what a "surface" is — it owns the
// registry and the walk, never the surface-name-to-type mapping's meaning
// — matching reflect.go's own "below the DI root" discipline.
package skillcoverage

import "reflect"

// registry accumulates every (name, type) pair Register has recorded, for
// the lifetime of one process. Package-level and mutated only by init()
// functions in this process's own import graph (the same "collected at
// program start, read after" shape fold.BuildVocabulary's callers already
// rely on) — never touched after any Registered()/WithRegistered() call a
// gate or a test depends on, so there is no concurrent-write hazard to
// guard here.
var registry = map[string]reflect.Type{}

// Register enrolls name -> t. Two registrations under the same name are a
// caller bug, not a runtime condition to tolerate silently — it panics at
// init() time (the same "surface fail fast" precedent
// cmd/a2a/catalog.go's own catalogCommandRows applies to a missing
// dispatch-verb case), so a copy-pasted registration is caught the moment
// the binary that carries it first runs, never discovered later as two
// answers to "what does surface X look like".
func Register(name string, t reflect.Type) {
	if _, exists := registry[name]; exists {
		panic("skillcoverage: surface " + name + " is already registered")
	}
	registry[name] = t
}

// Registered returns every Register()ed surface, each already walked to
// its JSON key set via SurfaceKeys — never a second, hand-maintained field
// list. The returned map is a fresh copy; mutating it never affects the
// registry.
func Registered() map[string][]string {
	out := make(map[string][]string, len(registry))
	for name, t := range registry {
		out[name] = SurfaceKeys(t)
	}
	return out
}

// WithRegistered returns a new map containing every entry of base PLUS
// every Register()ed surface — base is never mutated. This is
// cmd/a2a/catalog.go's own catalogSurfaces() change: wrap its existing
// five-entry map literal in this call instead of hand-adding a sixth (and
// a seventh, and an eighth) entry every time a new --json arm registers
// itself. A name present in BOTH base and the registry is a caller bug
// exactly like a double Register() call — it panics for the same reason:
// two definitions of one surface name is silent data loss for whichever
// one a naive merge would have dropped.
func WithRegistered(base map[string][]string) map[string][]string {
	out := make(map[string][]string, len(base)+len(registry))
	for name, keys := range base {
		out[name] = keys
	}
	for name, t := range registry {
		if _, exists := out[name]; exists {
			panic("skillcoverage: surface " + name + " is registered AND hand-listed — pick one")
		}
		out[name] = SurfaceKeys(t)
	}
	return out
}

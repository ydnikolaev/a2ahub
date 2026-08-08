package html

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// transcriptEventFieldsThisPackageDeliberatelyDrops names the
// cache.TranscriptEvent fields html.TranscriptEvent does not carry under the
// same name, each with the reason it is a decision.
var transcriptEventFieldsThisPackageDeliberatelyDrops = map[string]string{
	"Actor": "reshaped, not dropped: cache.TranscriptEventActor is a struct, and this projection emits a map[string]any so the browser sees only the keys that are actually populated (assemble.go builds it field by field, omitting empty model/session)",
}

// TestTranscriptEventCarriesEveryCacheField is the fourth projection gate,
// and it exists because its absence was found by an audit rather than by a
// test.
//
// P0 added `reason_code` and `transition_free` to cache.TranscriptEvent and
// both were silently dropped here: html.TranscriptEvent is hand-built in
// assemble.go, field by field, and Go is perfectly happy for the two types to
// diverge. The Item and OpenItem gates stayed green throughout, because they
// guard different types entirely — so the dashboard simply never received two
// fields the phase had just spent a commit carrying to the cache.
//
// That is the same failure the other three gates were written for, one layer
// up, and the lesson is the one those gates already state: a hand-built
// projection between two structs is a place fields go missing without a
// compile error, a red test, or any other signal.
//
// Names, not types or tags — same convention as the sibling gates: a rename
// on either side is a real event worth failing on, while the JSON tag is this
// package's own choice.
func TestTranscriptEventCarriesEveryCacheField(t *testing.T) {
	t.Parallel()

	source := exportedNames(reflect.TypeOf(cache.TranscriptEvent{}))
	projection := map[string]bool{}
	for _, name := range exportedNames(reflect.TypeOf(TranscriptEvent{})) {
		projection[name] = true
	}

	if len(source) == 0 || len(projection) == 0 {
		t.Fatal("one of the two types has no exported fields — reflection found nothing to compare and a green result here would mean nothing")
	}

	var missing []string
	for _, name := range source {
		if projection[name] {
			continue
		}
		if reason, deliberate := transcriptEventFieldsThisPackageDeliberatelyDrops[name]; deliberate {
			t.Logf("cache.TranscriptEvent.%s is deliberately not projected under that name: %s", name, reason)
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("html.TranscriptEvent drops %d field(s) of cache.TranscriptEvent: %s\n"+
			"assemble.go builds this projection by hand, so a dropped field reaches the dashboard as absence with nothing failing.\n"+
			"Either carry it, or add it to transcriptEventFieldsThisPackageDeliberatelyDrops with the reason it is a decision.",
			len(missing), strings.Join(missing, ", "))
	}
}

// TestTranscriptEventDropListNamesOnlyRealFields keeps the exemption honest.
// A stale entry reads as a considered decision forever while guarding
// nothing.
func TestTranscriptEventDropListNamesOnlyRealFields(t *testing.T) {
	t.Parallel()

	real := map[string]bool{}
	for _, name := range exportedNames(reflect.TypeOf(cache.TranscriptEvent{})) {
		real[name] = true
	}
	for name := range transcriptEventFieldsThisPackageDeliberatelyDrops {
		if !real[name] {
			t.Errorf("transcriptEventFieldsThisPackageDeliberatelyDrops names %q, which cache.TranscriptEvent no longer has — delete the entry rather than leaving a dead exemption", name)
		}
	}
}

func exportedNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}

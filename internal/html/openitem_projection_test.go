package html

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// TestThreadOpenItemCarriesEveryOpenItemField is the drift gate for the
// projection that produced the P11 W2 defect.
//
// internal/html keeps its OWN open-item type. That is legitimate — it renders
// a page and carries page-only facts (Prompt, Pending) the read model has no
// business knowing. What is not legitimate is the projection SILENTLY LOSING
// fields, and it had: `expected_transition`, `why` and `human_gate` all
// existed on cache.OpenItem and reached `a2a thread --json`, while the
// dashboard dropped them. The dashboard could say who owed a move but not
// which move or why, and after internal/pendency learned about G3 it still
// could not tell an agent that approving a decision is a human gate whose
// event the fold ignores — the exact "a surface names a move the tool would
// refuse" failure threads.md promises cannot happen.
//
// Nothing caught it. Both types compiled, both tests passed, and the loss was
// found only by rendering a real space and diffing the JSON by eye. This
// asserts the containment by reflection instead, so the NEXT field added to
// cache.OpenItem cannot reach `a2a thread` and quietly miss `a2a html`.
//
// It deliberately checks names, not types or tags: a rename on either side is
// a real event worth failing on, while the JSON tag is this package's own
// choice (cache uses `expected_transition`; a page could reasonably differ).
func TestThreadOpenItemCarriesEveryOpenItemField(t *testing.T) {
	t.Parallel()

	source := fieldNames(reflect.TypeOf(cache.OpenItem{}))
	projection := map[string]bool{}
	for _, name := range fieldNames(reflect.TypeOf(ThreadOpenItem{})) {
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
		if reason, deliberate := openItemFieldsThisPackageDeliberatelyDrops[name]; deliberate {
			t.Logf("cache.OpenItem.%s is deliberately not projected: %s", name, reason)
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("ThreadOpenItem drops %d field(s) of cache.OpenItem: %s\n"+
			"Every one of these reaches `a2a thread --json` and would silently not reach `a2a html`. "+
			"Either carry it, or add it to openItemFieldsThisPackageDeliberatelyDrops with the reason it is a decision.",
			len(missing), strings.Join(missing, ", "))
	}
}

func fieldNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}

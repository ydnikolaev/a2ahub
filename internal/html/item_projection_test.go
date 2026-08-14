package html

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// TestItemCarriesEveryCacheItemField is the drift gate for the projection
// cache.Item -> html.Item. internal/html/openitem_projection_test.go already
// guards cache.OpenItem -> html.ThreadOpenItem by reflection; nothing guarded
// this half, and that is exactly how a field can be added to cache.Item and
// never reach the dashboard without a single compile error or red test
// anywhere — both types are free to diverge in Go, and the loss is only
// found by rendering a real space and diffing the JSON by eye (same failure
// mode openitem_projection_test.go's own doc comment names).
//
// It deliberately checks names, not types or tags, for the same reason that
// file does: a rename on either side is a real event worth failing on, while
// the JSON tag is this package's own choice.
func TestItemCarriesEveryCacheItemField(t *testing.T) {
	t.Parallel()

	source := fieldNames(reflect.TypeOf(cache.Item{}))
	projection := map[string]bool{}
	for _, name := range fieldNames(reflect.TypeOf(Item{})) {
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
		if reason, deliberate := itemFieldsThisPackageDeliberatelyDrops[name]; deliberate {
			t.Logf("cache.Item.%s is deliberately not projected under that name: %s", name, reason)
			continue
		}
		missing = append(missing, name)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("Item drops %d field(s) of cache.Item: %s\n"+
			"Either carry it (under the same name), or add it to itemFieldsThisPackageDeliberatelyDrops with the reason it is a decision.",
			len(missing), strings.Join(missing, ", "))
	}
}

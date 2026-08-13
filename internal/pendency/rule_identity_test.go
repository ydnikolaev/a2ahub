package pendency

import (
	"reflect"
	"sort"
	"testing"
)

func TestEveryVerdictCarriesItsTypedRuleIdentity(t *testing.T) {
	t.Parallel()

	wantSet := make(map[RuleIdentity]bool, len(table))
	for key, row := range table {
		want := RuleIdentity(string(key.Kind) + "/" + string(key.State))
		if row.identity != want {
			t.Errorf("row %s/%s identity = %q, want %q", key.Kind, key.State, row.identity, want)
		}
		if wantSet[row.identity] {
			t.Errorf("duplicate rule identity %q", row.identity)
		}
		wantSet[row.identity] = true
		if !row.identity.Known() {
			t.Errorf("RuleIdentity(%q).Known() = false for table row", row.identity)
		}

		verdict, err := Resolve(Input{Kind: key.Kind, State: key.State})
		if err != nil {
			t.Errorf("Resolve(%s/%s): %v", key.Kind, key.State, err)
			continue
		}
		if verdict.RuleIdentity != row.identity {
			t.Errorf("Resolve(%s/%s).RuleIdentity = %q, want row identity %q",
				key.Kind, key.State, verdict.RuleIdentity, row.identity)
		}
	}

	want := make([]RuleIdentity, 0, len(wantSet))
	for identity := range wantSet {
		want = append(want, identity)
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if got := RuleIdentities(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RuleIdentities() = %v, want table-derived %v", got, want)
	}
	for _, identity := range []RuleIdentity{"", "future/unknown"} {
		if identity.Known() {
			t.Errorf("RuleIdentity(%q).Known() = true outside the table", identity)
		}
	}
}

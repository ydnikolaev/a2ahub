package contract

import (
	"errors"
	"testing"
)

// TestDecodeManifestPolicyKeys pins the one decoder both sides of the gates
// policy read space.yaml's `gates` and `flow` keys through (spec 04 section 10
// "P4-B carries" item 8): the plain forms decode to the raw values, and every
// form on which two YAML readers could disagree is refused rather than
// interpreted.
func TestDecodeManifestPolicyKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantGates any
		wantFlow  any
		wantErr   error
	}{
		{name: "both keys", raw: "space: s\ngates: confirm\nflow: express\n", wantGates: "confirm", wantFlow: "express"},
		{name: "quoted keys and values", raw: "\"gates\": \"autonomous\"\n'flow': 'full'\n", wantGates: "autonomous", wantFlow: "full"},
		{name: "absent keys", raw: "space: s\n"},
		{name: "explicit null", raw: "gates: null\nflow: ~\n"},
		{name: "a number is returned raw for the vocabulary check", raw: "gates: 7\n", wantGates: 7},
		{name: "a mapping is returned raw for the vocabulary check", raw: "gates:\n  mode: confirm\n", wantGates: map[string]any{"mode": "confirm"}},
		{name: "an anchor on the value alone is not an alias", raw: "gates: &g confirm\n", wantGates: "confirm"},
		{name: "aliases elsewhere in the document are not this decoder's business", raw: "base: &b {x: 1}\nother: *b\ngates: confirm\n", wantGates: "confirm"},
		{name: "a quoted << key is an ordinary string key", raw: "\"<<\": x\ngates: confirm\n", wantGates: "confirm"},

		{name: "a top-level merge key", raw: "base: &b\n  gates: autonomous\n<<: *b\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "a merge key with a list of aliases", raw: "a: &a {gates: autonomous}\nb: &b {flow: express}\n<<: [*a, *b]\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "a merge key that carries no policy key is still refused", raw: "base: &b {x: 1}\n<<: *b\ngates: confirm\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "an alias as the gates value", raw: "x: &a autonomous\ngates: *a\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "an alias as the flow value", raw: "x: &a express\nflow: *a\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "an alias nested in the gates value", raw: "x: &a autonomous\ngates: {mode: *a}\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "an alias nested in a sequence value", raw: "x: &a autonomous\nflow: [*a]\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "a duplicated gates key", raw: "gates: confirm\ngates: autonomous\n", wantErr: ErrManifestPolicyKeysAmbiguous},
		{name: "a duplicated flow key", raw: "flow: full\nflow: express\n", wantErr: ErrManifestPolicyKeysAmbiguous},

		{name: "not YAML", raw: "schema: [unclosed\n", wantErr: ErrManifestPolicyKeysUnreadable},
		{name: "an empty document", raw: "", wantErr: ErrManifestPolicyKeysUnreadable},
		{name: "a sequence", raw: "- a\n- b\n", wantErr: ErrManifestPolicyKeysUnreadable},
		{name: "a bare scalar", raw: "just text\n", wantErr: ErrManifestPolicyKeysUnreadable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			keys, err := DecodeManifestPolicyKeys([]byte(tt.raw))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("DecodeManifestPolicyKeys(%q) error = %v, want %v", tt.raw, err, tt.wantErr)
				}
				if keys.Gates != nil || keys.Flow != nil {
					t.Fatalf("DecodeManifestPolicyKeys(%q) = %+v with an error, want zero keys", tt.raw, keys)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeManifestPolicyKeys(%q) error = %v, want none", tt.raw, err)
			}
			if !sameRaw(keys.Gates, tt.wantGates) || !sameRaw(keys.Flow, tt.wantFlow) {
				t.Fatalf("DecodeManifestPolicyKeys(%q) = {gates %#v, flow %#v}, want {gates %#v, flow %#v}",
					tt.raw, keys.Gates, keys.Flow, tt.wantGates, tt.wantFlow)
			}
		})
	}
}

// sameRaw compares the decoded values the table uses: scalars by ==, and the one
// mapping row by its single entry.
func sameRaw(got, want any) bool {
	if wantMap, ok := want.(map[string]any); ok {
		gotMap, isMap := got.(map[string]any)
		if !isMap || len(gotMap) != len(wantMap) {
			return false
		}
		for key, value := range wantMap {
			if gotMap[key] != value {
				return false
			}
		}
		return true
	}
	return got == want
}

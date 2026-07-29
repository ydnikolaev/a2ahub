package operation

import "testing"

func TestRespondCanonicalAndSemantic(t *testing.T) {
	t.Parallel()

	a := Respond("axon", "agent", "bot", []string{"B", "A"}, "answered",
		map[string]string{"title": "done", "priority": "p1"}, []byte("body"))
	b := Respond("axon", "agent", "bot", []string{"A", "B"}, "answered",
		map[string]string{"priority": "p1", "title": "done"}, []byte("body"))
	if a != b || !Valid(a) {
		t.Fatalf("canonical keys differ or are invalid: %q %q", a, b)
	}
	if changed := Respond("axon", "agent", "bot", []string{"A", "B"}, "answered",
		map[string]string{"priority": "p1", "title": "done"}, []byte("different")); changed == a {
		t.Fatal("changed body produced the same operation key")
	}
}

func TestContractDeprecateIncludesSuccessor(t *testing.T) {
	t.Parallel()

	a := ContractDeprecate("axon", "XC-axon-widget", "1.0.0", "XC-axon-widget@2.0.0", "2026-12-31")
	b := ContractDeprecate("axon", "XC-axon-widget", "1.0.0", "XC-axon-widget@3.0.0", "2026-12-31")
	if a == b {
		t.Fatal("changed successor produced the same operation key")
	}
	if !Valid(a) || Valid("op-v1-nope") {
		t.Fatalf("Valid accepted/rejected the wrong key: %q", a)
	}
}

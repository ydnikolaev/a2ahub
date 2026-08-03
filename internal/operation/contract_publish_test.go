package operation

import "testing"

func TestContractPublishGoldenVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "semantic candidate intent",
			got: ContractPublishIntent(
				"atlas",
				"XC-atlas-demo",
				"auto:major",
				"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			),
			want: "op-v1-ff0642d00fb872bb963881c43fa7bd29b12c52916814e58504bd4d3ab4abe24b",
		},
		{
			name: "resolved target",
			got:  ContractPublish("atlas", "XC-atlas-demo", "2.0.0"),
			want: "op-v1-5b33cff5ae84b0af9fea078e2a52d6c5aec259f07853761f93f78a0aff94346f",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.got != test.want {
				t.Fatalf("key = %q, want %q", test.got, test.want)
			}
			if !Valid(test.got) {
				t.Fatalf("key %q is not canonical", test.got)
			}
		})
	}
}

func TestContractPublishTupleAndDomainSeparation(t *testing.T) {
	t.Parallel()

	const digest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	intent := ContractPublishIntent("atlas", "XC-atlas-demo", "auto:major", digest)
	intentChanges := []string{
		ContractPublishIntent("atlas-2", "XC-atlas-demo", "auto:major", digest),
		ContractPublishIntent("atlas", "XC-atlas-other", "auto:major", digest),
		ContractPublishIntent("atlas", "XC-atlas-demo", "auto:minor", digest),
		ContractPublishIntent("atlas", "XC-atlas-demo", "auto:major", "sha256:"+digest[len("sha256:")+1:]+"0"),
	}
	for i, changed := range intentChanges {
		if changed == intent {
			t.Fatalf("intent tuple change %d collided with %q", i, intent)
		}
	}

	target := ContractPublish("atlas", "XC-atlas-demo", "2.0.0")
	targetChanges := []string{
		ContractPublish("atlas-2", "XC-atlas-demo", "2.0.0"),
		ContractPublish("atlas", "XC-atlas-other", "2.0.0"),
		ContractPublish("atlas", "XC-atlas-demo", "2.0.1"),
	}
	for i, changed := range targetChanges {
		if changed == target {
			t.Fatalf("target tuple change %d collided with %q", i, target)
		}
	}
	if intent == target {
		t.Fatalf("contract-publish-intent and contract-publish domains collided: %q", intent)
	}
}

func TestBranchNameIsSharedProtocol(t *testing.T) {
	t.Parallel()

	got := BranchName("atlas", "contract-publish", "op-v1-abc")
	if got != "a2a/atlas/contract-publish/op-v1-abc" {
		t.Fatalf("BranchName = %q", got)
	}
}

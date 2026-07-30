package e2e

import "testing"

func TestTestListRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "equals form", args: []string{"-test.list=^Test"}, want: true},
		{name: "separate value form", args: []string{"-test.list", "^Test"}, want: true},
		{name: "ordinary run", args: []string{"-test.count=1", "-test.v"}, want: false},
		{name: "similar user flag", args: []string{"--list"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := testListRequested(tc.args); got != tc.want {
				t.Fatalf("testListRequested(%q) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

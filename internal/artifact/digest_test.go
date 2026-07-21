package artifact

import "testing"

func TestDigest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{
			// echo -n "hello a2ahub" | sha256sum
			name: "known fixture",
			in:   []byte("hello a2ahub"),
			want: "sha256:7c83c018bbff98590ca7792b0c65b8190e44071070283f1e3b9bd97923d6d0de",
		},
		{
			// sha256sum of the empty string, per NIST test vectors.
			name: "empty input",
			in:   []byte(""),
			want: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Digest(tc.in)
			if got != tc.want {
				t.Fatalf("Digest(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if len(got) != len("sha256:")+64 {
				t.Fatalf("Digest(%q) length = %d, want %d (never truncated)", tc.in, len(got), len("sha256:")+64)
			}
		})
	}
}

package workreport

import (
	"reflect"
	"testing"
)

func TestModes(t *testing.T) {
	t.Parallel()
	want := []Mode{
		ModePlanning,
		ModeImplementing,
		ModeTesting,
		ModeReviewing,
		ModeWaiting,
		ModePaused,
		ModeFinished,
	}
	got := Modes()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Modes() = %v, want %v", got, want)
	}
	if len(got) == 0 {
		t.Fatal("Modes() returned no values")
	}
	got[0] = got[len(got)-1]
	if fresh := Modes(); !reflect.DeepEqual(fresh, want) {
		t.Errorf("Modes() after caller mutation = %v, want fresh %v", fresh, want)
	}
	for _, mode := range want {
		listed := containsMode(Modes(), mode)
		if valid := mode.Valid(); listed != valid {
			t.Errorf("mode %q: Modes lists %t, Mode.Valid reports %t", mode, listed, valid)
		}
	}
	invalid := Mode("designing")
	if invalid.Valid() {
		t.Errorf("Mode(%q).Valid() = true, want false for an unlisted sentinel", invalid)
	}
}

func containsMode(modes []Mode, target Mode) bool {
	for _, mode := range modes {
		if mode == target {
			return true
		}
	}
	return false
}

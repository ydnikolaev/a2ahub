package validate

import (
	"reflect"
	"testing"
)

func TestValidateWorkCheckpointV2V3Parity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		edit func(*WorkCheckpointInput)
	}{
		{name: "valid", edit: func(*WorkCheckpointInput) {}},
		{name: "policy rejection", edit: func(in *WorkCheckpointInput) { in.Category = "notice" }},
		{name: "referential rejection", edit: func(in *WorkCheckpointInput) { in.Work.SubjectRef = "not-a-ref" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			v2 := validWorkCheckpointInput()
			test.edit(&v2)
			v2.InvocationPoint = V2
			v3 := v2
			v3.InvocationPoint = V3

			gotV2 := ValidateWorkCheckpoint(v2)
			gotV3 := ValidateWorkCheckpoint(v3)
			if gotV2.Valid != gotV3.Valid {
				t.Fatalf("V2/V3 validity differs: V2=%+v V3=%+v", gotV2, gotV3)
			}
			if !reflect.DeepEqual(gotV2.Violations, gotV3.Violations) {
				t.Fatalf("V2/V3 violations differ: V2=%+v V3=%+v", gotV2.Violations, gotV3.Violations)
			}
		})
	}
}

func TestValidateWorkCheckpointPolicyMatrix(t *testing.T) {
	t.Parallel()

	present := "present"
	tests := []struct {
		name string
		edit func(*WorkCheckpointInput)
		path string
	}{
		{name: "V1 invocation", edit: func(in *WorkCheckpointInput) { in.InvocationPoint = V1 }, path: "invocation_point"},
		{name: "non-announcement", edit: func(in *WorkCheckpointInput) { in.Type = "question" }, path: "type"},
		{name: "non-status category", edit: func(in *WorkCheckpointInput) { in.Category = "notice" }, path: "category"},
		{name: "blocking", edit: func(in *WorkCheckpointInput) { in.Blocking = true }, path: "blocking"},
		{name: "ack requested", edit: func(in *WorkCheckpointInput) { in.AckRequested = true }, path: "ack_requested"},
		{name: "top-level valid until", edit: func(in *WorkCheckpointInput) { in.ValidUntil = &present }, path: "valid_until"},
		{name: "deprecates", edit: func(in *WorkCheckpointInput) { in.Deprecates = &present }, path: "deprecates"},
		{name: "period", edit: func(in *WorkCheckpointInput) { in.Period = &present }, path: "period"},
		{name: "missing first-party session", edit: func(in *WorkCheckpointInput) { in.Actor.Session = "" }, path: "actor.session"},
		{name: "bad reported time", edit: func(in *WorkCheckpointInput) { in.Work.ReportedAt = "not-a-time" }, path: "work.reported_at"},
		{name: "non-UTC reported time", edit: func(in *WorkCheckpointInput) { in.Work.ReportedAt = "2026-08-03T12:00:00+02:00" }, path: "work.reported_at"},
		{name: "unknown-offset reported time", edit: func(in *WorkCheckpointInput) { in.Work.ReportedAt = "2026-08-03T12:00:00-00:00" }, path: "work.reported_at"},
		{name: "missing active valid until", edit: func(in *WorkCheckpointInput) { in.Work.ValidUntil = nil }, path: "work.valid_until"},
		{name: "bad valid until", edit: func(in *WorkCheckpointInput) { value := "not-a-time"; in.Work.ValidUntil = &value }, path: "work.valid_until"},
		{name: "valid until equal", edit: func(in *WorkCheckpointInput) { value := in.Work.ReportedAt; in.Work.ValidUntil = &value }, path: "work.valid_until"},
		{name: "valid beyond seven days", edit: func(in *WorkCheckpointInput) { value := "2026-08-10T10:00:01Z"; in.Work.ValidUntil = &value }, path: "work.valid_until"},
		{name: "finished carries valid until", edit: func(in *WorkCheckpointInput) { in.Work.Mode = WorkModeFinished }, path: "work.valid_until"},
		{name: "new sequence not one", edit: func(in *WorkCheckpointInput) { in.Work.SemanticSequence = 2 }, path: "work.semantic_sequence"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			in := validWorkCheckpointInput()
			test.edit(&in)
			assertOnlyWorkViolation(t, ValidateWorkCheckpoint(in), "POL-015", test.path)
		})
	}

	t.Run("third-party session is contextual schema territory", func(t *testing.T) {
		t.Parallel()
		in := validWorkCheckpointInput()
		in.FirstParty = false
		in.Actor.Session = ""
		if got := ValidateWorkCheckpoint(in); !got.Valid {
			t.Fatalf("third-party contextual validation rejected empty session: %+v", got.Violations)
		}
	})

	t.Run("finished forbids valid until but is otherwise valid", func(t *testing.T) {
		t.Parallel()
		in := validWorkCheckpointInput()
		in.Work.Mode = WorkModeFinished
		in.Work.ValidUntil = nil
		if got := ValidateWorkCheckpoint(in); !got.Valid {
			t.Fatalf("finished checkpoint rejected: %+v", got.Violations)
		}
	})
}

func TestValidateWorkCheckpointContinuationContext(t *testing.T) {
	t.Parallel()

	basePrevious := WorkCheckpointPrevious{
		WorkID:           "work:01K1Z6Y8Q9C7G5M3N2P4R6T8VW",
		SemanticSequence: 3,
		Owner: WorkCheckpointOwner{
			Space: "demo", Thread: "thread:axon-20260803-abcd",
			System: "axon", Name: "codex", Session: "session-1",
		},
		Mode: WorkModeImplementing, Recipients: []string{"beta"}, Classification: "internal",
	}
	tests := []struct {
		name string
		edit func(*WorkCheckpointInput)
		path string
	}{
		{name: "work id changes", edit: func(in *WorkCheckpointInput) { in.Work.ID = "work:01K1Z6Y8Q9C7G5M3N2P4R6T8VX" }, path: "work.id"},
		{name: "space changes", edit: func(in *WorkCheckpointInput) { in.Space = "other" }, path: "space"},
		{name: "thread changes", edit: func(in *WorkCheckpointInput) {
			in.Thread = "thread:axon-20260804-bcde"
			in.ResolvedSubjects[0].Thread = in.Thread
		}, path: "thread"},
		{name: "system changes", edit: func(in *WorkCheckpointInput) { in.Actor.System = "beta" }, path: "actor.system"},
		{name: "name changes", edit: func(in *WorkCheckpointInput) { in.Actor.Name = "other" }, path: "actor.name"},
		{name: "session changes", edit: func(in *WorkCheckpointInput) { in.Actor.Session = "session-2" }, path: "actor.session"},
		{name: "audience broadens", edit: func(in *WorkCheckpointInput) { in.Recipients = []string{"all"} }, path: "to"},
		{name: "audience changes", edit: func(in *WorkCheckpointInput) { in.Recipients = []string{"axon"} }, path: "to"},
		{name: "classification changes", edit: func(in *WorkCheckpointInput) { in.Classification = "restricted" }, path: "classification"},
		{name: "sequence repeats", edit: func(in *WorkCheckpointInput) { in.Work.SemanticSequence = 3 }, path: "work.semantic_sequence"},
		{name: "sequence skips", edit: func(in *WorkCheckpointInput) { in.Work.SemanticSequence = 5 }, path: "work.semantic_sequence"},
		{name: "finished stream continues", edit: func(in *WorkCheckpointInput) { in.Previous.Mode = WorkModeFinished }, path: "work.mode"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, point := range []InvocationPoint{V2, V3} {
				t.Run(string(point), func(t *testing.T) {
					t.Parallel()
					in := validWorkCheckpointInput()
					in.InvocationPoint = point
					in.Previous = cloneWorkPrevious(basePrevious)
					in.Work.SemanticSequence = 4
					test.edit(&in)
					assertOnlyWorkViolation(t, ValidateWorkCheckpoint(in), "POL-015", test.path)
				})
			}
		})
	}

	t.Run("exact next checkpoint is valid", func(t *testing.T) {
		t.Parallel()
		for _, point := range []InvocationPoint{V2, V3} {
			in := validWorkCheckpointInput()
			in.InvocationPoint = point
			in.Previous = cloneWorkPrevious(basePrevious)
			in.Work.SemanticSequence = 4
			if got := ValidateWorkCheckpoint(in); !got.Valid {
				t.Fatalf("valid %s continuation rejected: %+v", point, got.Violations)
			}
		}
	})
}

func TestValidateWorkCheckpointReferenceMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*WorkCheckpointInput)
		code string
		path string
	}{
		{name: "malformed subject", edit: func(in *WorkCheckpointInput) { in.Work.SubjectRef = "../../secret" }, code: "REF-016", path: "work.subject_ref"},
		{name: "other thread subject", edit: func(in *WorkCheckpointInput) { in.Work.SubjectRef = "thread:axon-20260804-bcde" }, code: "REF-016", path: "work.subject_ref"},
		{name: "artifact absent from refs", edit: func(in *WorkCheckpointInput) { in.Refs = nil }, code: "REF-016", path: "work.subject_ref"},
		{name: "artifact unresolved", edit: func(in *WorkCheckpointInput) { in.ResolvedSubjects = nil }, code: "REF-016", path: "work.subject_ref"},
		{name: "artifact belongs to other thread", edit: func(in *WorkCheckpointInput) { in.ResolvedSubjects[0].Thread = "thread:axon-20260804-bcde" }, code: "REF-016", path: "work.subject_ref"},
		{name: "referenced classification unknown", edit: func(in *WorkCheckpointInput) { in.ResolvedSubjects[0].Classification = "" }, code: "POL-015", path: "classification"},
		{name: "classification downgrade", edit: func(in *WorkCheckpointInput) { in.ResolvedSubjects[0].Classification = "restricted" }, code: "POL-015", path: "classification"},
		{name: "unknown current or historical system", edit: func(in *WorkCheckpointInput) {
			in.Work.Mode = WorkModeWaiting
			in.Work.WaitingOn = []WorkCheckpointWait{{Kind: WorkWaitSystem, ID: "gone"}}
		}, code: "REF-016", path: "work.waiting_on.0.id"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			in := validWorkCheckpointInput()
			test.edit(&in)
			assertOnlyWorkViolation(t, ValidateWorkCheckpoint(in), test.code, test.path)
		})
	}

	for _, test := range []struct {
		name string
		edit func(*WorkCheckpointInput)
	}{
		{name: "thread subject", edit: func(in *WorkCheckpointInput) {
			in.Work.SubjectRef = in.Thread
			in.Refs = nil
			in.ResolvedSubjects = nil
		}},
		{name: "contract subject", edit: func(in *WorkCheckpointInput) {
			in.Work.SubjectRef = "XC-axon-country@1.2.3"
			in.Refs = []string{in.Work.SubjectRef}
			in.ResolvedSubjects = []WorkCheckpointSubject{{Ref: in.Work.SubjectRef, Thread: in.Thread, Classification: "internal"}}
		}},
		{name: "cross-space contract subject", edit: func(in *WorkCheckpointInput) {
			in.Work.SubjectRef = "partner-space:XC-axon-country@1.2.3"
			in.Refs = []string{in.Work.SubjectRef}
			in.ResolvedSubjects = []WorkCheckpointSubject{{Ref: in.Work.SubjectRef, Thread: in.Thread, Classification: "internal"}}
		}},
		{name: "stronger checkpoint classification", edit: func(in *WorkCheckpointInput) { in.Classification = "restricted" }},
		{name: "current system wait", edit: func(in *WorkCheckpointInput) {
			in.Work.Mode = WorkModeWaiting
			in.Work.WaitingOn = []WorkCheckpointWait{{Kind: WorkWaitSystem, ID: "axon"}}
		}},
		{name: "historical system wait", edit: func(in *WorkCheckpointInput) {
			in.Work.Mode = WorkModeWaiting
			in.Work.WaitingOn = []WorkCheckpointWait{{Kind: WorkWaitSystem, ID: "legacy"}}
		}},
		{name: "non-system wait is opaque", edit: func(in *WorkCheckpointInput) {
			in.Work.Mode = WorkModeWaiting
			in.Work.WaitingOn = []WorkCheckpointWait{{Kind: WorkWaitTool, ID: "missing-tool"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			in := validWorkCheckpointInput()
			test.edit(&in)
			if got := ValidateWorkCheckpoint(in); !got.Valid {
				t.Fatalf("valid reference context rejected: %+v", got.Violations)
			}
		})
	}
}

func validWorkCheckpointInput() WorkCheckpointInput {
	validUntil := "2026-08-10T10:00:00Z"
	return WorkCheckpointInput{
		InvocationPoint: V2,
		ArtifactID:      "XA-axon-20260803-abcd",
		Type:            "announcement",
		Category:        "status",
		Space:           "demo",
		Thread:          "thread:axon-20260803-abcd",
		Refs:            []string{"XW-axon-20260803-abcd"},
		Recipients:      []string{"beta"},
		Classification:  "internal",
		FirstParty:      true,
		Actor:           WorkCheckpointActor{System: "axon", Name: "codex", Session: "session-1"},
		Work: WorkCheckpoint{
			ID: "work:01K1Z6Y8Q9C7G5M3N2P4R6T8VW", SemanticSequence: 1,
			Mode: WorkModeImplementing, SubjectRef: "XW-axon-20260803-abcd",
			ReportedAt: "2026-08-03T10:00:00Z", ValidUntil: &validUntil,
		},
		ResolvedSubjects: []WorkCheckpointSubject{{
			Ref: "XW-axon-20260803-abcd", Thread: "thread:axon-20260803-abcd", Classification: "internal",
		}},
		ManifestSystems:           []string{"axon", "beta"},
		HistoricalManifestSystems: []string{"legacy"},
	}
}

func cloneWorkPrevious(previous WorkCheckpointPrevious) *WorkCheckpointPrevious {
	cloned := previous
	cloned.Recipients = append([]string(nil), previous.Recipients...)
	return &cloned
}

func assertOnlyWorkViolation(t *testing.T, result Result, code, path string) {
	t.Helper()
	if result.Valid {
		t.Fatalf("ValidateWorkCheckpoint valid, want %s at %s", code, path)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("violations = %+v, want one %s at %s", result.Violations, code, path)
	}
	got := result.Violations[0]
	if got.Code != code || got.Path != path || got.Severity != SeverityReject {
		t.Fatalf("violation = %+v, want rejecting %s at %s", got, code, path)
	}
	wantClass := ClassPolicy
	if code == "REF-016" {
		wantClass = ClassReferential
	}
	if got.Class != wantClass {
		t.Fatalf("violation class = %q, want %q", got.Class, wantClass)
	}
}

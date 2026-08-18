package spacenotify

import "testing"

func TestSortMessages_ClassDeadlineThenID(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Class: ClassPublished, Artifact: &Artifact{ID: "XW-2"}},
		{Class: ClassHumanGate, Artifact: &Artifact{ID: "XW-1"}},
		{Class: ClassDigest, Route: Route{Chat: "c1"}},
		{Class: ClassBlocking, Artifact: &Artifact{ID: "XW-0"}, Urgency: &Urgency{Deadline: "2026-08-01"}},
		{Class: ClassBlocking, Artifact: &Artifact{ID: "XW-9"}, Urgency: &Urgency{Deadline: "2026-07-01"}},
	}
	sortMessages(msgs)

	if got := msgs[0].Class; got != ClassHumanGate || msgs[0].Artifact.ID != "XW-1" {
		t.Fatalf("msgs[0] = %+v, want human-gate XW-1 first", msgs[0])
	}
	if msgs[1].Class != ClassBlocking || msgs[1].Artifact.ID != "XW-9" {
		t.Fatalf("msgs[1] = %+v, want blocking XW-9 (earlier deadline) next", msgs[1])
	}
	if msgs[2].Class != ClassBlocking || msgs[2].Artifact.ID != "XW-0" {
		t.Fatalf("msgs[2] = %+v, want blocking XW-0 (later deadline) next", msgs[2])
	}
	if msgs[3].Class != ClassPublished || msgs[3].Artifact.ID != "XW-2" {
		t.Fatalf("msgs[3] = %+v, want published XW-2 next", msgs[3])
	}
	if msgs[4].Class != ClassDigest {
		t.Fatalf("msgs[4] = %+v, want digest last", msgs[4])
	}
}

func TestSortMessages_Deterministic(t *testing.T) {
	t.Parallel()
	build := func() []Message {
		return []Message{
			{Class: ClassPublished, Artifact: &Artifact{ID: "XW-b"}},
			{Class: ClassPublished, Artifact: &Artifact{ID: "XW-a"}},
			{Class: ClassBlocking, Artifact: &Artifact{ID: "XW-c"}},
		}
	}
	a, b := build(), build()
	sortMessages(a)
	sortMessages(b)
	for i := range a {
		if a[i].Class != b[i].Class || (a[i].Artifact == nil) != (b[i].Artifact == nil) {
			t.Fatalf("non-deterministic sort at index %d: %+v vs %+v", i, a[i], b[i])
		}
		if a[i].Artifact != nil && a[i].Artifact.ID != b[i].Artifact.ID {
			t.Fatalf("non-deterministic sort at index %d: %s vs %s", i, a[i].Artifact.ID, b[i].Artifact.ID)
		}
	}
}

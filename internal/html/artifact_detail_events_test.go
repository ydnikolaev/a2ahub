package html

import "testing"

func TestAttachLinkedArtifactEvents_ResponseCreation(t *testing.T) {
	t.Parallel()
	detail := ArtifactDetail{
		ID: "XS-seomatrix-1", Space: "getvisa", Thread: "thread:one",
		Events: []ArtifactDetailEvent{{
			ULID: "02", Transition: "verify", At: "2026-08-01T19:29:00Z",
		}},
	}
	views := []ThreadView{{
		Space: "getvisa", Thread: "thread:one",
		Transcript: []ThreadTranscriptRow{{
			At: "2026-08-01T18:58:00Z",
			Event: &TranscriptEvent{
				ULID: "01", Subject: "XW-axon-1", Transition: "respond",
				ClaimedState: "responded", ResponseID: "XS-seomatrix-1",
				Actor: map[string]any{"name": "xpressmike", "system": "seomatrix"},
			},
		}},
	}}

	attachLinkedArtifactEvents(&detail, views)

	if len(detail.Events) != 2 {
		t.Fatalf("events = %d, want linked response creation plus subject history: %+v", len(detail.Events), detail.Events)
	}
	created := detail.Events[0]
	if created.ULID != "01" || created.Transition != "respond" || created.Actor != "xpressmike" || created.ActorSystem != "seomatrix" {
		t.Fatalf("linked response event projected incorrectly: %+v", created)
	}
	if detail.Events[1].ULID != "02" {
		t.Fatalf("events not sorted causally: %+v", detail.Events)
	}
}

func TestAttachLinkedArtifactEvents_DeduplicatesExistingEvent(t *testing.T) {
	t.Parallel()
	detail := ArtifactDetail{
		ID: "XS-seomatrix-1", Space: "getvisa",
		Events: []ArtifactDetailEvent{{ULID: "01", Transition: "respond", At: "2026-08-01T18:58:00Z"}},
	}
	views := []ThreadView{{Space: "getvisa", Transcript: []ThreadTranscriptRow{{
		At:    "2026-08-01T18:58:00Z",
		Event: &TranscriptEvent{ULID: "01", Transition: "respond", ResponseID: detail.ID},
	}}}}

	attachLinkedArtifactEvents(&detail, views)

	if len(detail.Events) != 1 {
		t.Fatalf("duplicate event was appended: %+v", detail.Events)
	}
}

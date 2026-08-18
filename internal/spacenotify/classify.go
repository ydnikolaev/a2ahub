package spacenotify

import "github.com/ydnikolaev/a2ahub/internal/cache"

// classify computes fa's own class — spec 03 step 3, evaluated ONCE per
// artifact, before any per-route filtering (step 4 never changes it, only
// keeps or drops the artifact for a given route). An artifact that is
// both p1 AND gate-pending gets exactly one class, deterministically:
// human-gate always wins, because a gate blocks on a human regardless of
// priority.
//
//   - human-gate: fa.Verdict.HumanGate is non-empty — internal/fold's own
//     §3.7 gate name (G1-G5), never a second gate list here.
//   - blocking: the artifact is addressed to at least one participant
//     ("inbound" — fa.Addressees is the complete addressee set
//     BuildNotifyIndex already resolved) AND is p1 or carries the
//     `blocking` flag.
//   - published: otherwise — every other qualifying artifact.
func classify(fa cache.NotifyArtifact) string {
	if fa.Verdict.HumanGate != "" {
		return ClassHumanGate
	}
	inbound := len(fa.Addressees) > 0 || fa.Broadcast
	if inbound && (fa.Priority == "p1" || fa.Blocking) {
		return ClassBlocking
	}
	return ClassPublished
}

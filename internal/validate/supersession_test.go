package validate

import "testing"

func countCode(violations []Violation, code string) int {
	n := 0
	for _, v := range violations {
		if v.Code == code {
			n++
		}
	}
	return n
}

func TestCheckSupersessionGraph_LinearChainIsClean(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-bbbb", Predecessor: "XW-axon-20260731-aaaa"},
		{Successor: "XW-axon-20260802-cccc", Predecessor: "XW-axon-20260801-bbbb"},
	}
	got := CheckSupersessionGraph(links)
	if len(got) != 0 {
		t.Fatalf("linear chain must be clean, got %+v", got)
	}
}

func TestCheckSupersessionGraph_DisjointForestIsClean(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-bbbb", Predecessor: "XW-axon-20260731-aaaa"},
		{Successor: "XW-seomatrix-20260801-dddd", Predecessor: "XW-seomatrix-20260731-cccc"},
		{Successor: "XW-seomatrix-20260802-eeee", Predecessor: "XW-seomatrix-20260801-dddd"},
	}
	got := CheckSupersessionGraph(links)
	if len(got) != 0 {
		t.Fatalf("disjoint forest must be clean, got %+v", got)
	}
}

func TestCheckSupersessionGraph_ForkRejectsAndNamesBothClaimants(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-bbbb", Predecessor: "XW-axon-20260731-aaaa"},
		{Successor: "XW-axon-20260801-zzzz", Predecessor: "XW-axon-20260731-aaaa"},
	}
	got := CheckSupersessionGraph(links)
	if countCode(got, "REF-020") != 1 {
		t.Fatalf("want exactly one REF-020 fork violation, got %+v", got)
	}
	var fork Violation
	for _, v := range got {
		if v.Code == "REF-020" {
			fork = v
		}
	}
	want := map[string]bool{
		"XW-axon-20260731-aaaa": true,
		"XW-axon-20260801-bbbb": true,
		"XW-axon-20260801-zzzz": true,
	}
	if len(fork.Subjects) != len(want) {
		t.Fatalf("fork violation must name the predecessor and both successors, got Subjects=%v", fork.Subjects)
	}
	for _, s := range fork.Subjects {
		if !want[s] {
			t.Fatalf("fork violation named unexpected subject %q, got Subjects=%v", s, fork.Subjects)
		}
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("fork violation is missing subjects %v, got Subjects=%v", want, fork.Subjects)
	}
}

// TestCheckSupersessionGraph_DuplicateLinkIsNotAFork covers the doc
// comment's own carve-out: the SAME successor linked to the SAME
// predecessor twice (a duplicate event read, or the same PR walked twice)
// is a repeated claim, not a competing one.
func TestCheckSupersessionGraph_DuplicateLinkIsNotAFork(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-bbbb", Predecessor: "XW-axon-20260731-aaaa"},
		{Successor: "XW-axon-20260801-bbbb", Predecessor: "XW-axon-20260731-aaaa"},
	}
	got := CheckSupersessionGraph(links)
	if len(got) != 0 {
		t.Fatalf("a duplicated identical link must not be reported as a fork, got %+v", got)
	}
}

func TestCheckSupersessionGraph_TwoCycleRejectsAndNamesBothMembers(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-aaaa", Predecessor: "XW-axon-20260731-bbbb"},
		{Successor: "XW-axon-20260731-bbbb", Predecessor: "XW-axon-20260801-aaaa"},
	}
	got := CheckSupersessionGraph(links)
	if countCode(got, "REF-021") != 1 {
		t.Fatalf("want exactly one REF-021 cycle violation for a 2-cycle, got %+v", got)
	}
	var cyc Violation
	for _, v := range got {
		if v.Code == "REF-021" {
			cyc = v
		}
	}
	want := map[string]bool{"XW-axon-20260801-aaaa": true, "XW-axon-20260731-bbbb": true}
	if len(cyc.Subjects) != len(want) {
		t.Fatalf("2-cycle violation must name both members, got Subjects=%v", cyc.Subjects)
	}
	for _, s := range cyc.Subjects {
		if !want[s] {
			t.Fatalf("2-cycle violation named unexpected subject %q, got Subjects=%v", s, cyc.Subjects)
		}
	}
}

func TestCheckSupersessionGraph_ThreeCycleRejectsAndNamesAllMembers(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-aaaa", Predecessor: "XW-axon-20260731-bbbb"},
		{Successor: "XW-axon-20260731-bbbb", Predecessor: "XW-axon-20260701-cccc"},
		{Successor: "XW-axon-20260701-cccc", Predecessor: "XW-axon-20260801-aaaa"},
	}
	got := CheckSupersessionGraph(links)
	if countCode(got, "REF-021") != 1 {
		t.Fatalf("want exactly one REF-021 cycle violation for a 3-cycle, got %+v", got)
	}
	var cyc Violation
	for _, v := range got {
		if v.Code == "REF-021" {
			cyc = v
		}
	}
	want := map[string]bool{
		"XW-axon-20260801-aaaa": true,
		"XW-axon-20260731-bbbb": true,
		"XW-axon-20260701-cccc": true,
	}
	if len(cyc.Subjects) != len(want) {
		t.Fatalf("3-cycle violation must name all three members, got Subjects=%v", cyc.Subjects)
	}
	for _, s := range cyc.Subjects {
		if !want[s] {
			t.Fatalf("3-cycle violation named unexpected subject %q, got Subjects=%v", s, cyc.Subjects)
		}
	}
}

func TestCheckSupersessionGraph_SelfSupersedeIsACycle(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "XW-axon-20260801-aaaa", Predecessor: "XW-axon-20260801-aaaa"},
	}
	got := CheckSupersessionGraph(links)
	if countCode(got, "REF-021") != 1 {
		t.Fatalf("self-supersede must be reported as a 1-node cycle (REF-021), got %+v", got)
	}
	if hasCode(got, "REF-020") {
		t.Fatalf("self-supersede must not also be reported as a fork, got %+v", got)
	}
	var cyc Violation
	for _, v := range got {
		if v.Code == "REF-021" {
			cyc = v
		}
	}
	if len(cyc.Subjects) != 1 || cyc.Subjects[0] != "XW-axon-20260801-aaaa" {
		t.Fatalf("self-supersede cycle must name exactly its one member, got Subjects=%v", cyc.Subjects)
	}
}

func TestCheckSupersessionGraph_IncompleteLinkIsSkipped(t *testing.T) {
	t.Parallel()
	links := []SupersedeLink{
		{Successor: "", Predecessor: "XW-axon-20260731-aaaa"},
		{Successor: "XW-axon-20260801-bbbb", Predecessor: ""},
	}
	got := CheckSupersessionGraph(links)
	if len(got) != 0 {
		t.Fatalf("a link missing either id must be skipped, got %+v", got)
	}
}

func TestCheckSupersessionGraph_EmptyIsClean(t *testing.T) {
	t.Parallel()
	if got := CheckSupersessionGraph(nil); len(got) != 0 {
		t.Fatalf("nil links must be clean, got %+v", got)
	}
}

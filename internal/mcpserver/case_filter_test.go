package mcpserver

import "testing"

func TestIntersectKnowledgeIDsUsesAllCandidatesWithoutExplicitDocIDs(t *testing.T) {
	got := intersectKnowledgeIDs(nil, []string{"case-2", "case-1", "case-2", ""})
	assertIDs(t, got, []string{"case-2", "case-1"})
}

func TestIntersectKnowledgeIDsRestrictsExplicitDocIDs(t *testing.T) {
	got := intersectKnowledgeIDs(
		[]string{"document", "case-2", "case-1", "case-2"},
		[]string{"case-1", "case-2"},
	)
	assertIDs(t, got, []string{"case-2", "case-1"})
}

func TestIntersectKnowledgeIDsReturnsEmptyForNoCandidates(t *testing.T) {
	got := intersectKnowledgeIDs([]string{"document"}, nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want non-nil empty slice", got)
	}
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeCasePayload(t *testing.T) {
	payload := &types.UpsertCasePayload{
		Subject:              " subject ",
		Summary:              " summary ",
		CaseDate:             "2026-08-05",
		Chipsets:             []string{" QCM6490 ", "", "QCM6490", "QCS8550"},
		ChangeRequestNumbers: []string{"3901518", " 3901518 "},
	}

	normalized, caseDate, chipsets, changeRequests, err := normalizeCasePayload(payload)
	if err != nil {
		t.Fatalf("normalizeCasePayload returned error: %v", err)
	}
	if normalized.Subject != "subject" || normalized.Summary != "summary" {
		t.Fatalf("payload was not trimmed: %#v", normalized)
	}
	if caseDate == nil || caseDate.Format("2006-01-02") != "2026-08-05" {
		t.Fatalf("unexpected Case date: %v", caseDate)
	}
	assertStrings(t, chipsets, []string{"QCM6490", "QCS8550"})
	assertStrings(t, changeRequests, []string{"3901518"})
}

func TestNormalizeCasePayloadRejectsInvalidDate(t *testing.T) {
	_, _, _, _, err := normalizeCasePayload(&types.UpsertCasePayload{CaseDate: "08/05/2026"})
	if err != ErrInvalidCaseDate {
		t.Fatalf("expected ErrInvalidCaseDate, got %v", err)
	}
}

func TestIsCaseKnowledgePublished(t *testing.T) {
	for _, status := range []string{
		types.ParseStatusPending,
		types.ParseStatusProcessing,
		types.ParseStatusFinalizing,
		types.ParseStatusCompleted,
	} {
		if !isCaseKnowledgePublished(status) {
			t.Fatalf("expected %q to be published", status)
		}
	}
	for _, status := range []string{
		types.ManualKnowledgeStatusDraft,
		types.ParseStatusFailed,
		types.ParseStatusCancelled,
		types.ParseStatusDeleting,
	} {
		if isCaseKnowledgePublished(status) {
			t.Fatalf("expected %q to require publication", status)
		}
	}
}

func TestParseCaseFilterDate(t *testing.T) {
	if value, err := parseCaseFilterDate(" 2026-08-05 "); err != nil || value.Format("2006-01-02") != "2026-08-05" {
		t.Fatalf("unexpected parsed filter date: %v, %v", value, err)
	}
	if _, err := parseCaseFilterDate("08/05/2026"); err == nil {
		t.Fatal("expected invalid filter date to fail")
	}
}

func TestValidCaseFieldLengths(t *testing.T) {
	payload := &types.UpsertCasePayload{Subject: "subject"}
	if !validCaseFieldLengths("8538861", payload, []string{"QCM6490"}, []string{"3901518"}) {
		t.Fatal("expected valid Case fields")
	}
	payload.Subject = strings.Repeat("x", 250)
	if !validCaseFieldLengths("8538861", payload, nil, nil) {
		t.Fatal("expected long subject to pass since title is auto-truncated")
	}
	payload.Subject = "subject"
	if validCaseFieldLengths(strings.Repeat("1", 65), payload, nil, nil) {
		t.Fatal("expected Case number over 64 characters to fail")
	}
}

func TestCaseContentHashTracksSearchableMarkdownOnly(t *testing.T) {
	payload := &types.UpsertCasePayload{
		Subject:         "Modem crash",
		Summary:         "Root cause and fix",
		Description:     "not indexed",
		ExtraAttributes: map[string]any{"owner": "A"},
	}
	markdown := buildCaseMarkdown("8538861", payload, []string{"QCM6490"}, nil)
	initialHash := hashCaseContent(markdown)

	payload.Description = "changed but still not indexed"
	payload.ExtraAttributes["owner"] = "B"
	if got := hashCaseContent(buildCaseMarkdown("8538861", payload, []string{"QCM6490"}, nil)); got != initialHash {
		t.Fatalf("non-searchable fields changed content hash")
	}

	payload.Summary = "Updated root cause and fix"
	if got := hashCaseContent(buildCaseMarkdown("8538861", payload, []string{"QCM6490"}, nil)); got == initialHash {
		t.Fatalf("searchable Summary did not change content hash")
	}
}

func TestBuildCaseMarkdownExcludesDescription(t *testing.T) {
	markdown := buildCaseMarkdown("8538861", &types.UpsertCasePayload{
		Subject:      "Subject",
		Description:  "large raw description",
		Summary:      "AI summary",
		ProblemArea1: "Modem",
	}, []string{"QCM6490"}, []string{"3901518"})

	for _, expected := range []string{"Case 8538861", "AI summary", "QCM6490", "3901518", "Problem Area 1: Modem"} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("Markdown does not contain %q: %s", expected, markdown)
		}
	}
	if strings.Contains(markdown, "large raw description") {
		t.Fatalf("Markdown unexpectedly contains Description: %s", markdown)
	}
}

func assertStrings(t *testing.T, got, want []string) {
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

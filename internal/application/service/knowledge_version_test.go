package service

import (
	"testing"
)

func TestParseDocVersion(t *testing.T) {
	tests := []struct {
		filename   string
		wantID     string
		wantRev    string
		wantRest   string
		wantHasVer bool
	}{
		{
			filename:   "KBA-260514191553_REV_1_PCIe_Endpoint_D3cold.pdf",
			wantID:     "KBA-260514191553",
			wantRev:    "1",
			wantRest:   "_PCIe_Endpoint_D3cold",
			wantHasVer: true,
		},
		{
			filename:   "KBA-260514191553_REV_2_PCIe_Endpoint_D3cold.pdf",
			wantID:     "KBA-260514191553",
			wantRev:    "2",
			wantRest:   "_PCIe_Endpoint_D3cold",
			wantHasVer: true,
		},
		{
			filename:   "80-79812-73_REV_AA_Performance_Dashboard.pdf",
			wantID:     "80-79812-73",
			wantRev:    "AA",
			wantRest:   "_Performance_Dashboard",
			wantHasVer: true,
		},
		{
			filename:   "80-79812-73_REV_AB_Performance_Dashboard.pdf",
			wantID:     "80-79812-73",
			wantRev:    "AB",
			wantRest:   "_Performance_Dashboard",
			wantHasVer: true,
		},
		{
			filename:   "80-58495-11_REV_BA_QMX1xx_Boot.pdf",
			wantID:     "80-58495-11",
			wantRev:    "BA",
			wantRest:   "_QMX1xx_Boot",
			wantHasVer: true,
		},
		{
			filename:   "normal_document_without_version.pdf",
			wantID:     "",
			wantRev:    "",
			wantRest:   "",
			wantHasVer: false,
		},
		{
			filename:   "China active Projects(2026-05-25).xlsm",
			wantID:     "",
			wantRev:    "",
			wantRest:   "",
			wantHasVer: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			gotID, gotRev, gotRest, gotHasVer := parseDocVersion(tt.filename)
			if gotID != tt.wantID || gotRev != tt.wantRev || gotRest != tt.wantRest || gotHasVer != tt.wantHasVer {
				t.Errorf("parseDocVersion(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
					tt.filename, gotID, gotRev, gotRest, gotHasVer,
					tt.wantID, tt.wantRev, tt.wantRest, tt.wantHasVer)
			}
		})
	}
}

func TestCompareRevisions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		// Numeric
		{"1", "2", -1},
		{"2", "1", 1},
		{"1", "1", 0},
		{"2", "10", -1},
		{"10", "2", 1},

		// Single letter
		{"A", "B", -1},
		{"B", "A", 1},
		{"A", "A", 0},
		{"A", "Z", -1},

		// Double letter
		{"AA", "AB", -1},
		{"AB", "AA", 1},
		{"AA", "AA", 0},
		{"AZ", "BA", -1},
		{"BA", "AZ", 1},
		{"AA", "BA", -1},
		{"CA", "BA", 1},

		// Single vs double (shorter = older)
		{"A", "AA", -1},
		{"Z", "AA", -1},
		{"AA", "A", 1},

		// Numeric vs alpha (numeric = older)
		{"1", "A", -1},
		{"99", "A", -1},
		{"A", "1", 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := compareRevisions(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareRevisions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

package service

import (
	"regexp"
	"strconv"
	"strings"
)

// docVersionRe matches document filenames with REV_ version patterns.
// Examples:
//   - KBA-260514191553_REV_1_PCIe_Endpoint_...
//   - 80-79812-73_REV_AA_Performance_Dashboard_...
//   - 80-58495-11_REV_AB_QMX1xx_Boot_...
//
// Group 1: docBaseID (everything before _REV_)
// Group 2: revision (1, 2, A, AA, AB, etc.)
// Group 3: rest of filename after revision
var docVersionRe = regexp.MustCompile(`^(.+?)_REV_([A-Za-z0-9]+)(.*)$`)

// parseDocVersion extracts the document base ID and revision from a filename.
// Returns hasVersion=false if the filename doesn't contain a _REV_ pattern.
func parseDocVersion(filename string) (docBaseID, revision, rest string, hasVersion bool) {
	// Remove extension for matching
	nameWithoutExt := removeExtension(filename)

	matches := docVersionRe.FindStringSubmatch(nameWithoutExt)
	if matches == nil {
		return "", "", "", false
	}

	return matches[1], strings.ToUpper(matches[2]), matches[3], true
}

// compareRevisions compares two revision strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
//
// Supports:
//   - Numeric: "1" < "2" < "10"
//   - Single letter: "A" < "B" < "Z"
//   - Double letter: "AA" < "AB" < "AZ" < "BA"
//   - Mixed: numeric revisions are always considered older than letter revisions
func compareRevisions(a, b string) int {
	a = strings.ToUpper(strings.TrimSpace(a))
	b = strings.ToUpper(strings.TrimSpace(b))

	if a == b {
		return 0
	}

	aNum, aIsNum := parseNumericRevision(a)
	bNum, bIsNum := parseNumericRevision(b)

	// Both numeric
	if aIsNum && bIsNum {
		if aNum < bNum {
			return -1
		}
		return 1
	}

	// Both alphabetic
	if !aIsNum && !bIsNum {
		return compareAlphaRevisions(a, b)
	}

	// Mixed: numeric < alphabetic (numeric versions are considered older)
	if aIsNum {
		return -1
	}
	return 1
}

// parseNumericRevision tries to parse a revision as a number.
func parseNumericRevision(rev string) (int, bool) {
	n, err := strconv.Atoi(rev)
	if err != nil {
		return 0, false
	}
	return n, true
}

// compareAlphaRevisions compares two alphabetic revision strings.
// "A" < "B" < "Z" < "AA" < "AB" < "AZ" < "BA" < "ZZ"
func compareAlphaRevisions(a, b string) int {
	// Shorter strings are older (A < AA)
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}

	// Same length: compare character by character
	if a < b {
		return -1
	}
	return 1
}

// removeExtension removes the file extension from a filename.
func removeExtension(filename string) string {
	dotIdx := strings.LastIndex(filename, ".")
	if dotIdx < 0 {
		return filename
	}
	return filename[:dotIdx]
}

// docBaseIDToPattern converts a docBaseID to a SQL LIKE pattern for finding
// all versions of the same document.
// Example: "KBA-260514191553" → "KBA-260514191553_REV_%"
func docBaseIDToPattern(docBaseID string) string {
	return docBaseID + "_REV_%"
}

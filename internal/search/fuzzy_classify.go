package search

import (
	"strings"
	"unicode"
)

// IsIdentifierLike determines whether a token looks like a code or system identifier:
// - contains digits (v1.5, port8080, cve2024, 127.0.0.1)
// - contains path or punctuation delimiters: _, -, ., /, \, ~ (auth_token, deploy/staging.md, cve-2024-1029)
// - is ALLCAPS with length >= 2 (RAM, CPU, TLS, DNS, FTS5, WAL, UUID)
// - is camelCase / PascalCase with mixed case letters (e.g. upsertBatchSize, getMemory)
// Identifier-like tokens are matched exactly or by prefix only, and are never typo-expanded.
func IsIdentifierLike(token string) bool {
	token = CleanQueryToken(token)
	if len(token) < 2 {
		return false
	}

	// Delimiters
	if strings.ContainsAny(token, `_-./\~`) {
		return true
	}

	hasDigit := false
	hasUpper := false
	hasLower := false
	letterCount := 0

	runes := []rune(token)
	for i, r := range runes {
		if unicode.IsDigit(r) {
			hasDigit = true
		} else if unicode.IsLetter(r) {
			letterCount++
			if unicode.IsUpper(r) {
				hasUpper = true
				// Check for camelCase (lowercase immediately preceding uppercase)
				if i > 0 && unicode.IsLower(runes[i-1]) {
					return true
				}
			} else if unicode.IsLower(r) {
				hasLower = true
			}
		}
	}

	// Digits present
	if hasDigit {
		return true
	}

	// ALLCAPS with length >= 2
	if hasUpper && !hasLower && letterCount >= 2 {
		return true
	}

	return false
}

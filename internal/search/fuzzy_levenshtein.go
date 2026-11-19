package search

import (
	"strings"
	"unicode/utf8"
)

// DamerauLevenshtein calculates the Damerau-Levenshtein distance between s1 and s2,
// which is the minimum number of insertions, deletions, substitutions, and adjacent transpositions
// required to transform s1 into s2. If the distance exceeds maxDist, it returns maxDist + 1 early.
func DamerauLevenshtein(s1, s2 string, maxDist int) int {
	if s1 == s2 {
		return 0
	}
	len1 := utf8.RuneCountInString(s1)
	len2 := utf8.RuneCountInString(s2)

	diff := len1 - len2
	if diff < 0 {
		diff = -diff
	}
	if diff > maxDist {
		return maxDist + 1
	}

	r1 := []rune(s1)
	r2 := []rune(s2)

	// Single row / small matrix DP
	// d[i, j] will be distance between r1[0..i-1] and r2[0..j-1]
	d := make([][]int, len1+1)
	for i := range d {
		d[i] = make([]int, len2+1)
		d[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		d[0][j] = j
	}

	for i := 1; i <= len1; i++ {
		minInRow := i
		for j := 1; j <= len2; j++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}

			delCost := d[i-1][j] + 1
			insCost := d[i][j-1] + 1
			subCost := d[i-1][j-1] + cost

			minVal := delCost
			if insCost < minVal {
				minVal = insCost
			}
			if subCost < minVal {
				minVal = subCost
			}

			// Transposition check
			if i > 1 && j > 1 && r1[i-1] == r2[j-2] && r1[i-2] == r2[j-1] {
				transCost := d[i-2][j-2] + cost
				if transCost < minVal {
					minVal = transCost
				}
			}

			d[i][j] = minVal
			if minVal < minInRow {
				minInRow = minVal
			}
		}
		// If the minimum value in this row already exceeds maxDist, prune early
		if minInRow > maxDist {
			return maxDist + 1
		}
	}

	if d[len1][len2] > maxDist {
		return maxDist + 1
	}
	return d[len1][len2]
}

// CleanQueryToken removes surrounding punctuation and whitespace for fuzzy/vocab lookup.
func CleanQueryToken(s string) string {
	return strings.Trim(s, `"'(),:;?[]{}!*~^#$<>+=`)
}

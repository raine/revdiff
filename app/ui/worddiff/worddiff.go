// Package worddiff provides intra-line word-diff algorithms and a shared text-range
// highlight insertion engine. It owns tokenization, edit-distance alignment, byte-offset range
// building, similarity gating, and line pairing for add/remove diff blocks.
//
// The public API is exposed as methods on the stateless Differ type, enabling consumer-side
// interface wrapping in the ui package (same pattern as style.SGR).
package worddiff

import "regexp"

// Differ provides intra-line word-diff algorithms and highlight marker insertion.
// stateless — the receiver carries no mutable state. exists to group related methods
// under a type for consumer-side interface wrapping (same pattern as style.SGR).
type Differ struct{}

// New returns a Differ. the constructor exists for consistency with the DI pattern
// (main.go calls worddiff.New() and injects into ModelConfig).
func New() *Differ { return &Differ{} }

// Range represents a byte-offset range in a text line.
// used for both intra-line word-diff highlighting and search match highlighting.
type Range struct {
	Start int // byte offset of range start
	End   int // byte offset past last byte
}

// LinePair represents a line of content with its change direction for pairing.
type LinePair struct {
	Content  string
	IsRemove bool
}

// Pair represents a matched pair of remove/add line indices for intra-line diffing.
type Pair struct {
	RemoveIdx int
	AddIdx    int
}

// maxLineLenForDiff caps intra-line diff to lines up to this many bytes. it is a cheap
// pre-filter that keeps pathological input (minified bundles, single-line JSON) out of the
// tokenizer; maxDiffCells is the actual cost guard.
const maxLineLenForDiff = 20000

// maxDiffCells caps the edit-distance table at this many cells (minus tokens * plus tokens).
// the table dominates cost at 12 bytes per cell, so the budget is ~48MB per pair. it bounds
// one pair and nothing wider: recomputeIntraRanges calls ComputeIntraRanges once per paired
// remove/add line, so a file whose diff holds many long pairs pays this for each of them.
// byte length is a poor proxy for the cost: 500 repeated letters tokenize to one token,
// 500 bytes of minified JSON to nearly 300.
const maxDiffCells = 4_000_000

// similarityThreshold is the minimum percentage of common tokens for highlighting.
// pairs with less than this percentage of common content get no intra-line overlay.
const similarityThreshold = 30

// intralineToken represents a single token from the regex tokenizer with its byte offset in the source line.
type intralineToken struct {
	text  string // token text
	start int    // byte offset in the original line
	end   int    // byte offset past the last byte
}

// tokenPattern splits a line into word tokens (letters/digits/underscore), whitespace runs, and individual punctuation characters.
var tokenPattern = regexp.MustCompile(`[\pL\pN_]+|\s+|[^\pL\pN_\s]`)

// ComputeIntraRanges computes changed byte-offset ranges for a pair of minus/plus lines.
// returns ranges for the minus line and plus line respectively.
// returns nil ranges if either line is empty, exceeds maxLineLenForDiff, would need more than
// maxDiffCells edit-distance cells, or fails the similarity gate (< 30% common non-whitespace tokens).
func (d *Differ) ComputeIntraRanges(minusLine, plusLine string) ([]Range, []Range) {
	if minusLine == "" || plusLine == "" {
		return nil, nil
	}
	if len(minusLine) > maxLineLenForDiff || len(plusLine) > maxLineLenForDiff {
		return nil, nil
	}

	minusToks := d.tokenizeLineWithOffsets(minusLine)
	plusToks := d.tokenizeLineWithOffsets(plusLine)
	if len(minusToks) == 0 || len(plusToks) == 0 {
		return nil, nil
	}
	if len(minusToks)*len(plusToks) > maxDiffCells {
		return nil, nil
	}

	keepMinus, keepPlus := d.alignedKeptTokens(minusToks, plusToks)
	minusRanges := d.buildChangedRanges(minusToks, keepMinus)
	plusRanges := d.buildChangedRanges(plusToks, keepPlus)
	if len(minusRanges) == 0 && len(plusRanges) == 0 {
		return nil, nil // identical lines after tokenization
	}

	if !d.passesSimilarityGateFromKeep(minusToks, plusToks, keepMinus) {
		return nil, nil
	}

	return minusRanges, plusRanges
}

// PairLines pairs remove and add lines within a contiguous change block.
// equal-length runs pair 1:1 in order. unequal runs use greedy best-match scoring.
// the indices in the returned Pair values are indices into the input lines slice.
func (d *Differ) PairLines(lines []LinePair) []Pair {
	var removes, adds []int
	for i, lp := range lines {
		if lp.IsRemove {
			removes = append(removes, i)
		} else {
			adds = append(adds, i)
		}
	}

	if len(removes) == 0 || len(adds) == 0 {
		return nil
	}

	// equal-length: pair 1:1 in order
	if len(removes) == len(adds) {
		pairs := make([]Pair, len(removes))
		for i := range removes {
			pairs[i] = Pair{RemoveIdx: removes[i], AddIdx: adds[i]}
		}
		return pairs
	}

	// unequal: greedy best-match
	return d.greedyPair(lines, removes, adds)
}

// tokenizeLineWithOffsets splits a line into tokens with byte offsets.
// each token is a word (letters/digits/underscore), whitespace run, or individual punctuation character.
func (d *Differ) tokenizeLineWithOffsets(line string) []intralineToken {
	locs := tokenPattern.FindAllStringIndex(line, -1)
	tokens := make([]intralineToken, len(locs))
	for i, loc := range locs {
		tokens[i] = intralineToken{text: line[loc[0]:loc[1]], start: loc[0], end: loc[1]}
	}
	return tokens
}

// alignedKeptTokens computes which tokens from minus and plus lines are kept (unchanged) via edit-distance alignment.
// returns two boolean slices parallel to the input token slices: true = kept, false = changed.
func (d *Differ) alignedKeptTokens(minusToks, plusToks []intralineToken) ([]bool, []bool) {
	m, n := len(minusToks), len(plusToks)
	if m == 0 || n == 0 {
		return make([]bool, m), make([]bool, n)
	}

	// Opening a gap costs more than extending it. This keeps an inserted
	// expression together instead of matching delimiters scattered through it.
	const gapOpen int32 = 1
	const gapExtend int32 = 1
	const substitution int32 = 4
	const (
		aligned = iota
		removed
		added
	)
	dp := make([][][3]int32, m+1)
	for i := range dp {
		dp[i] = make([][3]int32, n+1)
	}
	for i := m; i >= 0; i-- {
		for j := n; j >= 0; j-- {
			if i == m && j == n {
				continue
			}
			for state := range 3 {
				best := int32(1 << 30)
				if i < m && j < n {
					cost := substitution
					if minusToks[i].text == plusToks[j].text {
						cost = 0
					}
					best = cost + dp[i+1][j+1][aligned]
				}
				if i < m {
					cost := gapExtend
					if state != removed {
						cost += gapOpen
					}
					best = min(best, cost+dp[i+1][j][removed])
				}
				if j < n {
					cost := gapExtend
					if state != added {
						cost += gapOpen
					}
					best = min(best, cost+dp[i][j+1][added])
				}
				dp[i][j][state] = best
			}
		}
	}

	keepMinus := make([]bool, m)
	keepPlus := make([]bool, n)
	i, j, state := 0, 0, aligned
	for i < m && j < n {
		cost := substitution
		equal := minusToks[i].text == plusToks[j].text
		if equal {
			cost = 0
		}
		if dp[i][j][state] == cost+dp[i+1][j+1][aligned] {
			keepMinus[i], keepPlus[j] = equal, equal
			i++
			j++
			state = aligned
			continue
		}
		cost = gapExtend
		if state != removed {
			cost += gapOpen
		}
		if dp[i][j][state] == cost+dp[i+1][j][removed] {
			i++
			state = removed
		} else {
			j++
			state = added
		}
	}
	return keepMinus, keepPlus
}

// isWhitespaceToken returns true if the token text is all whitespace.
func (d *Differ) isWhitespaceToken(t intralineToken) bool {
	for _, b := range []byte(t.text) {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return false
		}
	}
	return true
}

// buildChangedRanges converts token keep flags into byte-offset Range values.
// adjacent changed non-whitespace tokens are merged into single ranges.
// whitespace-only tokens are excluded from ranges (not highlighted).
func (d *Differ) buildChangedRanges(tokens []intralineToken, keep []bool) []Range {
	var ranges []Range
	var cur *Range

	for i, tok := range tokens {
		if keep[i] || d.isWhitespaceToken(tok) {
			// flush any open range
			if cur != nil {
				ranges = append(ranges, *cur)
				cur = nil
			}
			continue
		}
		// changed non-whitespace token
		if cur != nil {
			cur.End = tok.end // extend
		} else {
			cur = &Range{Start: tok.start, End: tok.end}
		}
	}
	if cur != nil {
		ranges = append(ranges, *cur)
	}
	return ranges
}

// passesSimilarityGateFromKeep returns true if the pair has at least 30% common non-whitespace tokens.
// uses pre-computed tokens and keep flags from alignedKeptTokens to avoid redundant tokenization/alignment.
// whitespace tokens are excluded from the calculation to avoid inflating similarity.
func (d *Differ) passesSimilarityGateFromKeep(minusToks, plusToks []intralineToken, keepMinus []bool) bool {
	equalNonWS := 0
	for i, k := range keepMinus {
		if k && !d.isWhitespaceToken(minusToks[i]) {
			equalNonWS++
		}
	}

	minusNonWS := d.countNonWhitespace(minusToks)
	plusNonWS := d.countNonWhitespace(plusToks)
	shorter := min(minusNonWS, plusNonWS)
	if shorter == 0 {
		return false
	}

	return equalNonWS*100 >= shorter*similarityThreshold
}

// countNonWhitespace returns the number of non-whitespace tokens.
func (d *Differ) countNonWhitespace(tokens []intralineToken) int {
	n := 0
	for _, t := range tokens {
		if !d.isWhitespaceToken(t) {
			n++
		}
	}
	return n
}

// greedyPair pairs lines greedily using normalized non-whitespace token overlap.
// Equal scores favor the earliest line. Scoring is linear in candidate token count.
// iterates the shorter side and picks the best unused match from the longer side.
func (d *Differ) greedyPair(lines []LinePair, removes, adds []int) []Pair {
	shorter, longer := removes, adds
	shorterIsRemove := true
	if len(adds) < len(removes) {
		shorter, longer = adds, removes
		shorterIsRemove = false
	}

	tokens := make([][]intralineToken, len(lines))
	for i, line := range lines {
		if len(line.Content) > maxLineLenForDiff {
			continue
		}
		for _, tok := range d.tokenizeLineWithOffsets(line.Content) {
			if !d.isWhitespaceToken(tok) {
				tokens[i] = append(tokens[i], tok)
			}
		}
	}
	used := make([]bool, len(longer))
	pairs := make([]Pair, 0, len(shorter))

	for _, si := range shorter {
		bestScore := -1.0
		bestIdx := -1

		for li, li2 := range longer {
			if used[li] {
				continue
			}
			score := d.pairSimilarity(tokens[si], tokens[li2])
			if score > bestScore {
				bestScore = score
				bestIdx = li
			}
		}

		if bestIdx >= 0 {
			used[bestIdx] = true
			if shorterIsRemove {
				pairs = append(pairs, Pair{RemoveIdx: si, AddIdx: longer[bestIdx]})
			} else {
				pairs = append(pairs, Pair{RemoveIdx: longer[bestIdx], AddIdx: si})
			}
		}
	}
	return pairs
}

// pairSimilarity compares token frequencies across the whole line, excluding whitespace.
// Repeated tokens count only up to their frequency in the other line, so duplicated
// expressions do not inflate the amount of shared content.
func (d *Differ) pairSimilarity(a, b []intralineToken) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	counts := make(map[string]int, len(a))
	for _, tok := range a {
		counts[tok.text]++
	}
	common := 0
	for _, tok := range b {
		if counts[tok.text] > 0 {
			common++
			counts[tok.text]--
		}
	}
	return 2 * float64(common) / float64(len(a)+len(b))
}

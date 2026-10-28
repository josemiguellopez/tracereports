// Package similar finds, among the failures the team already classified, the ones that look like a
// new failure: TF-IDF over the terms of their signatures (the incident key: the backend call that
// caused it or the normalized error, plus the category and the rule) and cosine similarity. Pure Go,
// no model: "it looks 92 % like the incident of the 12th, classified as an exhausted auth pool".
package similar

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Doc is a classified failure.
type Doc struct {
	ID        int64    // test id
	Terms     []string // de Terms(firma)
	CreatedAt int64
}

// Match is a doc that looks like the query, with its similarity (0..1).
type Match struct {
	ID    int64
	Score float64
}

var (
	wordRe   = regexp.MustCompile(`[\pL\pN_]+`)
	numberRe = regexp.MustCompile(`^\d+$`)
	hexRe    = regexp.MustCompile(`^[0-9a-f]{8,}$`)
)

// Terms splits a signature into lowercase terms; numbers and long hex ids become "#" so two
// failures that only differ in an id or a line still match. An HTTP status stays ("http 503" is not
// "http 500").
func Terms(signature string) []string {
	var out []string
	for _, w := range wordRe.FindAllString(strings.ToLower(signature), -1) {
		status := len(out) > 0 && out[len(out)-1] == "http" && len(w) == 3
		if !status && (numberRe.MatchString(w) || hexRe.MatchString(w)) {
			w = "#"
		}
		out = append(out, w)
	}
	return out
}

// Rank returns up to k docs whose similarity with query is at least min, best first (ties: newest
// first, then by id: the order never changes for the same input).
func Rank(query []string, docs []Doc, k int, min float64) []Match {
	if len(query) == 0 || len(docs) == 0 || k <= 0 {
		return nil
	}
	// idf con suavizado: un término presente en todos los documentos pesa poco, nunca cero
	df := map[string]int{}
	for _, d := range docs {
		seen := map[string]bool{}
		for _, t := range d.Terms {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
	n := float64(len(docs))
	idf := func(t string) float64 { return math.Log((n+1)/(float64(df[t])+1)) + 1 }
	vec := func(terms []string) (map[string]float64, float64) {
		tf := map[string]float64{}
		for _, t := range terms {
			tf[t]++
		}
		norm := 0.0
		for t, c := range tf {
			tf[t] = c * idf(t)
			norm += tf[t] * tf[t]
		}
		return tf, math.Sqrt(norm)
	}
	q, qn := vec(query)
	if qn == 0 {
		return nil
	}
	type scored struct {
		Match
		at int64
	}
	var out []scored
	for _, d := range docs {
		v, vn := vec(d.Terms)
		if vn == 0 {
			continue
		}
		dot := 0.0
		for t, w := range q {
			dot += w * v[t]
		}
		if s := dot / (qn * vn); s >= min {
			out = append(out, scored{Match{d.ID, math.Min(1, s)}, d.CreatedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Score != b.Score:
			return a.Score > b.Score
		case a.at != b.at:
			return a.at > b.at
		}
		return a.ID > b.ID
	})
	if len(out) > k {
		out = out[:k]
	}
	res := make([]Match, len(out))
	for i, s := range out {
		res[i] = s.Match
	}
	return res
}

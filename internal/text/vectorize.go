// Package text turns configuration and queries into comparable term vectors.
//
// It is deliberately stateless and corpus-unaware. Corpus statistics live in the index,
// because giving this package a corpus would silently change what every other caller of
// Similarity reports.
package text

import (
	"math"
	"strings"
	"unicode"
)

// fusedWeight is what an adjacent token pair counts for.
//
// Half weight is deliberate. A fused pair is a bridge for compound names, not evidence in
// its own right: "s3 bucket" has to reach `aws_s3_bucket`, but an incidental adjacency of
// two ordinary words must not outweigh a real term match.
const fusedWeight = 0.5

// stopWords are the scaffolding of English prose and of HCL boilerplate.
//
// The corpus-wide frequency cut in the index removes most Terraform noise on its own —
// "description" and "type" appear in every variable block and are dropped statistically.
// This list handles the words a *question* is made of, which frequency actively misjudges:
// configuration almost never writes "why" or "which", so rarity would score them as highly
// discriminating and then demand a block contain them.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true, "that": true,
	"from": true, "are": true, "was": true, "were": true, "has": true, "have": true,
	"had": true, "not": true, "but": true, "you": true, "your": true, "its": true,
	"all": true, "can": true, "will": true, "into": true, "when": true, "how": true,
	"why": true, "who": true, "where": true, "which": true, "what": true, "does": true,
	"did": true, "get": true, "getting": true, "got": true, "make": true, "made": true,
	"need": true, "want": true, "show": true, "tell": true, "give": true, "let": true,
	"use": true, "using": true, "used": true, "any": true, "some": true, "there": true,
	"here": true, "then": true, "than": true, "them": true, "they": true, "our": true,
	"set": true, "sets": true, "add": true, "adds": true,
}

// Tokenize splits text into lowercase terms.
//
// Identifiers are split on their separators *and* the parts kept individually, so
// `aws_s3_bucket` yields aws, s3 and bucket. That is what lets "s3 bucket" find a resource
// nobody would ever type in full, and it is why a plain whitespace tokenizer is not enough
// for HCL — almost every meaningful name in Terraform is a compound.
func Tokenize(s string) []string {
	var out []string
	var b strings.Builder

	flush := func() {
		if b.Len() == 0 {
			return
		}
		t := b.String()
		b.Reset()
		if len(t) < 2 || stopWords[t] {
			return
		}
		out = append(out, stem(t))
	}

	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// stem folds the plural of a term onto its singular.
//
// This is not linguistics, it is the minimum needed to stop a question failing on grammar.
// A person asks "where does the audit log bucket live" about a resource named `logs`, and
// without folding those together the query matches on "bucket" alone — which every module
// call in the repository also matches, so the actual answer loses to three of them.
//
// The length floors matter more than they look: `aws` and `dns` end in s and must never
// become `aw` and `dn`, and Terraform is full of such names.
func stem(t string) string {
	switch {
	case len(t) > 4 && strings.HasSuffix(t, "ies"):
		return t[:len(t)-3] + "y" // policies → policy
	case len(t) > 4 && strings.HasSuffix(t, "ses"),
		len(t) > 4 && strings.HasSuffix(t, "xes"),
		len(t) > 4 && strings.HasSuffix(t, "zes"),
		len(t) > 4 && strings.HasSuffix(t, "hes"):
		return t[:len(t)-2] // boxes → box, statuses → status
	case len(t) > 3 && strings.HasSuffix(t, "s") && !strings.HasSuffix(t, "ss"):
		return t[:len(t)-1] // logs → log, tags → tag
	}
	return t
}

// Vectorize counts plain terms. This is the set a block could reasonably be expected to
// contain, and it is what coverage is measured over.
func Vectorize(s string) map[string]float64 {
	v := make(map[string]float64)
	for _, t := range Tokenize(s) {
		v[t]++
	}
	return v
}

// VectorizeFused counts plain terms plus adjacent pairs at half weight.
//
// Fusion gives a query a weak phrase signal as well as compound-name reach: "logs bucket"
// yields the term "logsbucket", which a block naming both together has and one that merely
// mentions logs elsewhere does not.
func VectorizeFused(s string) map[string]float64 {
	tokens := Tokenize(s)
	v := make(map[string]float64, len(tokens)*2)
	for i, t := range tokens {
		v[t]++
		if i+1 < len(tokens) {
			v[t+tokens[i+1]] += fusedWeight
		}
	}
	return v
}

// CosineSimilarity compares two vectors, 0..1.
func CosineSimilarity(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	// Iterate the smaller vector; the dot product is symmetric and most vectors here are
	// a query against a whole block body.
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}

	var dot float64
	for term, weight := range small {
		if w, ok := large[term]; ok {
			dot += weight * w
		}
	}
	if dot == 0 {
		return 0
	}

	return dot / (norm(a) * norm(b))
}

func norm(v map[string]float64) float64 {
	var sum float64
	for _, w := range v {
		sum += w * w
	}
	if sum == 0 {
		return 1
	}
	return math.Sqrt(sum)
}

// Similarity is cosine similarity between two raw strings, fused on both sides. Fusing
// only one side throws the compound-name signal away.
func Similarity(a, b string) float64 {
	return CosineSimilarity(VectorizeFused(a), VectorizeFused(b))
}

// Sum totals a vector's weights — a vector's "length" for BM25 normalisation.
func Sum(v map[string]float64) float64 {
	var total float64
	for _, w := range v {
		total += w
	}
	return total
}

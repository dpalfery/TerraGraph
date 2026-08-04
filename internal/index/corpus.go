// Package index turns a parsed graph into something an agent can query: ranked retrieval
// with a character budget, plus the relationship queries the graph exists to answer.
package index

import (
	"math"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/dpalfery/terragraph/internal/text"
)

const (
	// k1 and b are the standard Okapi BM25 values.
	k1 = 1.2
	b  = 0.75

	// saturation is the BM25 score at which body relevance counts as half convincing.
	// BM25 is unbounded and every other scoring term here is a fixed weight, so it is
	// squashed into 0..1 to keep the total interpretable — and to let one relevance floor
	// mean the same thing for every query.
	saturation = 6.0

	// uninformativeShare is the fraction of the corpus a term may appear in before it is
	// treated as carrying no information at all.
	//
	// Terraform corpora need this more than prose does. "tags", "name", "type" and
	// "description" appear in a majority of blocks in almost any real repository, and
	// under plain term frequency they contribute as much evidence as the one rare term
	// that actually identifies what was asked for. Down-weighting by rarity is not enough:
	// a question made entirely of such words still accumulates a middling score across
	// hundreds of blocks, which is how a retrieval tool returns three confident results
	// about nothing.
	uninformativeShare = 0.5

	// minCorpusForCuts is the size below which frequency statistics are meaningless. In a
	// five-block module every term is in "most" of the corpus.
	minCorpusForCuts = 8
)

// Corpus holds the term statistics for one graph snapshot. It is built once per snapshot
// and never mutated.
type Corpus struct {
	graph *graph.Graph

	documentFrequency map[string]float64
	bodyVectors       map[string]map[string]float64
	bodyLengths       map[string]float64

	averageLength float64
	nodeCount     int
}

// BuildCorpus vectorises every node body once.
func BuildCorpus(g *graph.Graph) *Corpus {
	nodes := g.Nodes()
	c := &Corpus{
		graph:             g,
		documentFrequency: make(map[string]float64),
		bodyVectors:       make(map[string]map[string]float64, len(nodes)),
		bodyLengths:       make(map[string]float64, len(nodes)),
		nodeCount:         len(nodes),
	}

	var total float64
	for _, n := range nodes {
		v := text.VectorizeFused(NodeText(n))
		key := n.Key()
		c.bodyVectors[key] = v

		length := text.Sum(v)
		c.bodyLengths[key] = length
		total += length

		for term := range v {
			c.documentFrequency[term]++
		}
	}

	if len(nodes) > 0 {
		c.averageLength = math.Max(1, total/float64(len(nodes)))
	} else {
		c.averageLength = 1
	}
	return c
}

// NodeText is everything about a node that counts as prose: its leading comments and its
// own source.
//
// Comments are included because Terraform has no docstring convention, so a comment is
// routinely the only place a human word like "cloudtrail" or "contractual" appears near
// the resource it describes. Excluding them would make the body vector a vocabulary of
// provider schema keys and nothing else.
func NodeText(n *graph.Node) string {
	if n.Doc == "" {
		return n.Body
	}
	return n.Doc + "\n" + n.Body
}

// IsInformative reports whether a term says anything about which node is relevant.
func (c *Corpus) IsInformative(term string) bool {
	if c.nodeCount < minCorpusForCuts {
		return true
	}
	return c.documentFrequency[term] <= float64(c.nodeCount)*uninformativeShare
}

// InverseDocumentFrequency is the Robertson–Sparck Jones form: a term in every node lands
// near zero, a term in one or two dominates.
func (c *Corpus) InverseDocumentFrequency(term string) float64 {
	n := c.documentFrequency[term]
	return math.Log(1 + ((float64(c.nodeCount) - n + 0.5) / (n + 0.5)))
}

// WeightByRarity scales a query vector by term rarity, dropping the uninformative. Used
// where cosine similarity is still the right comparison and BM25's length normalisation
// does not apply.
func (c *Corpus) WeightByRarity(query map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(query))
	for term, count := range query {
		if !c.IsInformative(term) {
			continue
		}
		out[term] = count * c.InverseDocumentFrequency(term)
	}
	return out
}

// ScoreBody is how well a node's own text answers the query, on 0..1.
//
// queryTerms carries the fused pairs, which earn credit when a node has them. coverage is
// the plain words only — the terms a block could reasonably contain. Measuring coverage
// over the fused set instead makes every ordinary question unanswerable, because a
// multi-word query generates synthetic pairs that appear in no configuration ever written.
func (c *Corpus) ScoreBody(n *graph.Node, queryTerms map[string]float64, coverage []string) float64 {
	body, ok := c.bodyVectors[n.Key()]
	if !ok {
		return 0
	}
	length := c.bodyLengths[n.Key()]

	var score float64
	for term := range queryTerms {
		if !c.IsInformative(term) {
			continue
		}
		freq, present := body[term]
		if !present {
			continue
		}
		denominator := freq + (k1 * (1 - b + (b * length / c.averageLength)))
		score += c.InverseDocumentFrequency(term) * freq * (k1 + 1) / denominator
	}

	var askedFor, answered float64
	for _, term := range coverage {
		if !c.IsInformative(term) {
			continue
		}
		idf := c.InverseDocumentFrequency(term)
		askedFor += idf
		if _, present := body[term]; present {
			answered += idf
		}
	}

	if askedFor <= 0 || answered <= 0 {
		return 0
	}

	// Scale by how much of the question's *information* the node answers, not how many of
	// its words. Weighting by rarity punishes the right thing: a question about a subject
	// this repository has never heard of matches a stop-ish word or two, answers almost
	// none of what was asked, and collapses to a miss instead of a confident wrong result.
	return answered / askedFor * score / (score + saturation)
}

// NodeCount is how many nodes the statistics cover.
func (c *Corpus) NodeCount() int { return c.nodeCount }

// Graph returns the snapshot these statistics describe.
func (c *Corpus) Graph() *graph.Graph { return c.graph }

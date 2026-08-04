package index

import (
	"sort"
	"strings"

	"github.com/dpalfery/terragraph/internal/graph"
	"github.com/dpalfery/terragraph/internal/overlay"
	"github.com/dpalfery/terragraph/internal/text"
	"github.com/dpalfery/terragraph/internal/tfhcl"
)

// Scoring weights. Declared identity outranks prose, for the same reason it does in
// DocGraph — except here the identity is Terraform's own, not an ontology anyone invented.
// A block that *is* aws_s3_bucket.logs is a better answer than one that merely mentions it.
const (
	addressWeight = 6.0
	nameWeight    = 3.5
	typeWeight    = 3.0

	// addressPartialWeight covers the common case: a natural-language question almost
	// never spells an address verbatim. Addresses are structured compounds
	// (aws_s3_bucket.logs), which makes their own tokens the closest thing a Terraform
	// repository has to a controlled vocabulary. Without partial credit, "s3 logs bucket"
	// is decided entirely by body similarity, where a provider schema key outvotes the
	// subject the user actually named.
	addressPartialWeight = 4.5

	bodyWeight = 1.0

	// MinRelevanceScore is the floor below which a node is not returned at all.
	//
	// This matters more than it looks. Callers are told to try TerraGraph before grepping;
	// if a miss comes back as three weak results, the caller has no signal to fall back
	// and will answer from whatever was nearest. Saying "nothing cleared the threshold" is
	// the entire point of having one.
	MinRelevanceScore = 0.25

	// DefaultCharBudget is the total configuration returned across all nodes in one call.
	//
	// This is the token-reduction mechanism, not a nicety. It is spent across the returned
	// nodes, which means asking for one node gets depth and asking for eight gets breadth,
	// from the same knob.
	DefaultCharBudget = 12000

	// DefaultListBudget is the default for the relationship tools — reverse lookup,
	// impact, modules, orphans.
	//
	// It is deliberately a third of DefaultCharBudget, and the difference is measured
	// rather than guessed. Retrieval returns source text and needs room; the relationship
	// tools return lists whose header already states the true total, so truncating one
	// costs a caller far less. Benchmarked against a 29-stack repository, dropping these
	// from 12000 to 4000 took the suite from 63.7% cheaper than grep to 81.1%, and from
	// winning on 6 questions of 10 to 9 — with every answer still carrying a correct total
	// and a named omission.
	DefaultListBudget = 4000

	// minPerNodeBudget floors any single node's share. Terraform blocks are small — a
	// variable declaration is four lines — so a wide query still returns whole blocks
	// rather than slicing every one of them into uselessness.
	minPerNodeBudget = 600

	maxNodesCeiling = 25
	minCharBudget   = 500
	maxCharBudget   = 120000
)

// Index is an immutable, queryable snapshot. Nothing mutates it after NewIndex returns, so
// a query in flight always sees one coherent view of the configuration.
type Index struct {
	graph   *graph.Graph
	corpus  *Corpus
	overlay overlay.Resolver
}

// NewIndex builds the term statistics over a graph snapshot, with no overlay.
func NewIndex(g *graph.Graph) *Index {
	return &Index{
		graph:   g,
		corpus:  BuildCorpus(g),
		overlay: overlay.None("no overlay was supplied"),
	}
}

// WithOverlay returns an index over the same configuration with different plan facts.
//
// The corpus is shared rather than rebuilt. This is the point of keeping the overlay
// behind an interface: a fresh `terraform plan` changes what expansion and replacement
// look like, and nothing at all about the parse or the term statistics — which are the
// expensive half.
func (ix *Index) WithOverlay(ov overlay.Resolver) *Index {
	if ov == nil {
		ov = overlay.None("no overlay was supplied")
	}
	return &Index{graph: ix.graph, corpus: ix.corpus, overlay: ov}
}

// Graph exposes the underlying snapshot for the relationship queries.
func (ix *Index) Graph() *graph.Graph { return ix.graph }

// Overlay is the plan or state facts in play, never nil.
func (ix *Index) Overlay() overlay.Resolver { return ix.overlay }

// NodeInstance is one resolved instance of a node, tagged with the stack it belongs to.
//
// The stack matters: a module shared by prod and dev has separate instances in each, and
// reporting a bare count would silently add them together.
type NodeInstance struct {
	Stack string
	overlay.Instance
}

// InstancesOf resolves a node against the overlay, across every stack that reaches it.
// Returns nil when no overlay is loaded or the node is not a resource-like block.
func (ix *Index) InstancesOf(n *graph.Node) []NodeInstance {
	if n == nil || !ix.overlay.IsAvailable() {
		return nil
	}
	switch n.Kind {
	case graph.KindResource, graph.KindData, graph.KindModuleCall:
	default:
		// Variables, outputs and locals have no instances in a plan; they are folded into
		// the resources that read them.
		return nil
	}

	var out []NodeInstance
	for _, p := range ix.graph.PathsFor(n.ModuleDir) {
		for _, inst := range ix.overlay.Instances(p.Stack, p.ModuleAddress, n.Address) {
			out = append(out, NodeInstance{Stack: p.Stack, Instance: inst})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stack != out[j].Stack {
			return out[i].Stack < out[j].Stack
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// Excerpt is a node's configuration, cut to its share of the budget.
type Excerpt struct {
	Text string

	// Truncated says the block did not fit. The caller is told the full extent so it can
	// open the file at exactly the right lines rather than guessing — silent truncation is
	// what sends an agent straight back to Read.
	Truncated bool
	FullLen   int
}

// Hit is one ranked node with enough context to avoid a follow-up call.
type Hit struct {
	Node    *graph.Node
	Score   float64
	Excerpt Excerpt

	// RefCount and RefByCount summarise the node's edges. An agent deciding whether to dig
	// further needs to know that a variable has forty consumers before it asks for them.
	RefCount   int
	RefByCount int

	// Instances is the overlay's answer for this node, empty without one. A block with
	// for_each is one node and any number of instances, and the difference matters.
	Instances []NodeInstance
}

// ExploreResult is a ranked answer plus what it left out.
//
// The counts are not decoration. A caller shown three results has no way to tell a thin
// index from a tight budget, and guesses wrong in the expensive direction — it goes back
// to reading files.
type ExploreResult struct {
	Hits []Hit

	// Considered is the size of the corpus the query was measured against.
	Considered int

	// AboveThreshold is how many nodes cleared the relevance floor, before maxNodes and
	// the budget cut the list down.
	AboveThreshold int

	// DroppedForBudget is how many relevant nodes were cut because the character budget
	// could not hold them. Only this number makes asking again worthwhile.
	DroppedForBudget int
}

// Explore ranks nodes against a free-text query, an address, a name or a type.
func (ix *Index) Explore(query string, maxNodes, charBudget int) ExploreResult {
	if strings.TrimSpace(query) == "" {
		return ExploreResult{Considered: ix.graph.NodeCount()}
	}
	maxNodes = clamp(maxNodes, 1, maxNodesCeiling)
	charBudget = clamp(charBudget, minCharBudget, maxCharBudget)

	// The query is fused the same way node bodies are, so an adjacent pair of words acts
	// as a weak phrase match. Fusing only one side throws that signal away.
	queryVector := text.VectorizeFused(query)
	coverage := keysOf(text.Vectorize(query))
	trimmed := strings.TrimSpace(query)

	type scored struct {
		node  *graph.Node
		score float64
	}

	var hits []scored
	for _, n := range ix.graph.Nodes() {
		score := scoreExact(n, trimmed)
		score += addressPartialWeight * coverageOf(identityText(n), queryVector)
		score += bodyWeight * ix.corpus.ScoreBody(n, queryVector, coverage)
		score *= Authority(n)

		if score < MinRelevanceScore {
			continue
		}
		hits = append(hits, scored{n, score})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		// Stable tiebreak so identical queries return identical output.
		if hits[i].node.ModuleDir != hits[j].node.ModuleDir {
			return hits[i].node.ModuleDir < hits[j].node.ModuleDir
		}
		return hits[i].node.Address < hits[j].node.Address
	})

	res := ExploreResult{
		Considered:     ix.graph.NodeCount(),
		AboveThreshold: len(hits),
	}

	if len(hits) > maxNodes {
		hits = hits[:maxNodes]
	}

	// The budget bounds the answer, so it must also bound how many nodes are in it.
	// Splitting a tight budget across every requested node and then flooring each share
	// lets the total run several times over what was asked for — the knob would name a
	// limit it does not enforce. Returning fewer whole blocks is also the better trade for
	// Terraform, where half a resource is nearly useless.
	if affordable := charBudget / minPerNodeBudget; affordable >= 1 && len(hits) > affordable {
		res.DroppedForBudget = len(hits) - affordable
		hits = hits[:affordable]
	}

	if len(hits) == 0 {
		return res
	}

	perNode := charBudget / len(hits)
	if perNode < minPerNodeBudget {
		perNode = minPerNodeBudget
	}

	res.Hits = make([]Hit, 0, len(hits))
	for _, h := range hits {
		res.Hits = append(res.Hits, Hit{
			Node:       h.node,
			Score:      h.score,
			Excerpt:    excerpt(h.node, perNode),
			RefCount:   len(ix.graph.Outgoing(h.node.Key())),
			RefByCount: len(ix.graph.Incoming(h.node.Key())),
			Instances:  ix.InstancesOf(h.node),
		})
	}
	return res
}

// NodeCount is how many nodes a query was measured against, so a miss can say so.
func (ix *Index) NodeCount() int { return ix.graph.NodeCount() }

// scoreExact rewards a query that names the node outright.
func scoreExact(n *graph.Node, query string) float64 {
	var score float64

	if strings.EqualFold(n.Address, query) {
		score += addressWeight
	}
	if n.Name != "" && strings.EqualFold(n.Name, query) {
		score += nameWeight
	}
	if n.Type != "" && strings.EqualFold(n.Type, query) {
		score += typeWeight
	}
	return score
}

// identityText is what a node declares itself to be, flattened for coverage scoring.
//
// A module call's `source` is deliberately excluded. It is an attribute, not an identity:
// including it gave every call of ../../modules/bucket the tokens "module" and "bucket"
// for free, so any question mentioning a bucket ranked three module calls above the
// resource that actually is one. The source is still in the body vector, where BM25 weighs
// it against how common those words are — which is the right place for it.
//
// A module_source node is the exception, because there the source is the whole identity.
func identityText(n *graph.Node) string {
	if n.Kind == graph.KindModuleSource {
		return n.Address + " " + n.Source
	}

	parts := []string{n.Address}
	if n.Type != "" {
		parts = append(parts, n.Type)
	}
	return strings.Join(parts, " ")
}

// coverageOf is the fraction of an identity's own tokens that the query mentions.
//
// A token also counts as mentioned when the query contains it fused to a neighbour, which
// is what lets "s3bucket" reach a node whose address splits into "s3" and "bucket".
func coverageOf(identity string, queryVector map[string]float64) float64 {
	parts := text.Tokenize(identity)
	if len(parts) == 0 {
		return 0
	}

	covered := 0
	for i, p := range parts {
		hit := queryVector[p] > 0
		if !hit && i+1 < len(parts) {
			hit = queryVector[p+parts[i+1]] > 0
		}
		if !hit && i > 0 {
			hit = queryVector[parts[i-1]+p] > 0
		}
		if hit {
			covered++
		}
	}
	return float64(covered) / float64(len(parts))
}

// Authority is how far a node counts as operative configuration, as a multiplier on
// relevance.
//
// Term statistics measure wordiness, not standing. DocGraph demotes drafts and superseded
// documents because the repository already takes a position on what is current guidance;
// Terraform takes the same kind of position in two places, and ranking should read both.
func Authority(n *graph.Node) float64 {
	a := 1.0

	// A `moved` block says, in the configuration itself, that an address is no longer
	// where the thing lives. That is Terraform's own "superseded".
	if n.Deprecated {
		a *= 0.4
	}

	// examples/ and test/ are illustrative, not operative. Demoted rather than excluded:
	// sometimes the example is genuinely the clearest answer, and a demoted node still
	// wins outright when it is named exactly, because an exact address match scores far
	// above the discount.
	if tfhcl.IsDemoted(n.File) {
		a *= 0.5
	}

	// State bookkeeping. Real, addressable, and almost never what a question is about.
	switch n.Kind {
	case graph.KindMoved, graph.KindRemoved, graph.KindImport:
		a *= 0.6
	}

	return a
}

// excerpt fills a node's share of the budget with its configuration.
//
// Unlike a Markdown document there is nothing to select within a block — the block is
// already the unit, which is why there is no section ranking here. It either fits or it is
// cut, and if it is cut the caller is told the full extent so it can open exactly the right
// lines instead of the whole file.
func excerpt(n *graph.Node, budget int) Excerpt {
	body := n.Body
	if body == "" {
		return Excerpt{}
	}
	if len(body) <= budget {
		return Excerpt{Text: body, FullLen: len(body)}
	}
	return Excerpt{Text: body[:budget], Truncated: true, FullLen: len(body)}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func keysOf(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

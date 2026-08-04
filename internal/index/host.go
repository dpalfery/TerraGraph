package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dpalfery/terragraph/internal/overlay"
	"github.com/dpalfery/terragraph/internal/tfhcl"
)

// Host holds the current Index for a long-lived process and rebuilds it when its inputs
// change underneath it.
//
// There is no database and no incremental path. The snapshot is discarded whole and built
// again, which is the same trade DocGraph makes and for the same reason: parsing HCL and
// vectorising it costs milliseconds, so a persistence tier would buy latency at the price
// of a cache-invalidation problem. The consequence worth knowing is that editing one .tf
// file rebuilds every node. That is comfortable into the low thousands and is the first
// thing to revisit above that.
//
// The two inputs are tracked on separate clocks, because they change at very different
// rates and cost very different amounts to rebuild. Configuration is edited by hand;
// overlay files are rewritten by every `terraform plan`, which in an active session is
// constant. Folding both into one fingerprint would make a routine re-plan force a full
// re-parse and re-vectorisation of the whole repository — the expensive half — to refresh
// instance counts, which are the cheap half.
type Host struct {
	repoRoot string

	// explicitPlan is the --plan flag, honoured ahead of discovery.
	explicitPlan string

	mu           sync.Mutex
	index        *Index
	configStamp  fingerprint
	overlayStamp fingerprint

	configBuilds  int
	overlayBuilds int
}

type fingerprint struct {
	newest time.Time
	count  int
	size   int64
}

// NewHost creates a host over a repository root. Nothing is parsed until Current is called.
func NewHost(repoRoot string) *Host {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	return &Host{repoRoot: abs}
}

// WithPlan sets an explicit overlay specification: `stack=path` pairs, comma separated, or
// a bare path when the repository has one root. An empty value restores discovery.
func (h *Host) WithPlan(spec string) *Host {
	h.explicitPlan = spec
	return h
}

// RepoRoot is the absolute path being indexed.
func (h *Host) RepoRoot() string { return h.repoRoot }

// Builds is how many times the configuration has been parsed.
func (h *Host) Builds() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.configBuilds
}

// OverlayBuilds is how many times the overlay has been reloaded without a re-parse. The
// gap between this and Builds is what the two-clock split buys.
func (h *Host) OverlayBuilds() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.overlayBuilds
}

// Current returns the index, rebuilding whichever half has gone stale.
func (h *Host) Current() (*Index, error) {
	configStamp := h.computeConfigStamp()

	h.mu.Lock()
	defer h.mu.Unlock()

	configStale := h.index == nil || configStamp != h.configStamp

	if configStale {
		g, err := tfhcl.Load(h.repoRoot)
		if err != nil {
			// A failed rebuild must not destroy a working snapshot. An agent mid-question
			// is better served by a slightly stale answer than by an error, and the
			// staleness is self-correcting on the next call.
			if h.index != nil {
				return h.index, nil
			}
			return nil, err
		}
		h.index = NewIndex(g)
		h.configStamp = configStamp
		h.configBuilds++
	}

	// Overlay discovery depends on the root list, so it can only be fingerprinted once the
	// configuration has been read at least once.
	found := h.locate()
	overlayStamp := stampFiles(found)

	if configStale || overlayStamp != h.overlayStamp {
		h.index = h.index.WithOverlay(overlay.Build(found))
		h.overlayStamp = overlayStamp
		h.overlayBuilds++
	}

	return h.index, nil
}

// locate resolves which overlay files apply, preferring an explicit --plan over discovery.
func (h *Host) locate() []overlay.Found {
	roots := h.index.Graph().Roots

	if h.explicitPlan != "" {
		found, err := overlay.ParseExplicit(h.explicitPlan, roots)
		if err != nil {
			// The error is surfaced through the resolver's UnavailableReason rather than
			// failing the whole index: a bad --plan should not make retrieval stop working.
			return nil
		}
		for i := range found {
			if !filepath.IsAbs(found[i].Path) {
				found[i].Path = filepath.Join(h.repoRoot, found[i].Path)
			}
		}
		return found
	}

	return overlay.Discover(h.repoRoot, roots)
}

// ExplicitPlanError reports a malformed --plan specification, for the CLI to show at
// startup rather than silently degrading to no overlay.
func (h *Host) ExplicitPlanError() error {
	if h.explicitPlan == "" || h.index == nil {
		return nil
	}
	_, err := overlay.ParseExplicit(h.explicitPlan, h.index.Graph().Roots)
	return err
}

// computeConfigStamp fingerprints the configuration: newest modification time and file
// count, so an edit and a deletion are both noticed. Counting as well as timing matters —
// deleting a file leaves every remaining mtime untouched.
func (h *Host) computeConfigStamp() fingerprint {
	var fp fingerprint

	_ = filepath.WalkDir(h.repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != h.repoRoot && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		fp.count++
		if info.ModTime().After(fp.newest) {
			fp.newest = info.ModTime()
		}
		return nil
	})

	return fp
}

// stampFiles fingerprints the overlay inputs. Size is included as well as mtime because a
// re-plan can complete inside one filesystem timestamp tick and produce a different result.
func stampFiles(found []overlay.Found) fingerprint {
	var fp fingerprint
	for _, f := range found {
		info, err := os.Stat(f.Path)
		if err != nil {
			continue
		}
		fp.count++
		fp.size += info.Size()
		if info.ModTime().After(fp.newest) {
			fp.newest = info.ModTime()
		}
	}
	return fp
}

func skipDir(name string) bool {
	switch name {
	case ".git", ".terraform", "node_modules", ".idea", ".vscode":
		return true
	}
	return false
}

// StatFS is here so a caller can confirm the root exists before building a host over it,
// which produces a better error than an empty graph.
func StatFS(repoRoot string) error {
	_, err := os.Stat(repoRoot)
	return err
}

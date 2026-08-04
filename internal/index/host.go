package index

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dpalfery/terragraph/internal/tfhcl"
)

// Host holds the current Index for a long-lived process and rebuilds it when the
// configuration changes underneath it.
//
// There is no database and no incremental path. The snapshot is discarded whole and built
// again, which is the same trade DocGraph makes and for the same reason: parsing HCL and
// vectorising it costs milliseconds, so a persistence tier would buy latency at the price
// of a cache-invalidation problem. The consequence worth knowing is that editing one .tf
// file rebuilds every node. That is comfortable into the low thousands and is the first
// thing to revisit above that.
//
// The fingerprint is single-clock for now. DocGraph splits its two inputs because a
// CodeGraph daemon rewrites its database constantly while documentation is edited by hand;
// TerraGraph has only one input until the plan overlay lands, and inventing a second clock
// before there is a second thing to time would be structure without a reason.
type Host struct {
	repoRoot string

	mu    sync.Mutex
	index *Index
	stamp fingerprint

	// Builds counts snapshots taken, for status reporting and tests.
	builds int
}

type fingerprint struct {
	newest time.Time
	count  int
}

// NewHost creates a host over a repository root. Nothing is parsed until Current is called.
func NewHost(repoRoot string) *Host {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		abs = repoRoot
	}
	return &Host{repoRoot: abs}
}

// RepoRoot is the absolute path being indexed.
func (h *Host) RepoRoot() string { return h.repoRoot }

// Builds is how many snapshots have been taken.
func (h *Host) Builds() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.builds
}

// Current returns the index, rebuilding it first if the tree has changed.
func (h *Host) Current() (*Index, error) {
	stamp := h.computeStamp()

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.index != nil && stamp == h.stamp {
		return h.index, nil
	}

	g, err := tfhcl.Load(h.repoRoot)
	if err != nil {
		// A failed rebuild must not destroy a working snapshot. An agent mid-question is
		// better served by a slightly stale answer than by an error, and the staleness is
		// self-correcting on the next call.
		if h.index != nil {
			return h.index, nil
		}
		return nil, err
	}

	h.index = NewIndex(g)
	h.stamp = stamp
	h.builds++
	return h.index, nil
}

// computeStamp fingerprints the configuration: newest modification time and file count, so
// an edit and a deletion are both noticed. Counting as well as timing matters — deleting a
// file leaves every remaining mtime untouched.
func (h *Host) computeStamp() fingerprint {
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

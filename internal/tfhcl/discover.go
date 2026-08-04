package tfhcl

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs are never walked.
//
// `.terraform` is excluded rather than demoted. It holds verbatim copies of every module
// a root has initialised, so walking it would index a local module twice — once at its
// real path and once as a copy — and double-count exactly the call sites that
// "which stacks call this module" is meant to total. Remote module internals are worth
// having, but they belong to the plan overlay, which knows from modules.json which copies
// are duplicates and which are genuinely new.
var skipDirs = map[string]bool{
	".git":         true,
	".terraform":   true,
	"node_modules": true,
	".idea":        true,
	".vscode":      true,
}

// demotedSegments mark a directory whose configuration is illustrative rather than
// operative. They are indexed and ranked, only lower — sometimes the example is the
// answer, and excluding it would make the tool lie about what the repository contains.
var demotedSegments = map[string]bool{
	"examples":  true,
	"example":   true,
	"test":      true,
	"tests":     true,
	"fixtures":  true,
	"testdata":  true,
	"templates": true,
}

// Layout is the result of walking a repository: which directories hold configuration, and
// which of them Terraform would treat as a root.
type Layout struct {
	RepoRoot string

	// Dirs is every repo-relative directory containing at least one .tf file, sorted.
	Dirs []string

	// Roots is the subset of Dirs that are root modules.
	Roots []string

	// FilesByDir maps a directory to its .tf files, repo-relative and sorted.
	FilesByDir map[string][]string

	// CalledDirs is every directory reached by a local `module` source, so the caller can
	// tell a shared child module from an uncalled one.
	CalledDirs map[string]bool
}

// IsDemoted reports whether a repo-relative path sits under a directory whose contents are
// illustrative rather than operative.
func IsDemoted(relPath string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(relPath), "/") {
		if demotedSegments[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

// Discover walks repoRoot and classifies its Terraform directories.
//
// Root detection does not rely on a backend or provider block alone. Plenty of real roots
// declare neither — they inherit a backend from CI, or get their provider from a shared
// file one directory up. The reliable signal is negative: a directory holding .tf files
// that no local module call points at is something a person runs `terraform apply` in.
// Positive signals are still consulted, because a shared module directory that also
// happens to be applied directly does exist and would otherwise be missed.
func Discover(repoRoot string) (*Layout, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}

	l := &Layout{
		RepoRoot:   abs,
		FilesByDir: make(map[string][]string),
		CalledDirs: make(map[string]bool),
	}

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is a fact about the repository, not a reason to
			// abandon the whole walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != abs && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}

		name := d.Name()
		if !strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json") {
			return nil
		}

		rel, rerr := filepath.Rel(abs, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = ""
		}
		l.FilesByDir[dir] = append(l.FilesByDir[dir], rel)
		return nil
	})
	if err != nil {
		return nil, err
	}

	for dir, files := range l.FilesByDir {
		sort.Strings(files)
		l.Dirs = append(l.Dirs, dir)
	}
	sort.Strings(l.Dirs)

	return l, nil
}

// MarkCalled records that a local module source resolved to dir, which disqualifies it
// from being a root unless it carries a positive root signal of its own.
func (l *Layout) MarkCalled(dir string) { l.CalledDirs[dir] = true }

// ResolveLocalSource turns a `source = "../modules/vpc"` on a module call in fromDir into
// a repo-relative directory. Returns "" and false when the source is a registry or git
// reference, or escapes the repository.
func (l *Layout) ResolveLocalSource(fromDir, source string) (string, bool) {
	if !isLocalSource(source) {
		return "", false
	}
	joined := filepath.ToSlash(filepath.Clean(filepath.Join(fromDir, source)))
	if joined == "." {
		joined = ""
	}
	if strings.HasPrefix(joined, "..") {
		return "", false
	}
	if _, ok := l.FilesByDir[joined]; !ok {
		return "", false
	}
	return joined, true
}

func isLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}

// ComputeRoots finalises root classification. It must run after every module call has been
// seen, because the primary signal is that nothing calls the directory.
func (l *Layout) ComputeRoots(explicit map[string]bool) {
	l.Roots = nil
	for _, dir := range l.Dirs {
		if !l.CalledDirs[dir] || explicit[dir] {
			l.Roots = append(l.Roots, dir)
		}
	}
	sort.Strings(l.Roots)
}

// OwningStack returns the root module a directory belongs to. A root owns itself. A child
// module called from exactly one root belongs to that root; one shared between several
// roots belongs to none, because claiming an arbitrary owner would make stack-scoped
// answers quietly wrong.
func (l *Layout) OwningStack(dir string, callers map[string][]string) string {
	for _, r := range l.Roots {
		if r == dir {
			return dir
		}
	}
	seen := map[string]bool{}
	for _, c := range callers[dir] {
		seen[c] = true
	}
	if len(seen) == 1 {
		for c := range seen {
			return c
		}
	}
	return ""
}

// HasTfvars reports whether a directory carries .tfvars files, a positive root signal.
func HasTfvars(repoRoot, dir string) bool {
	entries, err := os.ReadDir(filepath.Join(repoRoot, dir))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".tfvars") || strings.HasSuffix(n, ".tfvars.json") {
			return true
		}
	}
	return false
}

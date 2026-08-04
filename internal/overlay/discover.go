package overlay

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Patterns matched inside a root module directory, in preference order. A plan is
// preferred over a state because it can answer replace-vs-update and a state cannot.
var (
	planPatterns  = []string{"*.tfplan.json", "tfplan.json", "plan.json"}
	statePatterns = []string{"*.tfstate.json", "tfstate.json", "state.json"}
)

// Found is one overlay file located on disk, before it is parsed.
type Found struct {
	Stack string
	Path  string
}

// Discover looks for an overlay file in each root module directory.
//
// Placement is the convention rather than content inspection: a plan document does not
// record which directory produced it, and guessing from the resource addresses would be
// wrong for the common case of two stacks that deploy the same module. A file sitting in
// the directory you ran `terraform plan` in is unambiguous.
func Discover(repoRoot string, roots []string) []Found {
	var found []Found

	for _, root := range roots {
		dir := filepath.Join(repoRoot, root)
		if path, ok := firstMatch(dir, planPatterns); ok {
			found = append(found, Found{Stack: root, Path: path})
			continue
		}
		if path, ok := firstMatch(dir, statePatterns); ok {
			found = append(found, Found{Stack: root, Path: path})
		}
	}
	return found
}

func firstMatch(dir string, patterns []string) (string, bool) {
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil || len(matches) == 0 {
			continue
		}
		sort.Strings(matches)
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && !info.IsDir() {
				return m, true
			}
		}
	}
	return "", false
}

// ParseExplicit reads the --plan flag: comma-separated `stack=path` pairs, or a bare path
// when the repository has exactly one root.
func ParseExplicit(spec string, roots []string) ([]Found, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	var out []Found
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		stack, path, hasEquals := strings.Cut(part, "=")
		if !hasEquals {
			if len(roots) != 1 {
				return nil, &AmbiguousStackError{Spec: part, Roots: roots}
			}
			out = append(out, Found{Stack: roots[0], Path: part})
			continue
		}
		out = append(out, Found{
			Stack: strings.Trim(strings.TrimSpace(stack), "/"),
			Path:  strings.TrimSpace(path),
		})
	}
	return out, nil
}

// AmbiguousStackError is returned when a bare overlay path cannot be attributed to a root.
type AmbiguousStackError struct {
	Spec  string
	Roots []string
}

func (e *AmbiguousStackError) Error() string {
	return "cannot tell which root module '" + e.Spec + "' belongs to; this repository has " +
		itoa(len(e.Roots)) + " roots (" + strings.Join(e.Roots, ", ") +
		"). Use --plan <root>=<path>."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Build loads every located overlay, keeping partial failures visible.
//
// One unreadable file does not stop the others: an overlay that covers three stacks out of
// four is still worth having, and the gap is reported rather than silently equated with
// having no overlay at all.
func Build(found []Found) Resolver {
	if len(found) == 0 {
		return None("no plan or state JSON found in any root module directory")
	}

	var loaded []*Overlay
	var partial []string

	for _, f := range found {
		o, err := LoadFile(f.Stack, f.Path)
		if err != nil {
			partial = append(partial, err.Error())
			continue
		}
		loaded = append(loaded, o)
	}

	if len(loaded) == 0 {
		return None(strings.Join(partial, "; "))
	}
	return NewSet(loaded, partial)
}

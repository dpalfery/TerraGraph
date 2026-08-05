package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectTargetsDeduplicates(t *testing.T) {
	root := t.TempDir()
	out, code := selectTargets(stringList{"claude", "claude-code", "claude"}, false, root)
	if code != 0 {
		t.Fatalf("selectTargets exit %d", code)
	}
	if len(out) != 1 {
		t.Fatalf("got %d targets, want 1 after dedupe", len(out))
	}
	if out[0].ID != "claude" {
		t.Fatalf("got %q, want claude", out[0].ID)
	}
}

func TestSelectTargetsRejectsUnknown(t *testing.T) {
	root := t.TempDir()
	_, code := selectTargets(stringList{"emacs"}, false, root)
	if code == 0 {
		t.Fatal("unknown tool should fail")
	}
}

func TestRunInitRejectsPositionalArgs(t *testing.T) {
	stderr := captureStderr(t, func() {
		if code := runInit([]string{"claude"}); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "unexpected arguments") {
		t.Fatalf("stderr missing unexpected-arguments message:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--tool") {
		t.Fatalf("stderr should point at --tool:\n%s", stderr)
	}
}

func TestRunInitRejectsAllWithTool(t *testing.T) {
	root := t.TempDir()
	stderr := captureStderr(t, func() {
		if code := runInit([]string{"--repo", root, "--all", "--tool", "claude"}); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
	})
	if !strings.Contains(stderr, "--all and --tool cannot be used together") {
		t.Fatalf("stderr missing conflict message:\n%s", stderr)
	}
}

func TestRunInitDeduplicatesRepeatedTools(t *testing.T) {
	root := t.TempDir()
	stdout := captureStdout(t, func() {
		// Point --binary at a path that need not exist: Plan only records the command string.
		if code := runInit([]string{
			"--repo", root,
			"--tool", "claude",
			"--tool", "claude-code",
			"--binary", "terragraph-mcp",
			"--dry-run",
		}); code != 0 {
			t.Fatalf("exit %d, want 0", code)
		}
	})
	if n := strings.Count(stdout, "Claude Code"); n != 1 {
		t.Fatalf("Claude Code appeared %d times, want 1:\n%s", n, stdout)
	}
	mcp := filepath.Join(root, ".mcp.json")
	if _, err := os.Stat(mcp); !os.IsNotExist(err) {
		t.Fatalf("dry-run should not write %s: %v", mcp, err)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureFD(t, &os.Stderr, fn)
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureFD(t, &os.Stdout, fn)
}

func captureFD(t *testing.T, fd **os.File, fn func()) string {
	t.Helper()
	old := *fd
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*fd = w
	// Buffer the result so a t.Fatalf inside fn (which Goexits after running defers)
	// cannot leave the reader blocked forever on an unbuffered send.
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		_ = r.Close()
		done <- buf.String()
	}()
	defer func() {
		// Close the write end before restoring *fd. If fn calls t.Fatalf, this defer is
		// what unblocks the reader; without it the pipe stays open and the test hangs
		// until the suite timeout.
		_ = w.Close()
		*fd = old
	}()
	fn()
	_ = w.Close()
	return <-done
}

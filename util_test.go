package main

import (
	"os"
	"strings"
	"testing"
)

func TestTruncateLines(t *testing.T) {
	in := "aaaa\nbbbb\ncccc\ndddd"
	out := truncateLines(in, 2, 2)
	if out != "cc\ndd" {
		t.Errorf("got %q", out)
	}
	if truncateLines("héllo wörld", 5, 5) != "héllo" {
		t.Error("rune-aware width truncation failed")
	}
}

func TestLastNonEmptyLines(t *testing.T) {
	in := "a\nb\nc\n\n\n"
	if got := lastNonEmptyLines(in, 2); got != "b\nc" {
		t.Errorf("got %q", got)
	}
	if got := lastNonEmptyLines(in, 0); got != "a\nb\nc" {
		t.Errorf("uncapped got %q", got)
	}
}

func TestCollapseHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := collapseHome(home + "/x"); got != "~/x" {
		t.Errorf("got %q", got)
	}
	if got := collapseHome("/tmp/x"); got != "/tmp/x" {
		t.Errorf("got %q", got)
	}
}

func TestStripANSIAndPad(t *testing.T) {
	if stripANSI("\x1b[32mok\x1b[0m") != "ok" {
		t.Error("stripANSI failed")
	}
	if pad("ab", 4) != "ab  " || pad("abcd", 2) != "abcd" {
		t.Error("pad failed")
	}
}

func TestSessionNameFor(t *testing.T) {
	got := sessionNameFor("/x/my.proj", "fix branch!")
	if got != "cbox-my_proj-fixbranch" {
		t.Errorf("got %q", got)
	}
	if !strings.HasPrefix(sessionNameFor("/a/b", ""), "cbox-b") {
		t.Error("prefix missing")
	}
}

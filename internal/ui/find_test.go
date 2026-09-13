package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// openProbe writes a file, previews it, and starts a find.
func openProbe(t *testing.T, m *Model, name, body string) {
	t.Helper()
	p := filepath.Join(m.idx.Root(), name)
	mustWrite(t, p, body)
	m.Update(fileMsg{f: m.loader.Load(m.hostFS, p, name), line: 1})
	if m.file == nil || m.file.Err != nil {
		t.Fatalf("could not preview %s: %+v", name, m.file)
	}
	m.setFocus(focusPreview)
	m.startFind()
}

// highlightedText returns what the preview actually renders inside the
// highlight styling, which is the thing that was wrong.
func highlightedText(t *testing.T, m *Model) []string {
	t.Helper()
	var out []string
	for _, hit := range m.findMatches {
		line := m.file.Plain[hit.line]
		out = append(out, ansi.Strip(ansi.Cut(line, hit.start, hit.end)))
	}
	return out
}

const probeTS = `"use client";

import { useCallback, useEffect, useState } from "react";
import type { Critique, Footprint, Layout } from "@/lib/types";

export type ConvTurn = {
  completedAt?: number;
  startedAt: number;
};

export type ConvProject = {
  footprint: Footprint | null;
  createdAt: number;
};
`

// TestFindHighlightsTheRightText is the reported bug: searching for "foo"
// highlighted "Sta" and "dAt" instead. The count was right; every position was
// shifted by however many escape bytes the syntax highlighting had emitted.
func TestFindHighlightsTheRightText(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)

	m.applyFind("foo")
	if m.findHits == 0 {
		t.Fatal("footprint and Footprint both contain foo")
	}
	for i, got := range highlightedText(t, m) {
		if !strings.EqualFold(got, "foo") {
			t.Errorf("hit %d highlights %q, want foo", i, got)
		}
	}
}

func TestFindLineNumbersAreRight(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)

	m.applyFind("useState")
	if len(m.findMatches) != 1 {
		t.Fatalf("expected one useState, got %d", len(m.findMatches))
	}
	// The import is the third line of the file, index 2.
	if got := m.findMatches[0].line; got != 2 {
		t.Errorf("line = %d, want 2", got)
	}
	if got := m.file.Plain[m.findMatches[0].line]; !strings.Contains(got, "useState") {
		t.Errorf("the recorded line does not contain the match: %q", got)
	}
	// And the caret follows it, which is what the gutter shows.
	if m.fileLine != 3 {
		t.Errorf("caret line = %d, want 3 (1-based)", m.fileLine)
	}
}

func TestFindColumnsSurviveSyntaxColouring(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)
	m.applyFind("number")

	for _, hit := range m.findMatches {
		plain := m.file.Plain[hit.line]
		styled := m.file.Styled[hit.line]
		// The styled line carries escapes; the columns are measured on the
		// plain one and must still cut the same text out of both.
		if !strings.Contains(styled, "\x1b[") {
			continue // nothing to colour on this line
		}
		fromPlain := ansi.Strip(ansi.Cut(plain, hit.start, hit.end))
		fromStyled := ansi.Strip(ansi.Cut(styled, hit.start, hit.end))
		if fromPlain != fromStyled {
			t.Errorf("line %d: plain cut %q, styled cut %q", hit.line, fromPlain, fromStyled)
		}
		if fromStyled != "number" {
			t.Errorf("line %d highlights %q", hit.line, fromStyled)
		}
	}
}

func TestFindLeavesTheTextIntact(t *testing.T) {
	// Splicing a highlight in must not change a single visible character.
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)

	before := make([]string, len(m.file.Plain))
	copy(before, m.file.Plain)

	m.applyFind("Foo")
	rendered := strings.Split(m.prev.GetContent(), "\n")
	for i, want := range before {
		if i >= len(rendered) {
			break
		}
		if got := ansi.Strip(rendered[i]); got != want {
			t.Errorf("line %d became %q, want %q", i, got, want)
		}
	}
}

func TestFindNavigatesAndWraps(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)
	m.applyFind("At")

	n := len(m.findMatches)
	if n < 3 {
		t.Fatalf("expected several matches, got %d", n)
	}
	if m.findSel != 0 {
		t.Errorf("the first match should start selected, got %d", m.findSel)
	}
	m.stepFind(1)
	if m.findSel != 1 {
		t.Errorf("next selected %d, want 1", m.findSel)
	}
	m.stepFind(-1)
	m.stepFind(-1)
	if m.findSel != n-1 {
		t.Errorf("stepping back past the start selected %d, want %d", m.findSel, n-1)
	}
	// The caret follows the selection so the gutter marks the right line.
	if m.fileLine != m.findMatches[m.findSel].line+1 {
		t.Errorf("caret is on line %d, selection is on %d",
			m.fileLine, m.findMatches[m.findSel].line+1)
	}
}

func TestFindWithNoMatchClearsEverything(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)

	m.applyFind("footprint")
	if m.findHits == 0 {
		t.Fatal("setup: expected matches")
	}
	m.applyFind("zzz-not-here")
	if m.findHits != 0 || len(m.findMatches) != 0 {
		t.Errorf("stale matches survived: %d", m.findHits)
	}
	// And the preview is back to the untouched highlighting.
	if m.prev.GetContent() != strings.Join(m.file.Styled, "\n") {
		t.Error("the preview was not restored after the matches went away")
	}
}

func TestFindSmartCase(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "useProject.ts", probeTS)

	m.applyFind("footprint") // lowercase: matches both spellings
	lower := m.findHits
	m.applyFind("Footprint") // has a capital: exact only
	upper := m.findHits

	if lower <= upper {
		t.Errorf("lowercase found %d and mixed case %d; smart case should widen the first",
			lower, upper)
	}
	for i, got := range highlightedText(t, m) {
		if got != "Footprint" {
			t.Errorf("hit %d highlights %q, want the exact spelling", i, got)
		}
	}
}

func TestFindHandlesNonASCII(t *testing.T) {
	// Lowercasing can change byte lengths, which would shift every column
	// after it on the line.
	m := newTestModel(t)
	openProbe(t, m, "notes.md", "# Ghi chú\n\nsửa lỗi tìm kiếm trong dự án\nfind the needle here\n")

	m.applyFind("needle")
	if len(m.findMatches) != 1 {
		t.Fatalf("matches = %d, want 1", len(m.findMatches))
	}
	if got := highlightedText(t, m)[0]; got != "needle" {
		t.Errorf("highlighted %q", got)
	}

	m.applyFind("lỗi")
	if len(m.findMatches) != 1 {
		t.Fatalf("Vietnamese match count = %d, want 1", len(m.findMatches))
	}
	if got := highlightedText(t, m)[0]; got != "lỗi" {
		t.Errorf("highlighted %q, want lỗi", got)
	}
}

func TestFindIsBounded(t *testing.T) {
	m := newTestModel(t)
	openProbe(t, m, "big.txt", strings.Repeat("aaaa\n", 3000))
	m.applyFind("aa")
	if m.findHits > maxFindHits {
		t.Errorf("collected %d hits, want at most %d", m.findHits, maxFindHits)
	}
}

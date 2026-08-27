package ui

import (
	"regexp"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
)

type rangeSelection struct {
	active     bool
	anchor     int
	end        int
	dragging   bool
	dragAnchor int
}

var selectionSGRRe = regexp.MustCompile(`\x1b\[[0-9:;]*m`)

func (m Model) selectionRow(row string, idx int) string {
	if !m.selectionContains(idx) {
		return row
	}
	// Inner line styling can emit resets that disable an outer reverse-video
	// envelope. Re-enable reverse video after every SGR sequence so gutters,
	// syntax colors, word-diff spans, and wrapped rows stay selected end to end.
	row = selectionSGRRe.ReplaceAllString(row, "$0\x1b[7m")
	return "\033[7m" + row + "\033[27m"
}

func (m Model) selectionContains(idx int) bool {
	if !m.annot.selection.active {
		return false
	}
	lo, hi := m.annot.selection.anchor, m.annot.selection.end
	if lo > hi {
		lo, hi = hi, lo
	}
	return idx >= lo && idx <= hi
}

func (m *Model) clearSelection() {
	m.annot.selection = rangeSelection{}
	m.invalidateRenderCaches()
}

func (m Model) validSelectionRow(idx int) bool {
	if idx < 0 || idx >= len(m.file.lines) || m.annot.cursorOnAnnotation || m.file.requestedPath != "" {
		return false
	}
	dl := m.file.lines[idx]
	if !isHunkChange(dl.ChangeType) || dl.IsBinary || dl.IsPlaceholder {
		return false
	}
	hunks := m.findHunks()
	return !m.isDeleteOnlyPlaceholder(idx, hunks)
}

func (m *Model) beginSelection(idx int) bool {
	if !m.validSelectionRow(idx) {
		m.output.hint = "Select an added or removed line"
		return false
	}
	m.ensureSelectionHunkExpanded(idx)
	m.annot.cursorOnAnnotation = false
	m.nav.diffCursor = idx
	m.annot.selection = rangeSelection{active: true, anchor: idx, end: idx}
	m.invalidateRenderCaches()
	m.layout.viewport.SetContent(m.renderDiff())
	return true
}

func (m *Model) extendSelectionTo(idx int) bool {
	if !m.annot.selection.active {
		return false
	}
	r, ok := m.hunkRangeAt(m.annot.selection.anchor)
	if !ok {
		m.clearSelection()
		return false
	}
	idx = max(r.start, min(idx, r.end-1))
	if !m.validSelectionRow(idx) {
		m.output.hint = "Selection stays within the current hunk"
		return false
	}
	m.nav.diffCursor = idx
	m.annot.cursorOnAnnotation = false
	m.annot.selection.end = idx
	m.invalidateRenderCaches()
	m.syncViewportToCursor()
	return true
}

// ensureSelectionHunkExpanded makes every selected underlying row visible in
// collapsed view, including removals when selection starts from a visible add.
func (m *Model) ensureSelectionHunkExpanded(idx int) {
	if !m.modes.collapsed.enabled {
		return
	}
	r, ok := m.hunkRangeAt(idx)
	if ok {
		if m.modes.collapsed.expandedHunks == nil {
			m.modes.collapsed.expandedHunks = make(map[int]bool)
		}
		m.modes.collapsed.expandedHunks[r.start] = true
	}
}

func (m Model) handleSelectRange() (tea.Model, tea.Cmd) {
	if m.layout.focus != paneDiff {
		return m, nil
	}
	m.beginSelection(m.nav.diffCursor)
	return m, nil
}

func (m Model) handleAnnotateHunk() (tea.Model, tea.Cmd) {
	if m.layout.focus != paneDiff || !m.validSelectionRow(m.nav.diffCursor) {
		m.output.hint = "Cursor is not on a diff hunk"
		return m, nil
	}
	r, _ := m.hunkRangeAt(m.nav.diffCursor)
	m.ensureSelectionHunkExpanded(m.nav.diffCursor)
	m.annot.selection = rangeSelection{active: true, anchor: r.start, end: r.end - 1}
	cmd := m.startScopedAnnotation(annotation.ScopeHunk, r)
	m.layout.viewport.SetContent(m.renderDiff())
	return m, cmd
}

func (m *Model) startScopedAnnotation(scope annotation.Scope, r hunkRange) tea.Cmd {
	target, ok := m.annotationFromRows(scope, r.start, r.end)
	if !ok {
		m.output.hint = "Selection is unavailable"
		return nil
	}
	m.annot.target = &target
	return m.startAnnotationInput(target)
}

func (m Model) selectedRange() (hunkRange, bool) {
	if !m.annot.selection.active {
		return hunkRange{}, false
	}
	lo, hi := m.annot.selection.anchor, m.annot.selection.end
	if lo > hi {
		lo, hi = hi, lo
	}
	if _, ok := m.hunkRangeAt(lo); !ok {
		return hunkRange{}, false
	}
	r, ok := m.hunkRangeAt(hi)
	if !ok || lo < r.start {
		return hunkRange{}, false
	}
	return hunkRange{start: lo, end: hi + 1}, true
}

func (m Model) annotationFromRows(scope annotation.Scope, start, end int) (annotation.Annotation, bool) {
	if start < 0 || end > len(m.file.lines) || start >= end {
		return annotation.Annotation{}, false
	}
	a := annotation.Annotation{File: m.file.name, Scope: scope}
	oldStart, newStart := 0, 0
	for i := start; i < end; i++ {
		dl := m.file.lines[i]
		if !isHunkChange(dl.ChangeType) || dl.IsBinary || dl.IsPlaceholder {
			return annotation.Annotation{}, false
		}
		kind := string(dl.ChangeType)
		a.Excerpt = append(a.Excerpt, annotation.ExcerptLine{Type: kind, Content: dl.Content})
		if dl.ChangeType == diff.ChangeRemove {
			if oldStart == 0 {
				oldStart = dl.OldNum
			}
			a.OldCount++
		} else {
			if newStart == 0 {
				newStart = dl.NewNum
			}
			a.NewCount++
		}
	}
	if oldStart == 0 {
		oldStart = m.zeroCountStart(start, true)
	}
	if newStart == 0 {
		newStart = m.zeroCountStart(start, false)
	}
	a.OldStart, a.NewStart = oldStart, newStart
	first := m.file.lines[start]
	a.Type = string(first.ChangeType)
	a.Line = m.diffLineNum(first)
	return a, true
}

// zeroCountStart returns the unified-diff insertion point for a side with no
// selected rows. A zero-count range starts after the nearest preceding row on
// that side, or at zero when the selection is at the beginning of the file.
func (m Model) zeroCountStart(start int, oldSide bool) int {
	anchor := m.file.lines[start].NewAnchor
	if oldSide {
		anchor = m.file.lines[start].OldAnchor
	}
	if anchor > 0 {
		return anchor
	}
	for i := start - 1; i >= 0; i-- {
		n := m.file.lines[i].NewNum
		if oldSide {
			n = m.file.lines[i].OldNum
		}
		if n > 0 {
			return n
		}
	}
	return 0
}

func (m *Model) extendSelectionForAction(action keymap.Action) bool {
	if !m.annot.selection.active {
		return false
	}
	r, ok := m.hunkRangeAt(m.annot.selection.anchor)
	if !ok {
		m.clearSelection()
		return true
	}
	target := m.annot.selection.end
	switch action {
	case keymap.ActionDown:
		target++
	case keymap.ActionUp:
		target--
	case keymap.ActionPageDown, keymap.ActionHalfPageDown, keymap.ActionEnd:
		target = r.end - 1
	case keymap.ActionPageUp, keymap.ActionHalfPageUp, keymap.ActionHome:
		target = r.start
	default:
		return false
	}
	if target < r.start || target >= r.end {
		m.output.hint = "Selection stays within the current hunk"
		target = max(r.start, min(target, r.end-1))
	}
	m.extendSelectionTo(target)
	return true
}

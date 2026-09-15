package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/overlay"
)

func selectionFixture() []diff.DiffLine {
	return []diff.DiffLine{
		{OldNum: 9, NewNum: 9, Content: "before", ChangeType: diff.ChangeContext},
		{OldNum: 10, Content: "old one", ChangeType: diff.ChangeRemove},
		{OldNum: 11, Content: "old two", ChangeType: diff.ChangeRemove},
		{NewNum: 10, Content: "new one", ChangeType: diff.ChangeAdd},
		{NewNum: 11, Content: "new two", ChangeType: diff.ChangeAdd},
		{OldNum: 12, NewNum: 12, Content: "between", ChangeType: diff.ChangeContext},
		{NewNum: 13, Content: "other", ChangeType: diff.ChangeAdd},
	}
}

func newSelectionModel(t *testing.T) Model {
	t.Helper()
	m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{})
	m.layout.focus = paneDiff
	m.layout.width, m.layout.height = 100, 30
	m.layout.viewport.Width, m.layout.viewport.Height = 96, 25
	m.file.name = "dir/a.go"
	m.file.lines = selectionFixture()
	m.nav.diffCursor = 1
	m.layout.viewport.SetContent(m.renderDiff())
	return m
}

func TestModelKeyboardRangeSelectionAndSave(t *testing.T) {
	m := newSelectionModel(t)
	result, _ := m.dispatchAction(keymap.ActionSelectRange)
	m = result.(Model)
	require.True(t, m.annot.selection.active)
	assert.Equal(t, 1, m.annot.selection.anchor)

	result, _ = m.dispatchAction(keymap.ActionDown)
	m = result.(Model)
	result, _ = m.dispatchAction(keymap.ActionDown)
	m = result.(Model)
	assert.Equal(t, 3, m.annot.selection.end)

	result, _ = m.dispatchAction(keymap.ActionUp)
	m = result.(Model)
	assert.Equal(t, 2, m.annot.selection.end)
	result, _ = m.dispatchAction(keymap.ActionDown)
	m = result.(Model)

	result, _ = m.dispatchAction(keymap.ActionConfirm)
	m = result.(Model)
	require.True(t, m.annot.annotating)
	m.annot.input.SetValue("replace these rows")
	m.saveAnnotation()

	const want = "## dir/a.go @@ -10,2 +10,1 @@ (range)\n-old one\n-old two\n+new one\n\nreplace these rows\n"
	assert.Equal(t, want, m.store.FormatOutput())
	assert.False(t, m.annot.selection.active)
	assert.True(t, strings.HasSuffix(m.store.FormatOutput(), "\n"))
}

func TestModelSpaceFinishesRangeSelectionWithoutClearingRows(t *testing.T) {
	m := newSelectionModel(t)
	result, _ := m.dispatchAction(keymap.ActionSelectRange)
	m = result.(Model)
	result, _ = m.dispatchAction(keymap.ActionDown)
	m = result.(Model)
	result, _ = m.dispatchAction(keymap.ActionDown)
	m = result.(Model)
	require.True(t, m.annot.selection.selecting)

	result, _ = m.dispatchAction(keymap.ActionSelectRange)
	m = result.(Model)
	assert.True(t, m.annot.selection.active)
	assert.False(t, m.annot.selection.selecting)
	assert.Equal(t, 1, m.annot.selection.anchor)
	assert.Equal(t, 3, m.annot.selection.end)

	result, _ = m.dispatchAction(keymap.ActionDown)
	m = result.(Model)
	assert.Equal(t, 4, m.nav.diffCursor)
	assert.Equal(t, 1, m.annot.selection.anchor)
	assert.Equal(t, 3, m.annot.selection.end)

	result, _ = m.dispatchAction(keymap.ActionSelectRange)
	m = result.(Model)
	assert.True(t, m.annot.selection.selecting)
	assert.Equal(t, 4, m.annot.selection.anchor)
	assert.Equal(t, 4, m.annot.selection.end)
}

func TestModelRangeAnnotationInputFollowsBottomSelection(t *testing.T) {
	tests := []struct {
		name   string
		anchor int
		end    int
		mouse  bool
	}{
		{name: "keyboard forward", anchor: 1, end: 4},
		{name: "keyboard reverse", anchor: 4, end: 1},
		{name: "mouse forward", anchor: 1, end: 4, mouse: true},
		{name: "mouse reverse", anchor: 4, end: 1, mouse: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSelectionModel(t)
			if tt.mouse {
				m.annot.selection.dragAnchor = tt.anchor
				m.nav.diffCursor = tt.anchor
				result, _ := m.dragDiffSelection(m.diffTopRow() + tt.end)
				m = result.(Model)
			} else {
				require.True(t, m.beginSelection(tt.anchor))
				require.True(t, m.extendSelectionTo(tt.end))
			}
			require.Equal(t, tt.end, m.nav.diffCursor, "selection direction controls the cursor endpoint")

			m.startAnnotation()
			m.annot.input.SetValue("range input")
			plain := ansi.Strip(m.renderDiff())
			inputAt := strings.Index(plain, "range input")
			require.Greater(t, inputAt, 0)
			for _, source := range []string{"old one", "old two", "new one", "new two"} {
				assert.Less(t, strings.Index(plain, source), inputAt, source+" must remain above the input")
			}
			assert.Equal(t, tt.end, m.nav.diffCursor, "input placement must not move the cursor")
		})
	}
}

func TestModelRangeAnnotationInputHandlesWrapCollapsedAndViewportBottom(t *testing.T) {
	t.Run("wrapped bottom row", func(t *testing.T) {
		m := newSelectionModel(t)
		m.layout.width = 32
		m.layout.viewport.Width = 28
		m.modes.wrap = true
		m.file.lines[4].Content = "new two " + strings.Repeat("wrapped ", 12)
		require.True(t, m.beginSelection(1))
		require.True(t, m.extendSelectionTo(4))
		m.startAnnotation()
		m.annot.input.SetValue("range input")

		plain := ansi.Strip(m.renderDiff())
		assert.Greater(t, strings.Index(plain, "range input"), strings.LastIndex(plain, "wrapped"))
	})

	t.Run("collapsed hunk", func(t *testing.T) {
		m := newSelectionModel(t)
		m.modes.collapsed.enabled = true
		m.modes.collapsed.expandedHunks = make(map[int]bool)
		require.True(t, m.beginSelection(4))
		require.True(t, m.extendSelectionTo(1))
		m.startAnnotation()
		m.annot.input.SetValue("range input")

		plain := ansi.Strip(m.renderDiff())
		assert.Contains(t, plain, "old one")
		assert.Greater(t, strings.Index(plain, "range input"), strings.Index(plain, "new two"))
	})

	t.Run("reverse selection near viewport bottom", func(t *testing.T) {
		m := newSelectionModel(t)
		m.layout.viewport = viewport.New(96, 5)
		m.layout.viewport.SetContent(m.renderDiff())
		m.layout.viewport.SetYOffset(0)
		require.True(t, m.beginSelection(4))
		require.True(t, m.extendSelectionTo(1))
		m.startAnnotation()
		m.annot.input.SetValue("range input")
		m.layout.viewport.SetContent(m.renderDiff())

		assert.Equal(t, 1, m.layout.viewport.YOffset)
		visible := ansi.Strip(m.layout.viewport.View())
		assert.Contains(t, visible, "old one", "cursor endpoint remains visible")
		assert.Contains(t, visible, "range input", "input remains visible")
	})
}

func TestModelSavedRangeAnnotationFollowsBottomSelection(t *testing.T) {
	tests := []struct {
		name        string
		anchor, end int
		mouse       bool
	}{
		{name: "keyboard forward", anchor: 1, end: 4},
		{name: "keyboard reverse", anchor: 4, end: 1},
		{name: "mouse forward", anchor: 1, end: 4, mouse: true},
		{name: "mouse reverse", anchor: 4, end: 1, mouse: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSelectionModel(t)
			if tt.mouse {
				m.annot.selection.dragAnchor = tt.anchor
				m.nav.diffCursor = tt.anchor
				result, _ := m.dragDiffSelection(m.diffTopRow() + tt.end)
				m = result.(Model)
			} else {
				require.True(t, m.beginSelection(tt.anchor))
				require.True(t, m.extendSelectionTo(tt.end))
			}
			m.startAnnotation()
			m.annot.input.SetValue("saved range")
			m.saveAnnotation()

			plain := ansi.Strip(m.renderDiff())
			commentAt := strings.Index(plain, "saved range")
			require.Greater(t, commentAt, 0)
			for _, source := range []string{"old one", "old two", "new one", "new two"} {
				assert.Less(t, strings.Index(plain, source), commentAt)
			}
		})
	}
}

func TestModelPersistedHunkAnnotationRoundTripPlacementAndInteraction(t *testing.T) {
	m := newSelectionModel(t)
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
	require.True(t, ok)
	hunk.Comment = strings.Repeat("wrapped persisted comment ", 8) + "\nsecond line"
	m.store.Add(hunk)

	output := m.store.FormatOutput()
	reloaded := annotation.NewStore()
	require.NoError(t, reloaded.Load(strings.NewReader(output)))
	assert.Equal(t, output, reloaded.FormatOutput())
	m.store = reloaded
	persisted := m.store.Get(m.file.name)[0]
	owner, ok := m.annotationDisplayOwnerIndex(persisted)
	require.True(t, ok)
	assert.Equal(t, 4, owner)

	m.layout.width = 38
	m.layout.viewport = viewport.New(34, 5)
	plain := ansi.Strip(m.renderDiff())
	commentAt := strings.Index(plain, "wrapped persisted")
	require.Greater(t, commentAt, strings.Index(plain, "new two"))
	key := m.annotationKey(11, "+")
	assert.Greater(t, m.wrappedAnnotationLineCount(key), 1)

	m.positionOnAnnotation(persisted)
	assert.Equal(t, owner, m.nav.diffCursor)
	assert.True(t, m.annot.cursorOnAnnotation)
	assert.Greater(t, m.layout.viewport.YOffset, 0)
	row := m.cursorViewportY()
	idx, onAnnotation := m.visualRowToDiffLine(row)
	assert.Equal(t, owner, idx)
	assert.True(t, onAnnotation)

	m.startAnnotation()
	assert.Equal(t, persisted.Comment, m.annot.existingMultiline)
	m.cancelAnnotation()
	m.positionOnAnnotation(persisted)
	m.deleteAnnotation()
	assert.Zero(t, m.store.Count())
}

func TestModelPersistedScopedAnnotationCollapsedOwnerAndLineFileRegressions(t *testing.T) {
	m := newSelectionModel(t)
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
	require.True(t, ok)
	hunk.Comment = "persisted hunk"
	m.store.Add(hunk)
	m.store.Add(annotation.Annotation{File: m.file.name, Line: 12, Type: " ", Comment: "line note"})
	m.store.Add(annotation.Annotation{File: m.file.name, Comment: "file note"})

	m.modes.collapsed.enabled = true
	m.modes.collapsed.expandedHunks = make(map[int]bool)
	plain := ansi.Strip(m.renderDiff())
	assert.NotContains(t, plain, "old one")
	assert.Greater(t, strings.Index(plain, "persisted hunk"), strings.Index(plain, "new two"))
	assert.Less(t, strings.Index(plain, "file note"), strings.Index(plain, "before"))
	lineAt := strings.Index(plain, "line note")
	assert.Greater(t, lineAt, strings.Index(plain, "between"))
	assert.Less(t, lineAt, strings.Index(plain, "other"))
}

func TestModelDeleteHunkAnnotationFromAnyCanonicalHunkRow(t *testing.T) {
	tests := []struct {
		name   string
		cursor int
		mutate func(*Model)
	}{
		{name: "first remove", cursor: 1},
		{name: "middle remove wrapped", cursor: 2, mutate: func(m *Model) { m.modes.wrap = true }},
		{name: "middle add compact", cursor: 3, mutate: func(m *Model) { m.modes.compact = true }},
		{name: "last add collapsed", cursor: 4, mutate: func(m *Model) {
			m.modes.collapsed.enabled = true
			m.modes.collapsed.expandedHunks = map[int]bool{1: true}
		}},
		{name: "reverse origin", cursor: 3, mutate: func(m *Model) {
			require.True(t, m.beginSelection(4))
			require.True(t, m.extendSelectionTo(1))
			r, ok := m.selectedRange()
			require.True(t, ok)
			m.startScopedAnnotation(annotation.ScopeHunk, r)
			m.annot.input.SetValue("reverse hunk")
			m.saveAnnotation()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSelectionModel(t)
			if tt.mutate != nil {
				tt.mutate(&m)
			}
			if m.store.Count() == 0 {
				hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
				require.True(t, ok)
				hunk.Comment = "whole hunk"
				m.store.Add(hunk)
			}
			m.nav.diffCursor = tt.cursor
			m.annot.cursorOnAnnotation = false

			result, cmd := m.dispatchAction(keymap.ActionDeleteAnnotation)
			m = result.(Model)
			assert.Nil(t, cmd)
			assert.Zero(t, m.store.Count())
		})
	}
}

func TestModelDeletePreloadedHunkAnnotationFromDiffRow(t *testing.T) {
	m := newSelectionModel(t)
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
	require.True(t, ok)
	hunk.Comment = "preloaded"
	original := annotation.NewStore()
	original.Add(hunk)
	reloaded := annotation.NewStore()
	require.NoError(t, reloaded.Load(strings.NewReader(original.FormatOutput())))
	m.store = reloaded
	m.nav.diffCursor = 2

	m.deleteAnnotation()
	assert.Zero(t, m.store.Count())
}

func TestModelDeleteHunkAnnotationDiffRowPreservesLineAndRangeAnnotations(t *testing.T) {
	m := newSelectionModel(t)
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
	require.True(t, ok)
	hunk.Comment = "hunk"
	rng, ok := m.annotationFromRows(annotation.ScopeRange, 1, 3)
	require.True(t, ok)
	rng.Comment = "range"
	line := annotation.Annotation{File: m.file.name, Line: 11, Type: "+", Comment: "line"}
	m.store.Add(hunk)
	m.store.Add(rng)
	m.store.Add(line)
	m.nav.diffCursor = 4
	m.annot.cursorOnAnnotation = false

	m.deleteAnnotation()
	items := m.store.Get(m.file.name)
	require.Len(t, items, 2)
	assert.Equal(t, annotation.ScopeRange, items[0].Scope)
	assert.Equal(t, annotation.ScopeLine, items[1].Scope)

	m.annot.cursorOnAnnotation = true
	target := line
	m.annot.target = &target
	m.deleteAnnotation()
	items = m.store.Get(m.file.name)
	require.Len(t, items, 1)
	assert.Equal(t, annotation.ScopeRange, items[0].Scope)
}

func TestModelDeleteHunkAnnotationDiffRowNoOps(t *testing.T) {
	tests := []struct {
		name   string
		cursor int
		mutate func(*Model)
		add    bool
	}{
		{name: "context before", cursor: 0, add: true},
		{name: "context after", cursor: 5, add: true},
		{name: "adjacent hunk", cursor: 6, add: true},
		{name: "placeholder", cursor: 1, add: true, mutate: func(m *Model) { m.file.lines[1].IsPlaceholder = true }},
		{name: "unloaded file", cursor: 1, add: true, mutate: func(m *Model) { m.file.requestedPath = "other.go" }},
		{name: "hunk without annotation", cursor: 2},
		{name: "line annotation only", cursor: 4, mutate: func(m *Model) {
			m.store.Add(annotation.Annotation{File: m.file.name, Line: 11, Type: "+", Comment: "line"})
		}},
		{name: "range annotation only", cursor: 2, mutate: func(m *Model) {
			rng, ok := m.annotationFromRows(annotation.ScopeRange, 1, 3)
			require.True(t, ok)
			rng.Comment = "range"
			m.store.Add(rng)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSelectionModel(t)
			if tt.add {
				hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
				require.True(t, ok)
				hunk.Comment = "hunk"
				m.store.Add(hunk)
			}
			if tt.mutate != nil {
				tt.mutate(&m)
			}
			before := m.store.FormatOutput()
			m.nav.diffCursor = tt.cursor
			m.annot.cursorOnAnnotation = false
			m.deleteAnnotation()
			assert.Equal(t, before, m.store.FormatOutput())
		})
	}
}

func TestModelDeleteHunkAnnotationIgnoresCollapsedDeletePlaceholder(t *testing.T) {
	m := newSelectionModel(t)
	m.file.lines = []diff.DiffLine{
		{OldNum: 9, NewNum: 9, Content: "before", ChangeType: diff.ChangeContext},
		{OldNum: 10, Content: "gone one", ChangeType: diff.ChangeRemove},
		{OldNum: 11, Content: "gone two", ChangeType: diff.ChangeRemove},
		{OldNum: 12, NewNum: 10, Content: "after", ChangeType: diff.ChangeContext},
	}
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 3)
	require.True(t, ok)
	hunk.Comment = "delete-only hunk"
	m.store.Add(hunk)
	m.modes.collapsed.enabled = true
	m.modes.collapsed.expandedHunks = make(map[int]bool)
	m.nav.diffCursor = 1

	m.deleteAnnotation()
	assert.Equal(t, 1, m.store.Count())

	m.modes.collapsed.expandedHunks[1] = true
	m.nav.diffCursor = 2
	m.deleteAnnotation()
	assert.Zero(t, m.store.Count())
}

func TestModelDeleteHunkAnnotationRefreshesFilterAndFileNavigation(t *testing.T) {
	files := []string{"a.go", "b.go"}
	m := testModel(files, map[string][]diff.DiffLine{"b.go": {{NewNum: 1, Content: "b", ChangeType: diff.ChangeContext}}})
	m.tree = testNewFileTree(files)
	m.file.name = "a.go"
	m.file.lines = selectionFixture()
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
	require.True(t, ok)
	hunk.Comment = "hunk"
	m.store.Add(hunk)
	m.store.Add(annotation.Annotation{File: "b.go", Line: 1, Type: " ", Comment: "other"})
	m.tree.ToggleFilter(m.annotatedFiles())
	m.nav.diffCursor = 3
	pending := true
	m.nav.pendingHunkJump = &pending
	target := hunk
	m.pendingAnnotJump = &target

	cmd := m.deleteAnnotation()
	require.NotNil(t, cmd)
	assert.Nil(t, m.nav.pendingHunkJump)
	assert.Nil(t, m.pendingAnnotJump)
	assert.Equal(t, "b.go", m.tree.SelectedFile())
	assert.Equal(t, "b.go", m.file.requestedPath)
}

func TestModelPersistedRemovedRangeExpandsToVisibleOwnerOnJump(t *testing.T) {
	m := newSelectionModel(t)
	removed, ok := m.annotationFromRows(annotation.ScopeRange, 1, 3)
	require.True(t, ok)
	removed.Comment = "removed range"
	m.store.Add(removed)
	m.modes.collapsed.enabled = true
	m.modes.collapsed.expandedHunks = make(map[int]bool)

	assert.NotContains(t, ansi.Strip(m.renderDiff()), "removed range")
	m.positionOnAnnotation(removed)
	assert.Equal(t, 2, m.nav.diffCursor)
	assert.True(t, m.annot.cursorOnAnnotation)
	plain := ansi.Strip(m.renderDiff())
	assert.Greater(t, strings.Index(plain, "removed range"), strings.Index(plain, "old two"))
}

func TestModelRangeSelectionClampsAndCancels(t *testing.T) {
	m := newSelectionModel(t)
	m.beginSelection(1)
	m.extendSelectionForAction(keymap.ActionUp)
	assert.Equal(t, 1, m.annot.selection.end)
	assert.Contains(t, m.output.hint, "current hunk")
	m.extendSelectionForAction(keymap.ActionEnd)
	assert.Equal(t, 4, m.annot.selection.end)

	result, _ := m.dispatchAction(keymap.ActionDismiss)
	m = result.(Model)
	assert.False(t, m.annot.selection.active)
	assert.Empty(t, m.store.FormatOutput())
}

func TestModelEscFromRangeInputClearsSelection(t *testing.T) {
	m := newSelectionModel(t)
	m.beginSelection(1)
	result, _ := m.dispatchAction(keymap.ActionConfirm)
	m = result.(Model)
	require.True(t, m.annot.annotating)
	result, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = result.(Model)
	assert.False(t, m.annot.annotating)
	assert.False(t, m.annot.selection.active)
	assert.Empty(t, m.store.FormatOutput())
}

func TestModelRangeSelectionRejectsInvalidRows(t *testing.T) {
	m := newSelectionModel(t)
	for _, idx := range []int{0, 5} {
		m.nav.diffCursor = idx
		result, _ := m.dispatchAction(keymap.ActionSelectRange)
		m = result.(Model)
		assert.False(t, m.annot.selection.active)
		assert.Empty(t, m.store.FormatOutput())
	}
	m.file.lines[1].IsPlaceholder = true
	m.nav.diffCursor = 1
	result, _ := m.dispatchAction(keymap.ActionSelectRange)
	assert.False(t, result.(Model).annot.selection.active)
}

func TestModelWholeHunkTargetIsCanonicalFromEveryRow(t *testing.T) {
	var first annotation.Annotation
	for _, idx := range []int{1, 2, 4} {
		m := newSelectionModel(t)
		m.nav.diffCursor = idx
		result, _ := m.dispatchAction(keymap.ActionAnnotateHunk)
		m = result.(Model)
		require.NotNil(t, m.annot.target)
		if idx == 1 {
			first = *m.annot.target
		} else {
			assert.Equal(t, first, *m.annot.target)
		}
		assert.Equal(t, annotation.ScopeHunk, m.annot.target.Scope)
		m.annot.input.SetValue("whole change")
		plain := ansi.Strip(m.renderDiff())
		assert.Greater(t, strings.Index(plain, "whole change"), strings.Index(plain, "new two"))
		m.saveAnnotation()
		assert.Contains(t, m.store.FormatOutput(), "@@ -10,2 +10,2 @@ (hunk)")
		assert.Contains(t, m.store.FormatOutput(), "-old one\n-old two\n+new one\n+new two\n\nwhole change\n")
	}
}

func TestModelSingleRowRangeAndViewModeInvariant(t *testing.T) {
	m := newSelectionModel(t)
	m.nav.diffCursor = 3
	m.beginSelection(3)
	ann, ok := m.annotationFromRows(annotation.ScopeRange, 3, 4)
	require.True(t, ok)
	assert.Equal(t, 11, ann.OldStart)
	assert.Equal(t, 0, ann.OldCount)
	assert.Equal(t, 10, ann.NewStart)
	assert.Equal(t, 1, ann.NewCount)
	assert.Equal(t, []annotation.ExcerptLine{{Type: "+", Content: "new one"}}, ann.Excerpt)
	ann.Comment = "single"
	m.store.Add(ann)

	plain := m.store.FormatOutput()
	for _, mutate := range []func(*Model){
		func(mm *Model) { mm.modes.wrap = true },
		func(mm *Model) { mm.modes.lineNumbers = true; mm.file.lineNumWidth = 2 },
		func(mm *Model) { mm.modes.wordDiff = true },
		func(mm *Model) { mm.modes.collapsed.enabled = true; mm.ensureHunkExpanded(3) },
	} {
		mutate(&m)
		view := m.renderDiff()
		assert.Contains(t, view, "\x1b[7m", "selected source row stays highlighted")
		assert.Equal(t, plain, m.store.FormatOutput(), "rendering cannot affect structured output")
	}
}

func TestModelScopedZeroCountCoordinatesUseUnifiedInsertionPoints(t *testing.T) {
	m := newSelectionModel(t)

	added, ok := m.annotationFromRows(annotation.ScopeRange, 6, 7)
	require.True(t, ok)
	assert.Equal(t, 12, added.OldStart)
	assert.Zero(t, added.OldCount)
	assert.Equal(t, 13, added.NewStart)
	assert.Equal(t, 1, added.NewCount)

	m.file.lines = []diff.DiffLine{
		{OldNum: 20, NewNum: 20, Content: "before", ChangeType: diff.ChangeContext},
		{OldNum: 21, Content: "gone", ChangeType: diff.ChangeRemove},
		{OldNum: 22, NewNum: 21, Content: "after", ChangeType: diff.ChangeContext},
	}
	removed, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 2)
	require.True(t, ok)
	assert.Equal(t, 21, removed.OldStart)
	assert.Equal(t, 1, removed.OldCount)
	assert.Equal(t, 20, removed.NewStart)
	assert.Zero(t, removed.NewCount)
}

func TestModelScopedZeroCountCoordinatesUseParserAnchorsAcrossHiddenContext(t *testing.T) {
	m := newSelectionModel(t)
	m.file.lines = []diff.DiffLine{
		{Content: "⋯ 49 lines ⋯", ChangeType: diff.ChangeDivider},
		{NewNum: 51, OldAnchor: 50, Content: "inserted", ChangeType: diff.ChangeAdd},
	}

	added, ok := m.annotationFromRows(annotation.ScopeRange, 1, 2)
	require.True(t, ok)
	assert.Equal(t, 50, added.OldStart)
	assert.Zero(t, added.OldCount)
	assert.Equal(t, 51, added.NewStart)
}

func TestModelSelectionHighlightSurvivesInnerSGRResets(t *testing.T) {
	m := newSelectionModel(t)
	m.annot.selection = rangeSelection{active: true, anchor: 1, end: 1}
	row := "left\x1b[0mmiddle\x1b[27mright\x1b[31mred"

	got := m.selectionRow(row, 1)
	assert.Equal(t, "\x1b[7mleft\x1b[0m\x1b[7mmiddle\x1b[27m\x1b[7mright\x1b[31m\x1b[7mred\x1b[27m", got)
	assert.Equal(t, row, m.selectionRow(row, 0))
}

func TestModelScopedAnnotationsCoexistEditDeleteListAndNavigate(t *testing.T) {
	m := newSelectionModel(t)
	line := annotation.Annotation{File: m.file.name, Line: 10, Type: "+", Comment: "line"}
	rng, ok := m.annotationFromRows(annotation.ScopeRange, 3, 4)
	require.True(t, ok)
	rng.Comment = "range"
	hunk, ok := m.annotationFromRows(annotation.ScopeHunk, 1, 5)
	require.True(t, ok)
	hunk.Comment = "hunk"
	m.store.Add(line)
	m.store.Add(rng)
	m.store.Add(hunk)
	assert.Len(t, m.buildAnnotListItems(), 3)
	assert.Len(t, m.buildAnnotListSpec().Items, 3)

	spec := m.buildAnnotListSpec()
	var rangeTarget *overlay.AnnotationTarget
	for i := range spec.Items {
		if spec.Items[i].Scope == string(annotation.ScopeRange) {
			rangeTarget = &spec.Items[i].AnnotationTarget
		}
	}
	require.NotNil(t, rangeTarget)
	result, _, jumped := m.tryJumpToAnnotationTarget(rangeTarget)
	require.True(t, jumped)
	m = result.(Model)
	require.NotNil(t, m.annot.target)
	assert.Equal(t, rng.Excerpt, m.annot.target.Excerpt)
	m.startAnnotation()
	m.annot.input.SetValue("edited range")
	m.saveAnnotation()
	edited := m.store.Get(m.file.name)
	var editedRange annotation.Annotation
	for _, item := range edited {
		if item.Scope == annotation.ScopeRange {
			editedRange = item
		}
	}
	assert.Equal(t, rng.Excerpt, editedRange.Excerpt)
	m.positionOnAnnotation(editedRange)
	m.annot.cursorOnAnnotation = true
	m.deleteAnnotation()
	assert.Len(t, m.store.Get(m.file.name), 2)
	assert.Contains(t, m.store.FormatOutput(), "line")
	assert.Contains(t, m.store.FormatOutput(), "(hunk)")
	assert.NotContains(t, m.store.FormatOutput(), "(range)")
}

func TestModelAnnotationNavigationRecognizesScopedMarkerWithoutListTarget(t *testing.T) {
	m := newSelectionModel(t)
	rng, ok := m.annotationFromRows(annotation.ScopeRange, 3, 4)
	require.True(t, ok)
	rng.Comment = "range"
	next := annotation.Annotation{File: m.file.name, Line: 13, Type: "+", Comment: "next"}
	m.store.Add(rng)
	m.store.Add(next)
	m.nav.diffCursor = 3
	m.annot.cursorOnAnnotation = true
	m.annot.target = nil

	result, _ := m.handleAnnotNav(true)
	m = result.(Model)
	assert.Equal(t, 6, m.nav.diffCursor)
	require.NotNil(t, m.annot.target)
	assert.True(t, sameAnnotationTarget(next, *m.annot.target))
}

func TestModelAnnotationCursorIgnoresStaleScopedTarget(t *testing.T) {
	m := newSelectionModel(t)
	stale, ok := m.annotationFromRows(annotation.ScopeRange, 1, 2)
	require.True(t, ok)
	stale.Comment = "stale range"
	current := annotation.Annotation{File: m.file.name, Line: 13, Type: "+", Comment: "current line"}
	m.store.Add(stale)
	m.store.Add(current)
	m.annot.target = &stale
	m.nav.diffCursor = 6
	m.annot.cursorOnAnnotation = true

	m.startAnnotation()
	require.NotNil(t, m.annot.target)
	assert.True(t, sameAnnotationTarget(current, *m.annot.target))
	assert.Equal(t, "current line", m.annot.input.Value())
}

func TestModelScopedEditorCompletionPreservesTarget(t *testing.T) {
	m := newSelectionModel(t)
	target, ok := m.annotationFromRows(annotation.ScopeRange, 1, 4)
	require.True(t, ok)
	m.annot.annotating = true
	m.annot.target = &target
	result, _ := m.handleEditorFinished(editorFinishedMsg{content: "from editor", target: &target})
	m = result.(Model)
	assert.False(t, m.annot.annotating)
	assert.Contains(t, m.store.FormatOutput(), "@@ -10,2 +10,1 @@ (range)")
	assert.Contains(t, m.store.FormatOutput(), "\n\nfrom editor\n")
}

func TestModelFileRequestClearsSelectionBeforeAsyncLoad(t *testing.T) {
	m := newSelectionModel(t)
	m.beginSelection(1)
	require.True(t, m.annot.selection.active)

	cmd := m.requestFileDiff("other.go")
	assert.NotNil(t, cmd)
	assert.False(t, m.annot.selection.active)
	assert.Equal(t, "other.go", m.file.requestedPath)
	assert.NotContains(t, m.layout.viewport.View(), "\x1b[7m")
}

func TestModelFileLoadClearsSelection(t *testing.T) {
	m := newSelectionModel(t)
	m.beginSelection(1)
	m.file.loadSeq = 2
	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "other.go", seq: 2, lines: []diff.DiffLine{{NewNum: 1, Content: "x", ChangeType: diff.ChangeAdd}}})
	m = result.(Model)
	assert.False(t, m.annot.selection.active)
	assert.Nil(t, m.annot.target)
}

func TestModelHelpShowsRangeHunkAndReviewedDefaults(t *testing.T) {
	m := newSelectionModel(t)
	spec := m.buildHelpSpec()
	entries := map[string]string{}
	for _, section := range spec.Sections {
		for _, entry := range section.Entries {
			entries[entry.Description] = entry.Keys
		}
	}
	assert.Equal(t, "Space", entries["start range selection"])
	assert.Equal(t, "c", entries["annotate current diff hunk"])
	assert.Equal(t, "m", entries["mark file as reviewed"])
}

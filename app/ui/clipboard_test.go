package ui

import (
	"errors"
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/mocks"
)

func newClipboardModel(t *testing.T, lines []diff.DiffLine, clipboard Clipboard) Model {
	t.Helper()
	m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{Clipboard: clipboard})
	m.layout.focus = paneDiff
	m.file.name = "dir/file.go"
	m.file.lines = lines
	m.nav.diffCursor = 0
	return m
}

func mixedClipboardHunk() []diff.DiffLine {
	return []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "before", ChangeType: diff.ChangeContext},
		{OldNum: 2, Content: "old\tvalue", ChangeType: diff.ChangeRemove},
		{OldNum: 3, Content: "", ChangeType: diff.ChangeRemove},
		{NewNum: 2, Content: "new value", ChangeType: diff.ChangeAdd},
		{NewNum: 3, Content: "literal \x1b[31m bytes", ChangeType: diff.ChangeAdd},
		{OldNum: 4, NewNum: 4, Content: "after", ChangeType: diff.ChangeContext},
	}
}

func TestModelCopyHunkCursorPositionsProduceIdenticalOutput(t *testing.T) {
	const want = "dir/file.go\n-old\tvalue\n-\n+new value\n+literal \x1b[31m bytes\n"
	for _, cursor := range []int{1, 2, 4} {
		t.Run(strconv.Itoa(cursor), func(t *testing.T) {
			clipboard := &mocks.ClipboardMock{CopyFunc: func(text string) error {
				assert.Equal(t, want, text)
				return nil
			}}
			m := newClipboardModel(t, mixedClipboardHunk(), clipboard)
			m.nav.diffCursor = cursor

			result, cmd := m.handleCopyHunk()
			model := result.(Model)

			assert.Nil(t, cmd)
			assert.Equal(t, "Copied diff hunk", model.output.hint)
			require.Len(t, clipboard.CopyCalls(), 1)
		})
	}
}

func TestModelCopyHunkKeepsAdjacentHunksSeparate(t *testing.T) {
	lines := []diff.DiffLine{
		{Content: "first old", ChangeType: diff.ChangeRemove},
		{Content: "first new", ChangeType: diff.ChangeAdd},
		{Content: "separator", ChangeType: diff.ChangeContext},
		{Content: "second", ChangeType: diff.ChangeAdd},
	}
	clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return nil }}
	m := newClipboardModel(t, lines, clipboard)
	m.nav.diffCursor = 3

	result, _ := m.handleCopyHunk()
	model := result.(Model)

	require.Len(t, clipboard.CopyCalls(), 1)
	assert.Equal(t, "dir/file.go\n+second\n", clipboard.CopyCalls()[0].Content)
	assert.Equal(t, "Copied diff hunk", model.output.hint)
}

func TestModelCopyHunkInvalidContexts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Model)
	}{
		{name: "tree focus", mutate: func(m *Model) { m.layout.focus = paneTree; m.nav.diffCursor = 1 }},
		{name: "context", mutate: func(m *Model) { m.nav.diffCursor = 0 }},
		{name: "divider", mutate: func(m *Model) {
			m.file.lines = []diff.DiffLine{{Content: "gap", ChangeType: diff.ChangeDivider}}
		}},
		{name: "annotation sub-row", mutate: func(m *Model) { m.nav.diffCursor = 1; m.annot.cursorOnAnnotation = true }},
		{name: "empty file", mutate: func(m *Model) { m.file.lines = nil }},
		{name: "unloaded file", mutate: func(m *Model) { m.nav.diffCursor = 1; m.file.requestedPath = "next.go" }},
		{name: "no file", mutate: func(m *Model) { m.nav.diffCursor = 1; m.file.name = "" }},
		{name: "binary placeholder", mutate: func(m *Model) {
			m.file.lines = []diff.DiffLine{{Content: "binary", ChangeType: diff.ChangeAdd, IsBinary: true}}
		}},
		{name: "opaque placeholder", mutate: func(m *Model) {
			m.file.lines = []diff.DiffLine{{Content: "placeholder", ChangeType: diff.ChangeAdd, IsPlaceholder: true}}
		}},
		{name: "collapsed delete placeholder", mutate: func(m *Model) {
			m.file.lines = []diff.DiffLine{
				{Content: "old one", ChangeType: diff.ChangeRemove},
				{Content: "old two", ChangeType: diff.ChangeRemove},
			}
			m.modes.collapsed.enabled = true
			m.modes.collapsed.expandedHunks = map[int]bool{}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error {
				t.Fatal("clipboard must not be called")
				return nil
			}}
			m := newClipboardModel(t, mixedClipboardHunk(), clipboard)
			tc.mutate(&m)

			result, cmd := m.handleCopyHunk()
			model := result.(Model)

			assert.Nil(t, cmd)
			assert.Empty(t, clipboard.CopyCalls())
			assert.Equal(t, "Cursor is not on a diff hunk", model.output.hint)
		})
	}
}

func TestModelCopyHunkDisplayModesDoNotChangeOutput(t *testing.T) {
	const want = "dir/file.go\n-old\tvalue\n-\n+new value\n+literal \x1b[31m bytes\n"
	tests := []struct {
		name   string
		mutate func(*Model)
	}{
		{name: "wrapped", mutate: func(m *Model) { m.modes.wrap = true }},
		{name: "collapsed", mutate: func(m *Model) {
			m.modes.collapsed.enabled = true
			m.modes.collapsed.expandedHunks = map[int]bool{}
		}},
		{name: "compact", mutate: func(m *Model) { m.modes.compact = true }},
		{name: "word diff", mutate: func(m *Model) { m.modes.wordDiff = true }},
		{name: "line numbers", mutate: func(m *Model) { m.modes.lineNumbers = true }},
		{name: "syntax highlighted", mutate: func(m *Model) {
			m.file.highlighted = []string{"\x1b[31mdecorated\x1b[0m"}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return nil }}
			m := newClipboardModel(t, mixedClipboardHunk(), clipboard)
			m.nav.diffCursor = 3
			tc.mutate(&m)

			m.handleCopyHunk()

			require.Len(t, clipboard.CopyCalls(), 1)
			assert.Equal(t, want, clipboard.CopyCalls()[0].Content)
		})
	}
}

func TestModelCopyHunkOutcomesKeepReviewState(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		hint string
	}{
		{name: "success", hint: "Copied diff hunk"},
		{name: "failure", err: errors.New("terminal denied copy"), hint: "Copy failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return tc.err }}
			m := newClipboardModel(t, mixedClipboardHunk(), clipboard)
			m.nav.diffCursor = 3
			m.store.Add(annotation.Annotation{File: "dir/file.go", Line: 2, Type: "+", Comment: "note"})
			m.tree.Rebuild([]diff.FileEntry{{Path: "dir/file.go"}})
			m.tree.SetReviewed("dir/file.go", "fingerprint")

			result, cmd := m.handleCopyHunk()
			model := result.(Model)

			assert.Nil(t, cmd)
			assert.Equal(t, tc.hint, model.transientHint())
			assert.Equal(t, 3, model.nav.diffCursor)
			assert.Equal(t, "dir/file.go", model.file.name)
			assert.Equal(t, 1, model.store.Count())
			assert.True(t, model.tree.IsReviewed("dir/file.go"))
			require.Len(t, clipboard.CopyCalls(), 1)
		})
	}
}

func TestModelCopyHunkUnavailableClipboardReportsFailure(t *testing.T) {
	clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return nil }}
	m := newClipboardModel(t, mixedClipboardHunk(), clipboard)
	m.clipboard = nil
	m.nav.diffCursor = 1

	result, cmd := m.handleCopyHunk()

	assert.Nil(t, cmd)
	assert.Equal(t, "Copy failed", result.(Model).output.hint)
}

func TestNewModelTypedNilClipboardUsesDefault(t *testing.T) {
	var clipboard *mocks.ClipboardMock
	m := newClipboardModel(t, mixedClipboardHunk(), clipboard)
	assert.NotNil(t, m.clipboard)
}

func TestModelCopyHunkDispatchesDefaultAndCustomBindings(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  rune
		km   *keymap.Keymap
	}{
		{name: "default", key: 'Y', km: keymap.Default()},
		{name: "custom", key: 'x', km: func() *keymap.Keymap {
			km := keymap.Default()
			km.Unbind("Y")
			km.Bind("x", keymap.ActionCopyHunk)
			return km
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return nil }}
			m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{
				Clipboard: clipboard,
				Keymap:    tc.km,
			})
			m.layout.focus = paneDiff
			m.file.name = "a.go"
			m.file.lines = []diff.DiffLine{{Content: "line", ChangeType: diff.ChangeAdd}}

			result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})

			assert.Nil(t, cmd)
			assert.Equal(t, "Copied diff hunk", result.(Model).output.hint)
			require.Len(t, clipboard.CopyCalls(), 1)
			assert.Equal(t, "a.go\n+line\n", clipboard.CopyCalls()[0].Content)
		})
	}
}

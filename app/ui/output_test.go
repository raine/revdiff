package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/ui/mocks"
)

type postFlushHookStub struct {
	content string
}

func (s *postFlushHookStub) Prepare(content string) *exec.Cmd {
	s.content = content
	return exec.Command("sh", "-c", "exit 0")
}

func TestModel_HandleCopyAnnotations(t *testing.T) {
	annotations := []annotation.Annotation{
		{File: "b.go", Line: 8, Type: "-", Comment: "second file"},
		{File: "a.go", Line: 3, Type: "+", Comment: "first file"},
		{File: "a.go", Line: 7, Type: "+", Comment: "another note"},
	}

	t.Run("copies canonical complete snapshot without mutation", func(t *testing.T) {
		store := annotation.NewStore()
		for _, a := range annotations {
			store.Add(a)
		}
		before := store.All()
		clipboard := &mocks.ClipboardMock{CopyFunc: func(content string) error {
			assert.Equal(t, store.FormatOutput(), content)
			return nil
		}}
		m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{Clipboard: clipboard})

		model := m.handleCopyAnnotations().(Model)
		assert.Equal(t, "Copied 3 annotations", model.output.hint)
		assert.Len(t, clipboard.CopyCalls(), 1)
		assert.Equal(t, before, store.All(), "copy must leave every annotation unchanged")
	})

	t.Run("empty annotations do not call clipboard", func(t *testing.T) {
		clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error {
			t.Fatal("clipboard must not be called for an empty store")
			return nil
		}}
		m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{Clipboard: clipboard})

		model := m.handleCopyAnnotations().(Model)
		assert.Equal(t, "No annotations to copy", model.output.hint)
		assert.Empty(t, clipboard.CopyCalls())
	})

	t.Run("clipboard failure leaves review and store intact", func(t *testing.T) {
		store := annotation.NewStore()
		store.Add(annotations[0])
		clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return errors.New("terminal rejected OSC 52") }}
		m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{Clipboard: clipboard})

		model := m.handleCopyAnnotations().(Model)
		assert.Equal(t, "Copy failed", model.output.hint)
		assert.Equal(t, []annotation.Annotation{annotations[0]}, store.Get("b.go"))
	})

	t.Run("single annotation uses singular feedback", func(t *testing.T) {
		store := annotation.NewStore()
		store.Add(annotations[0])
		clipboard := &mocks.ClipboardMock{CopyFunc: func(string) error { return nil }}
		m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{Clipboard: clipboard})

		result := m.handleCopyAnnotations()
		assert.Equal(t, "Copied 1 annotation", result.(Model).output.hint)
	})
}

func TestNewModel_TypedNilClipboardUsesDefault(t *testing.T) {
	var clipboard *mocks.ClipboardMock
	m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{Clipboard: clipboard})
	assert.NotNil(t, m.clipboard)
}

func TestNewModel_OutputPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "present", path: "/tmp/review.md"},
		{name: "empty", path: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := testNewModel(t, &mocks.RendererMock{}, annotation.NewStore(), noopHighlighter(), ModelConfig{OutputPath: tc.path})
			assert.Equal(t, tc.path, m.cfg.outputPath)
			assert.Empty(t, m.output.hint)
		})
	}
}

func TestModel_HandleFlushOutput_EmptyPath(t *testing.T) {
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: ""})

	result, cmd := m.handleFlushOutput()
	model := result.(Model)
	assert.Equal(t, "Output flush requires -o/--output or --post-flush-command", model.output.hint)
	assert.Nil(t, cmd)
}

func TestModel_HandleFlushOutput_EmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.md")
	m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{OutputPath: path})

	result, cmd := m.handleFlushOutput()
	model := result.(Model)
	assert.Equal(t, "No annotations to flush", model.output.hint)
	assert.Nil(t, cmd)
	assert.NoFileExists(t, path, "empty store must not create the output file")
}

func TestModel_HandleFlushOutput_Success(t *testing.T) {
	tests := []struct {
		name     string
		anns     []annotation.Annotation
		wantHint string
	}{
		{
			name:     "single",
			anns:     []annotation.Annotation{{File: "a.go", Line: 1, Type: "+", Comment: "note"}},
			wantHint: "Wrote 1 annotation to output file",
		},
		{
			name: "multiple",
			anns: []annotation.Annotation{
				{File: "a.go", Line: 1, Type: "+", Comment: "note"},
				{File: "b.go", Line: 5, Type: " ", Comment: "check"},
			},
			wantHint: "Wrote 2 annotations to output file",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := annotation.NewStore()
			for _, a := range tc.anns {
				store.Add(a)
			}
			path := filepath.Join(t.TempDir(), "out.md")
			m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: path})

			result, cmd := m.handleFlushOutput()
			model := result.(Model)
			assert.Equal(t, tc.wantHint, model.output.hint)
			assert.Nil(t, cmd)

			got, err := os.ReadFile(path) //nolint:gosec // path is a t.TempDir() file
			require.NoError(t, err)
			assert.Equal(t, store.FormatOutput(), string(got), "written file must match FormatOutput")
			assert.Equal(t, len(tc.anns), store.Count(), "flush must not mutate the store")
		})
	}
}

func TestModel_HandleFlushOutput_WriteError(t *testing.T) {
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	path := filepath.Join(t.TempDir(), "missing-dir", "out.md")
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: path})

	result, cmd := m.handleFlushOutput()
	model := result.(Model)
	assert.Equal(t, "Flush failed", model.output.hint)
	assert.Nil(t, cmd)
	assert.NoFileExists(t, path)
}

func TestModel_HandleFlushOutput_PostFlushHook(t *testing.T) {
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	hook := &postFlushHookStub{}

	t.Run("without output file", func(t *testing.T) {
		m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{PostFlushHook: hook})

		result, cmd := m.handleFlushOutput()
		model := result.(Model)
		require.NotNil(t, cmd)
		assert.Equal(t, store.FormatOutput(), hook.content)
		assert.Equal(t, "Running post-flush command with 1 annotation", model.output.hint)
	})

	t.Run("with output file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "out.md")
		m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{
			OutputPath:    path,
			PostFlushHook: hook,
			MouseTracking: true,
		})

		result, cmd := m.handleFlushOutput()
		model := result.(Model)
		require.NotNil(t, cmd)
		assert.Equal(t, store.FormatOutput(), hook.content)
		assert.Equal(t, "Wrote 1 annotation to output file; running post-flush command", model.output.hint)
		assert.FileExists(t, path)
	})
}

func TestModel_HandlePostFlushFinished(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		result, cmd := m.handlePostFlushFinished(postFlushFinishedMsg{
			successHint:  "Wrote 2 annotations to output file and ran post-flush command",
			failureHint:  "Wrote 2 annotations to output file; post-flush command failed",
			restoreMouse: true,
		})
		model := result.(Model)
		assert.Equal(t, "Wrote 2 annotations to output file and ran post-flush command", model.output.hint)
		assert.NotNil(t, cmd)
	})

	t.Run("failure", func(t *testing.T) {
		m := testModel([]string{"a.go"}, nil)
		result, cmd := m.handlePostFlushFinished(postFlushFinishedMsg{
			err:         errors.New("exit status 1"),
			successHint: "Wrote 1 annotation to output file and ran post-flush command",
			failureHint: "Wrote 1 annotation to output file; post-flush command failed",
		})
		model := result.(Model)
		assert.Equal(t, "Wrote 1 annotation to output file; post-flush command failed", model.output.hint)
		assert.Nil(t, cmd)
	})
}

func TestNewModel_TypedNilPostFlushHook(t *testing.T) {
	var hook *postFlushHookStub
	m := testNewModel(t, plainRenderer(), annotation.NewStore(), noopHighlighter(), ModelConfig{PostFlushHook: hook})
	assert.Nil(t, m.postFlushHook)
}

func TestModel_ActionCopyAnnotations_Dispatch(t *testing.T) {
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	clipboard := &mocks.ClipboardMock{CopyFunc: func(content string) error {
		assert.Equal(t, store.FormatOutput(), content)
		return nil
	}}
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{Clipboard: clipboard})

	result, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model := result.(Model)
	assert.Nil(t, cmd)
	assert.Equal(t, "Copied 1 annotation", model.output.hint)
	assert.Len(t, clipboard.CopyCalls(), 1)
}

func TestModel_ActionFlushOutput_Dispatch(t *testing.T) {
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 1, Type: "+", Comment: "note"})
	path := filepath.Join(t.TempDir(), "out.md")
	m := testNewModel(t, plainRenderer(), store, noopHighlighter(), ModelConfig{OutputPath: path})

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	model := result.(Model)
	assert.Equal(t, "Wrote 1 annotation to output file", model.output.hint)
	assert.FileExists(t, path, "O key must flush annotations to the output file")
}

func TestModel_OutputHint_ShownInStatusBar(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.output.hint = "test output hint"
	assert.Equal(t, "test output hint", m.transientHint())
}

func TestModel_OutputHint_ClearsOnNextKey(t *testing.T) {
	m := testModel([]string{"a.go"}, nil)
	m.output.hint = "some hint"

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model := result.(Model)
	assert.Empty(t, model.output.hint, "any key press must clear the output hint")
}

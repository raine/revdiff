package ui

import (
	"log"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/diff"
)

// handleCopyHunk copies the complete underlying add/remove group at the
// diff cursor. Rendering modes and viewport decoration do not participate in
// extraction, so the copied text stays stable across view changes.
func (m Model) handleCopyHunk() (tea.Model, tea.Cmd) {
	content, ok := m.currentHunkClipboardText()
	if !ok {
		m.output.hint = "Cursor is not on a diff hunk"
		return m, nil
	}
	if m.clipboard == nil {
		m.output.hint = "Copy failed"
		return m, nil
	}
	if err := m.clipboard.Copy(content); err != nil {
		log.Printf("[WARN] copy diff hunk: %v", err)
		m.output.hint = "Copy failed"
		return m, nil
	}
	m.output.hint = "Copied diff hunk"
	return m, nil
}

func (m Model) currentHunkClipboardText() (string, bool) {
	if m.layout.focus != paneDiff || m.annot.cursorOnAnnotation || m.file.name == "" ||
		m.file.requestedPath != "" || len(m.file.lines) == 0 {
		return "", false
	}
	r, ok := m.hunkRangeAt(m.nav.diffCursor)
	if !ok {
		return "", false
	}
	hunks := m.findHunks()
	if m.isDeleteOnlyPlaceholder(m.nav.diffCursor, hunks) {
		return "", false
	}

	var b strings.Builder
	b.WriteString(m.file.name)
	b.WriteByte('\n')
	for i := r.start; i < r.end; i++ {
		dl := m.file.lines[i]
		if dl.IsBinary || dl.IsPlaceholder {
			return "", false
		}
		switch dl.ChangeType {
		case diff.ChangeAdd:
			b.WriteByte('+')
		case diff.ChangeRemove:
			b.WriteByte('-')
		default:
			return "", false
		}
		b.WriteString(dl.Content)
		b.WriteByte('\n')
	}
	return b.String(), true
}

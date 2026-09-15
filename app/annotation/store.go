package annotation

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/umputun/revdiff/app/fsutil"
)

// Scope identifies the source span represented by an annotation.
type Scope string

const (
	ScopeLine  Scope = ""
	ScopeRange Scope = "range"
	ScopeHunk  Scope = "hunk"
)

// ExcerptLine is one clean source row in a range or hunk annotation.
type ExcerptLine struct {
	Type    string // "+" or "-"
	Content string
}

// Annotation represents a user comment on a file or source span.
type Annotation struct {
	File    string // file path relative to repo root
	Line    int    // identity anchor line; scoped display ownership is resolved from coordinates
	EndLine int    // legacy same-side range end, 0 means no legacy range
	Type    string // anchor change type: "+", "-", or " "
	Comment string // user comment text

	Scope    Scope
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Excerpt  []ExcerptLine
}

// Store holds annotations in memory, keyed by filename.
type Store struct {
	annotations map[string][]Annotation
}

// NewStore creates an empty annotation store.
func NewStore() *Store { return &Store{annotations: make(map[string][]Annotation)} }

// Add inserts an annotation or replaces one with the same scope identity.
func (s *Store) Add(a Annotation) {
	a = cloneAnnotation(a)
	existing := s.annotations[a.File]
	if i, ok := s.findExact(a); ok {
		existing[i] = a
		return
	}
	s.annotations[a.File] = append(existing, a)
}

// Delete removes the first annotation anchored at file, line, and change type.
// DeleteExact should be used when the caller has a complete annotation identity.
func (s *Store) Delete(file string, line int, changeType string) bool {
	for _, a := range s.annotations[file] {
		if a.Line == line && a.Type == changeType {
			return s.DeleteExact(a)
		}
	}
	return false
}

// DeleteExact removes the annotation with the same scope identity as a.
func (s *Store) DeleteExact(a Annotation) bool {
	i, ok := s.findExact(a)
	if !ok {
		return false
	}
	existing := s.annotations[a.File]
	s.annotations[a.File] = append(existing[:i], existing[i+1:]...)
	if len(s.annotations[a.File]) == 0 {
		delete(s.annotations, a.File)
	}
	return true
}

// Has reports whether any annotation is anchored at file, line, and change type.
func (s *Store) Has(file string, line int, changeType string) bool {
	for _, a := range s.annotations[file] {
		if a.Line == line && a.Type == changeType {
			return true
		}
	}
	return false
}

func (s *Store) findExact(a Annotation) (int, bool) {
	for i, existing := range s.annotations[a.File] {
		if sameIdentity(existing, a) {
			return i, true
		}
	}
	return 0, false
}

func sameIdentity(a, b Annotation) bool {
	if a.File != b.File || a.Scope != b.Scope {
		return false
	}
	if a.Scope == ScopeLine {
		return a.Line == b.Line && a.Type == b.Type
	}
	return a.OldStart == b.OldStart && a.OldCount == b.OldCount &&
		a.NewStart == b.NewStart && a.NewCount == b.NewCount
}

// Get returns annotations for file in stable source and scope order.
func (s *Store) Get(file string) []Annotation {
	stored := s.annotations[file]
	result := make([]Annotation, len(stored))
	for i, a := range stored {
		result[i] = cloneAnnotation(a)
	}
	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if scopeOrder(a.Scope) != scopeOrder(b.Scope) {
			return scopeOrder(a.Scope) < scopeOrder(b.Scope)
		}
		if a.OldStart != b.OldStart {
			return a.OldStart < b.OldStart
		}
		if a.OldCount != b.OldCount {
			return a.OldCount < b.OldCount
		}
		if a.NewStart != b.NewStart {
			return a.NewStart < b.NewStart
		}
		return a.NewCount < b.NewCount
	})
	return result
}

func cloneAnnotation(a Annotation) Annotation {
	a.Excerpt = append([]ExcerptLine(nil), a.Excerpt...)
	return a
}

func scopeOrder(scope Scope) int {
	switch scope {
	case ScopeLine:
		return 0
	case ScopeRange:
		return 1
	case ScopeHunk:
		return 2
	default:
		return 3
	}
}

// Count returns the number of stored annotations.
func (s *Store) Count() int {
	count := 0
	for _, anns := range s.annotations {
		count += len(anns)
	}
	return count
}

// Clear removes every annotation.
func (s *Store) Clear() { s.annotations = make(map[string][]Annotation) }

// RemoveMatching removes annotations that are unchanged from a prior snapshot.
// An annotation edited or replaced after the snapshot was taken is preserved.
func (s *Store) RemoveMatching(snapshot map[string][]Annotation) {
	for file, flushed := range snapshot {
		for _, a := range flushed {
			i, ok := s.findExact(a)
			if !ok || !sameAnnotation(s.annotations[file][i], a) {
				continue
			}
			existing := s.annotations[file]
			s.annotations[file] = append(existing[:i], existing[i+1:]...)
			if len(s.annotations[file]) == 0 {
				delete(s.annotations, file)
			}
		}
	}
}

func sameAnnotation(a, b Annotation) bool {
	if a.File != b.File || a.Line != b.Line || a.EndLine != b.EndLine || a.Type != b.Type ||
		a.Comment != b.Comment || a.Scope != b.Scope || a.OldStart != b.OldStart ||
		a.OldCount != b.OldCount || a.NewStart != b.NewStart || a.NewCount != b.NewCount ||
		len(a.Excerpt) != len(b.Excerpt) {
		return false
	}
	for i := range a.Excerpt {
		if a.Excerpt[i] != b.Excerpt[i] {
			return false
		}
	}
	return true
}

// All returns a sorted copy of the annotations grouped by file.
func (s *Store) All() map[string][]Annotation {
	result := make(map[string][]Annotation, len(s.annotations))
	for file := range s.annotations {
		result[file] = s.Get(file)
	}
	return result
}

// Files returns annotated file paths in alphabetical order.
func (s *Store) Files() []string {
	files := make([]string, 0, len(s.annotations))
	for file := range s.annotations {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

// Load parses and adds annotations from canonical structured output.
func (s *Store) Load(r io.Reader) error {
	records, err := Parse(r)
	if err != nil {
		return err
	}
	for _, a := range records {
		s.Add(a)
	}
	return nil
}

// FormatOutput produces the canonical structured annotation format.
func (s *Store) FormatOutput() string {
	if len(s.annotations) == 0 {
		return ""
	}
	var buf strings.Builder
	first := true
	for _, file := range s.Files() {
		for _, a := range s.Get(file) {
			if !first {
				buf.WriteByte('\n')
			}
			first = false
			body := s.escapeHeaderLines(a.Comment)
			switch {
			case a.Line == 0:
				fmt.Fprintf(&buf, "## %s (file-level)\n%s\n", a.File, body)
			case a.Scope == ScopeRange || a.Scope == ScopeHunk:
				fmt.Fprintf(&buf, "## %s @@ -%d,%d +%d,%d @@ (%s)\n", a.File,
					a.OldStart, a.OldCount, a.NewStart, a.NewCount, a.Scope)
				for _, line := range a.Excerpt {
					buf.WriteString(line.Type)
					buf.WriteString(line.Content)
					buf.WriteByte('\n')
				}
				buf.WriteByte('\n')
				buf.WriteString(body)
				buf.WriteByte('\n')
			case a.EndLine > 0:
				fmt.Fprintf(&buf, "## %s:%d-%d (%s)\n%s\n", a.File, a.Line, a.EndLine, a.Type, body)
			default:
				fmt.Fprintf(&buf, "## %s:%d (%s)\n%s\n", a.File, a.Line, a.Type, body)
			}
		}
	}
	return buf.String()
}

// WriteFile atomically writes FormatOutput and returns the written snapshot.
func (s *Store) WriteFile(path string) (string, error) {
	content := s.FormatOutput()
	if err := fsutil.AtomicWriteFile(path, []byte(content)); err != nil {
		return "", fmt.Errorf("write annotations to %s: %w", path, err)
	}
	return content, nil
}

func (s *Store) escapeHeaderLines(body string) string {
	if !strings.Contains(body, "## ") {
		return body
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "## ") {
			lines[i] = " " + line
		}
	}
	return strings.Join(lines, "\n")
}

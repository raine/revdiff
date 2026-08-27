package annotation

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var (
	legacyHeaderRe = regexp.MustCompile(`^## (.+?)(?::(\d+)(?:-(\d+))?)? \((file-level|\+|-| )\)$`)
	scopeHeaderRe  = regexp.MustCompile(`^## (.+) @@ -(\d+),(\d+) \+(\d+),(\d+) @@ \((range|hunk)\)$`)
)

// Parse reads markdown produced by Store.FormatOutput.
func Parse(r io.Reader) ([]Annotation, error) {
	p := parser{scanner: bufio.NewScanner(r)}
	p.scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	return p.parse()
}

type parser struct {
	scanner      *bufio.Scanner
	out          []Annotation
	current      *Annotation
	body         []string
	seenHeader   bool
	nonBlankSeen bool
}

func (p *parser) parse() ([]Annotation, error) {
	for p.scanner.Scan() {
		line := p.scanner.Text()
		if strings.HasPrefix(line, "## ") {
			ann, err := p.parseHeader(line)
			if err != nil {
				if !p.seenHeader {
					return nil, err
				}
				p.appendBody(line)
				continue
			}
			if err := p.flush(); err != nil {
				return nil, err
			}
			p.seenHeader = true
			p.current = &ann
			continue
		}
		if !p.seenHeader {
			if strings.TrimSpace(line) == "" {
				continue
			}
			p.nonBlankSeen = true
			break
		}
		p.appendBody(line)
	}
	if err := p.scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan annotations: %w", err)
	}
	if !p.seenHeader && p.nonBlankSeen {
		return nil, errors.New("annotation input has content before any header")
	}
	if err := p.flush(); err != nil {
		return nil, err
	}
	return p.out, nil
}

func (p *parser) appendBody(line string) {
	if strings.HasPrefix(line, " ") && strings.HasPrefix(strings.TrimLeft(line, " "), "## ") {
		line = line[1:]
	}
	p.body = append(p.body, line)
}

func (p *parser) flush() error {
	if p.current == nil {
		return nil
	}
	if n := len(p.body); n > 0 && p.body[n-1] == "" {
		p.body = p.body[:n-1]
	}
	if p.current.Scope == ScopeRange || p.current.Scope == ScopeHunk {
		excerptCount := p.current.OldCount + p.current.NewCount
		if len(p.body) < excerptCount+1 || p.body[excerptCount] != "" {
			return fmt.Errorf("malformed %s annotation for %q: expected %d excerpt lines and a blank separator",
				p.current.Scope, p.current.File, excerptCount)
		}
		oldSeen, newSeen := 0, 0
		for _, line := range p.body[:excerptCount] {
			if line == "" || (line[0] != '+' && line[0] != '-') {
				return fmt.Errorf("malformed %s annotation excerpt for %q", p.current.Scope, p.current.File)
			}
			kind := line[:1]
			if kind == "+" {
				newSeen++
			} else {
				oldSeen++
			}
			p.current.Excerpt = append(p.current.Excerpt, ExcerptLine{Type: kind, Content: line[1:]})
		}
		if oldSeen != p.current.OldCount || newSeen != p.current.NewCount {
			return fmt.Errorf("malformed %s annotation counts for %q", p.current.Scope, p.current.File)
		}
		p.body = p.body[excerptCount+1:]
		if len(p.current.Excerpt) > 0 {
			p.current.Type = p.current.Excerpt[0].Type
			if p.current.Type == "+" {
				p.current.Line = p.current.NewStart
			} else {
				p.current.Line = p.current.OldStart
			}
		}
	}
	p.current.Comment = strings.Join(p.body, "\n")
	p.out = append(p.out, *p.current)
	p.current = nil
	p.body = nil
	return nil
}

func (p *parser) parseHeader(line string) (Annotation, error) {
	if m := scopeHeaderRe.FindStringSubmatch(line); m != nil {
		values := make([]int, 4)
		for i := range values {
			n, err := strconv.Atoi(m[i+2])
			if err != nil {
				return Annotation{}, fmt.Errorf("malformed annotation range: %q", line)
			}
			values[i] = n
		}
		return Annotation{File: m[1], OldStart: values[0], OldCount: values[1], NewStart: values[2], NewCount: values[3], Scope: Scope(m[6])}, nil
	}
	m := legacyHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return Annotation{}, fmt.Errorf("malformed annotation header: %q", line)
	}
	ann := Annotation{File: m[1]}
	if m[4] == "file-level" {
		if m[2] != "" {
			ann.File += ":" + m[2]
			if m[3] != "" {
				ann.File += "-" + m[3]
			}
		}
		return ann, nil
	}
	ann.Type = m[4]
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return Annotation{}, fmt.Errorf("malformed annotation header line number: %q", line)
	}
	ann.Line = n
	if m[3] != "" {
		end, err := strconv.Atoi(m[3])
		if err != nil {
			return Annotation{}, fmt.Errorf("malformed annotation header end line: %q", line)
		}
		ann.EndLine = end
	}
	return ann, nil
}

// Package clipboard copies text through terminal clipboard control sequences.
package clipboard

import (
	"fmt"
	"io"
	"os"
	"strings"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
)

// MaxContentBytes is the largest clipboard payload accepted by Copier.
// The bound keeps the encoded OSC 52 sequence within the limits commonly
// enforced by terminal emulators and multiplexers.
const MaxContentBytes = 100_000

// Copier sends clipboard content to the terminal that hosts revdiff.
type Copier struct {
	out     io.Writer
	term    string
	openTTY func() (io.WriteCloser, error)
}

// New constructs a terminal clipboard copier. OSC 52 travels through local
// terminals and SSH connections because it is written to the controlling TTY.
func New() Copier {
	return Copier{term: os.Getenv("TERM")}
}

// Copy writes content as one OSC 52 system-clipboard operation.
func (c Copier) Copy(content string) error {
	if len(content) > MaxContentBytes {
		return fmt.Errorf("clipboard content exceeds %d-byte limit", MaxContentBytes)
	}

	seq := osc52.New(content)
	if strings.HasPrefix(c.term, "screen") && os.Getenv("TMUX") == "" {
		seq = seq.Screen()
	}
	out := c.out
	if out == nil {
		tty, err := c.terminal()
		if err != nil {
			return fmt.Errorf("open controlling terminal for clipboard: %w", err)
		}
		defer tty.Close()
		out = tty
	}

	encoded := seq.String()
	n, err := io.WriteString(out, encoded)
	if err != nil {
		return fmt.Errorf("write OSC 52 clipboard sequence: %w", err)
	}
	if n != len(encoded) {
		return fmt.Errorf("write OSC 52 clipboard sequence: %w", io.ErrShortWrite)
	}
	return nil
}

func (c Copier) terminal() (io.WriteCloser, error) {
	if c.openTTY != nil {
		return c.openTTY()
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/tty: %w", err)
	}
	return tty, nil
}

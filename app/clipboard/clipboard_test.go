package clipboard

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("tty unavailable") }

func TestCopierCopy(t *testing.T) {
	t.Setenv("TMUX", "")
	var out strings.Builder
	c := Copier{out: &out, term: "xterm-256color"}

	require.NoError(t, c.Copy("hello"))
	assert.Equal(t, "\x1b]52;c;aGVsbG8=\x07", out.String())
}

func TestCopierCopyScreenPassthrough(t *testing.T) {
	t.Setenv("TMUX", "")
	var out strings.Builder
	c := Copier{out: &out, term: "screen-256color"}

	require.NoError(t, c.Copy("hello"))
	assert.Equal(t, "\x1bP\x1b]52;c;aGVsbG8=\x07\x1b\\", out.String())
}

func TestCopierCopyTmuxUsesTmuxClipboardHandling(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	var out strings.Builder
	c := Copier{out: &out, term: "screen-256color"}

	require.NoError(t, c.Copy("hello"))
	assert.Equal(t, "\x1b]52;c;aGVsbG8=\x07", out.String())
}

func TestCopierCopyRejectsOversizedContent(t *testing.T) {
	var out strings.Builder
	c := Copier{out: &out, term: "xterm-256color"}

	err := c.Copy(strings.Repeat("x", MaxContentBytes+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "100000-byte limit")
	assert.Empty(t, out.String())
}

func TestCopierCopyReportsTTYOpenFailure(t *testing.T) {
	c := Copier{
		term: "xterm-256color",
		openTTY: func() (io.WriteCloser, error) {
			return nil, errors.New("no controlling tty")
		},
	}

	err := c.Copy("hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open controlling terminal for clipboard")
}

func TestCopierCopyReportsWriteFailure(t *testing.T) {
	c := Copier{out: failingWriter{}, term: "xterm-256color"}

	err := c.Copy("hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write OSC 52 clipboard sequence")
}

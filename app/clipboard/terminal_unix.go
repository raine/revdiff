//go:build !windows

package clipboard

import (
	"fmt"
	"os"
)

func openTerminal() (*os.File, error) {
	terminal, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/tty: %w", err)
	}
	return terminal, nil
}

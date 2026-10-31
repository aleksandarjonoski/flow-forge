//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package store

import (
	"fmt"
	"os"
)

// lockDir only creates the lock file on platforms without a supported lock
// call: the data directory is not protected against a second server there.
func lockDir(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	return f, nil
}

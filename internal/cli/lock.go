package cli

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func acquireLock() (*os.File, error) {
	const path = "/tmp/keenetic-geosite-sync.lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another sync or cleanup is already running")
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return file, nil
}

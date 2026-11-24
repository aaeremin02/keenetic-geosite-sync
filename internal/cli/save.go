package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"keenetic-geosite-sync/internal/keenetic"
)

const (
	saveCommand  = "system configuration save"
	saveAttempts = 3
	saveDelay    = 250 * time.Millisecond
)

type configurationSaver struct {
	lock *os.File
	run  func(context.Context, string) error
}

func (s configurationSaver) save(ctx context.Context, verified keenetic.Fingerprint) error {
	// Record only the fingerprint of the verified router configuration.
	_, recordErr := s.lock.WriteAt(verified[:], 0)
	saveErr := s.retry(ctx)
	if saveErr != nil && recordErr != nil {
		return errors.Join(saveErr, fmt.Errorf("record pending configuration save: %w", recordErr))
	}
	return saveErr
}

func (s configurationSaver) resume(ctx context.Context, current keenetic.Fingerprint) (string, error) {
	var pending keenetic.Fingerprint
	n, err := s.lock.ReadAt(pending[:], 0)
	if n == 0 && errors.Is(err, io.EOF) {
		return "", nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read pending configuration save: %w", err)
	}
	if n != len(pending) || pending != current {
		if err := s.clear(); err != nil {
			return "", err
		}
		return "Pending save discarded because the running configuration no longer matches. Review the current configuration before saving.", nil
	}
	if err := s.retry(ctx); err != nil {
		return "", fmt.Errorf("pending configuration save: %w", err)
	}
	return "Pending configuration saved", nil
}

func (s configurationSaver) retry(ctx context.Context) error {
	for attempt := 1; ; attempt++ {
		err := s.run(ctx, saveCommand)
		if err == nil {
			return s.clear()
		}
		if attempt == saveAttempts {
			return fmt.Errorf("save failed after %d attempts: %w", attempt, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("save interrupted: %w", ctx.Err())
		case <-time.After(saveDelay):
		}
	}
}

func (s configurationSaver) clear() error {
	if err := s.lock.Truncate(0); err != nil {
		return fmt.Errorf("clear pending configuration save: %w", err)
	}
	return nil
}

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const backupFileSuffix = ".nodedrbackup"

// Verify decrypts fileBytes and checks it against its recorded checksum
// without touching the database. A scheduled backup is only reported as
// successful after this passes, so a file that cannot be restored is never
// counted as a backup.
func (s *Service) Verify(fileBytes []byte) (*Header, error) {
	_, headerJSON, sealed, err := parseFile(fileBytes)
	if err != nil {
		return nil, err
	}
	var header Header
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidFile, err.Error())
	}
	if header.Version != backupFormatVersion {
		return nil, fmt.Errorf("%w: unsupported backup format version %d", ErrInvalidFile, header.Version)
	}
	dump, err := s.aead.Open(sealed, headerJSON)
	if err != nil {
		return nil, fmt.Errorf("backup: verification could not decrypt the file: %w", err)
	}
	sum := sha256.Sum256(dump)
	if hex.EncodeToString(sum[:]) != header.SHA256 {
		return nil, ErrChecksumMismatch
	}
	return &header, nil
}

// RunScheduled creates one backup in dir, verifies it, writes it atomically
// with owner-only permissions, then prunes to the newest keep files. It is
// invoked by the worker, not an HTTP caller, so it carries no principal.
func (s *Service) RunScheduled(ctx context.Context, dir string, keep int) (string, error) {
	if !s.Enabled() {
		return "", ErrNotConfigured
	}
	if keep < 1 {
		return "", fmt.Errorf("backup: retention must keep at least 1 file, got %d", keep)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup: creating %s: %w", dir, err)
	}
	file, header, err := s.buildBackup(ctx)
	if err != nil {
		return "", err
	}
	if _, err := s.Verify(file); err != nil {
		return "", fmt.Errorf("backup: refusing to keep an unverifiable backup: %w", err)
	}
	final := filepath.Join(dir, backupFilename(header.CreatedAt))
	tmp := final + ".partial"
	if err := os.WriteFile(tmp, file, 0o600); err != nil {
		return "", fmt.Errorf("backup: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("backup: finalising %s: %w", final, err)
	}
	if err := pruneBackups(dir, keep); err != nil {
		slog.Warn("backup retention prune failed", "dir", dir, "error", err)
	}
	return final, nil
}

// pruneBackups deletes all but the newest keep backup files in dir. File
// names embed a UTC timestamp, so lexical order is chronological. Only
// files this package names are touched.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "rechvix-backup-") && strings.HasSuffix(n, backupFileSuffix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for len(names) > keep {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// RunScheduleLoop runs RunScheduled every interval until ctx is cancelled,
// logging each outcome. The first run happens after one interval, not at
// startup, so a crash-looping worker cannot hammer pg_dump.
func (s *Service) RunScheduleLoop(ctx context.Context, dir string, keep int, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := time.Now()
			path, err := s.RunScheduled(ctx, dir, keep)
			if err != nil {
				log.Error("scheduled backup failed", "error", err)
				continue
			}
			log.Info("scheduled backup ok", "file", path, "took", time.Since(start).String())
		}
	}
}

package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	appcrypto "rechvix/internal/platform/crypto"
)

func newTestService(t *testing.T, keyByte byte) *Service {
	t.Helper()
	aead, err := appcrypto.NewAEAD(bytes.Repeat([]byte{keyByte}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return &Service{aead: aead, now: time.Now}
}

func TestSealVerifyRoundTrip(t *testing.T) {
	s := newTestService(t, 1)
	file, header, err := s.seal([]byte("pretend pg_dump bytes"), "18.0", time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(file)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.SHA256 != header.SHA256 || got.ArchiveBytes != len("pretend pg_dump bytes") {
		t.Fatalf("header mismatch: %+v vs %+v", got, header)
	}
}

func TestVerifyRejectsTamperedAndWrongKey(t *testing.T) {
	s := newTestService(t, 1)
	file, _, err := s.seal([]byte("data"), "18.0", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), file...)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := s.Verify(tampered); err == nil {
		t.Fatal("tampered ciphertext must fail verification")
	}
	if _, err := newTestService(t, 2).Verify(file); err == nil {
		t.Fatal("wrong key must fail verification")
	}
	if _, err := s.Verify([]byte("not a backup")); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("garbage input: want ErrInvalidFile, got %v", err)
	}
}

func TestPruneKeepsNewestOnly(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"rechvix-backup-2026-10-18-030000.nodedrbackup",
		"rechvix-backup-2026-10-19-030000.nodedrbackup",
		"rechvix-backup-2026-10-20-030000.nodedrbackup",
		"rechvix-backup-2026-10-21-030000.nodedrbackup",
		"unrelated.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneBackups(dir, 2); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[string]bool{names[0]: false, names[1]: false, names[2]: true, names[3]: true, "unrelated.txt": true} {
		_, err := os.Stat(filepath.Join(dir, n))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", n, err == nil, want)
		}
	}
}

func TestRunScheduledRequiresConfigAndRetention(t *testing.T) {
	s := newTestService(t, 1)
	if _, err := s.RunScheduled(t.Context(), t.TempDir(), 3); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	s.backupDSN = "postgres://x"
	if _, err := s.RunScheduled(t.Context(), t.TempDir(), 0); err == nil {
		t.Fatal("keep=0 must be rejected")
	}
}

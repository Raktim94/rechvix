//go:build integration

package integration

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	backupapp "rechvix/internal/modules/backup/app"
	catalogueapp "rechvix/internal/modules/catalogue/app"
	"rechvix/internal/platform/audit"
	appcrypto "rechvix/internal/platform/crypto"
	"rechvix/internal/platform/permissions"
)

func newTestBackupService(t *testing.T) *backupapp.Service {
	t.Helper()
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump not on PATH — skipping (CI installs postgresql-client explicitly; see .github/workflows/ci.yml)")
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	aead, err := appcrypto.NewAEAD(key)
	if err != nil {
		t.Fatalf("NewAEAD: %v", err)
	}
	checker := permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool)
	recorder := audit.NewPGRecorder(sharedPool)
	return backupapp.NewService(sharedPool, sharedMigratorDSN, aead, checker, recorder)
}

// TestBackup_ExportInspectRestore_RoundTrips is this repo's own version
// of what a human operator did once, manually, over real HTTP against a
// live server + real postgres:18 container, before this test existed
// (see docs/TODO.md's Stage 16 entry) — automated here so it's not a
// one-time check. Proves the full loop: create real data, export a real
// encrypted backup, create MORE data, restore the earlier backup, and
// confirm the later data is genuinely gone — not just that Export/
// Restore returned nil error.
func TestBackup_ExportInspectRestore_RoundTrips(t *testing.T) {
	ctx := context.Background()
	backupSvc := newTestBackupService(t)
	catalogueSvc := newTestCatalogueService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	if !backupSvc.Enabled() {
		t.Fatal("backupSvc.Enabled() = false, want true (sharedMigratorDSN should make this always true in this suite)")
	}

	uom, err := catalogueSvc.CreateUnitOfMeasure(ctx, principal, catalogueapp.CreateUnitOfMeasureParams{Code: "PCS", Name: "Pieces"})
	if err != nil {
		t.Fatalf("CreateUnitOfMeasure: %v", err)
	}
	preBackupName := "Pre-Backup Widget " + principal.OrganisationID.String()[:8]
	if _, err := catalogueSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{Name: preBackupName, BaseUOMID: uom.ID}); err != nil {
		t.Fatalf("CreateProduct (pre-backup): %v", err)
	}

	data, filename, err := backupSvc.Export(ctx, principal)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data) < 100 {
		t.Fatalf("Export produced suspiciously small output: %d bytes", len(data))
	}
	if filename == "" {
		t.Fatal("Export returned an empty filename")
	}

	header, err := backupSvc.Inspect(data)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if header.Version != 1 {
		t.Fatalf("header.Version = %d, want 1", header.Version)
	}
	if header.ArchiveBytes <= 0 {
		t.Fatalf("header.ArchiveBytes = %d, want > 0", header.ArchiveBytes)
	}

	// A row added AFTER the backup — this is what the restore below must
	// make disappear, proving Restore actually overwrote live data rather
	// than just returning success.
	postBackupName := "Post-Backup Widget " + principal.OrganisationID.String()[:8]
	if _, err := catalogueSvc.CreateProduct(ctx, principal, catalogueapp.CreateProductParams{Name: postBackupName, BaseUOMID: uom.ID}); err != nil {
		t.Fatalf("CreateProduct (post-backup): %v", err)
	}

	// The confirmation gate: wrong phrase must be rejected before ever
	// touching pg_restore.
	if err := backupSvc.Restore(ctx, principal, data, "not the phrase"); err == nil {
		t.Fatal("Restore succeeded with the wrong confirmation phrase — the gate did nothing")
	}

	if err := backupSvc.Restore(ctx, principal, data, backupapp.RestoreConfirmationPhrase); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	list, err := catalogueSvc.ListProducts(ctx, principal)
	if err != nil {
		t.Fatalf("ListProducts after restore: %v", err)
	}
	var sawPre, sawPost bool
	for _, p := range list {
		if p.Name == preBackupName {
			sawPre = true
		}
		if p.Name == postBackupName {
			sawPost = true
		}
	}
	if !sawPre {
		t.Error("pre-backup product is missing after restore — restore lost data it should have kept")
	}
	if sawPost {
		t.Error("post-backup product still exists after restore — restore did not actually revert live data")
	}
}

// TestBackup_Restore_RejectsTamperedFile proves a corrupted/tampered
// backup file is rejected before pg_restore ever runs against it —
// AEAD's own authentication (the header is "additional data", bound to
// the ciphertext) catches a flipped byte in either the header or the
// encrypted archive.
func TestBackup_Restore_RejectsTamperedFile(t *testing.T) {
	ctx := context.Background()
	backupSvc := newTestBackupService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	data, _, err := backupSvc.Export(ctx, principal)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	tampered := make([]byte, len(data))
	copy(tampered, data)
	// Flip one byte well past the header — inside the encrypted archive.
	tampered[len(tampered)-1] ^= 0xFF

	if err := backupSvc.Restore(ctx, principal, tampered, backupapp.RestoreConfirmationPhrase); err == nil {
		t.Fatal("Restore succeeded against a tampered file — AEAD authentication did not catch it")
	}
}

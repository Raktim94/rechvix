import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, apiUrl, ApiError } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

interface BackupHeader {
  version: number;
  created_at: string;
  postgres_version: string;
  sha256: string;
  archive_bytes: number;
}

const RESTORE_CONFIRM_PHRASE = "RESTORE";

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

/** Triggers a real browser "Save As" for a POST response — fetch has no
 * built-in equivalent of clicking a download link (the browser only
 * auto-saves a response's Content-Disposition on a real navigation/
 * anchor click, never on a fetch), so this reads the response as a
 * blob and manufactures a throwaway anchor to click programmatically.
 * Parses the same {"error":{...}} envelope api-client.ts's `request`
 * does, since this bypasses `request` entirely (it always JSON-parses,
 * which a binary backup file response is not). */
async function downloadPost(path: string, fallbackFilename: string): Promise<void> {
  const res = await fetch(apiUrl(path), { method: "POST", credentials: "include" });
  if (!res.ok) {
    const body: unknown = await res.json().catch(() => null);
    const message =
      body && typeof body === "object" && "error" in body && typeof (body as { error?: { message?: string } }).error?.message === "string"
        ? (body as { error: { message: string } }).error.message
        : `Export failed (${res.status}).`;
    throw new ApiError(res.status, "EXPORT_FAILED", message);
  }
  const disposition = res.headers.get("Content-Disposition") ?? "";
  const filename = /filename="([^"]+)"/.exec(disposition)?.[1] ?? fallbackFilename;
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function ExportPanel() {
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  async function handleExport() {
    setError(null);
    setDone(false);
    setBusy(true);
    try {
      await downloadPost("/backup/export", "rechvix-backup.nodedrbackup");
      setDone(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not create a backup.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className={layout.panel}>
      <h2>Export a backup</h2>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Downloads one encrypted file with every organisation's complete data on this instance — products, customers,
        invoices, the full accounting ledger, everything. Keep it somewhere safe; it's the only copy outside this
        server.
      </p>
      <div className={ui.formActions}>
        <button type="button" className={ui.btnPrimary} disabled={busy} onClick={() => void handleExport()}>
          {busy ? "Preparing backup…" : "Download backup"}
        </button>
      </div>
      {error ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {error}
        </p>
      ) : null}
      {done ? <p style={{ color: "var(--color-positive)", marginTop: 8 }}>Backup downloaded.</p> : null}
    </div>
  );
}

function RestorePanel() {
  const [file, setFile] = useState<File | null>(null);
  const [header, setHeader] = useState<BackupHeader | null>(null);
  const [confirmText, setConfirmText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [restored, setRestored] = useState(false);

  const inspect = useMutation({
    mutationFn: (f: File) => api.uploadRaw<BackupHeader>("/backup/inspect", f),
    onSuccess: (h) => {
      setHeader(h);
      setError(null);
    },
    onError: (err) => {
      setHeader(null);
      setError(err instanceof ApiError ? err.message : "Could not read this file.");
    },
  });

  const restore = useMutation({
    mutationFn: (f: File) => api.uploadRaw(`/backup/restore?confirm=${encodeURIComponent(RESTORE_CONFIRM_PHRASE)}`, f),
    onSuccess: () => {
      setRestored(true);
      setError(null);
    },
    onError: (err) => {
      setError(err instanceof ApiError ? err.message : "Restore failed.");
    },
  });

  function onFileChange(f: File | null) {
    setFile(f);
    setHeader(null);
    setConfirmText("");
    setRestored(false);
    setError(null);
    if (f) inspect.mutate(f);
  }

  const canRestore = !!file && !!header && confirmText === RESTORE_CONFIRM_PHRASE;

  return (
    <div className={layout.panel} style={{ borderColor: "var(--color-negative)" }}>
      <h2>Restore from a backup</h2>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        <strong>This replaces everything currently in the database</strong> with what's in the backup file. Every
        organisation, every invoice, every product created since that backup was taken will be gone. The app will be
        unavailable for a moment while it runs. Do this only if you mean to — there is no undo.
      </p>

      <div className={ui.field} style={{ marginBottom: 12 }}>
        <label htmlFor="restore-file">Backup file (.nodedrbackup)</label>
        <input
          id="restore-file"
          type="file"
          accept=".nodedrbackup"
          onChange={(e) => onFileChange(e.target.files?.[0] ?? null)}
        />
      </div>

      {inspect.isPending ? <p className={ui.muted}>Reading file…</p> : null}

      {header ? (
        <div className={ui.formGrid} style={{ marginBottom: 16 }}>
          <div>
            <span className={ui.muted}>Backup created</span>
            <div>{new Date(header.created_at).toLocaleString()}</div>
          </div>
          <div>
            <span className={ui.muted}>Size</span>
            <div>{formatBytes(header.archive_bytes)}</div>
          </div>
          <div>
            <span className={ui.muted}>PostgreSQL version</span>
            <div>{header.postgres_version || "—"}</div>
          </div>
        </div>
      ) : null}

      {header ? (
        <div className={ui.field} style={{ maxWidth: 360, marginBottom: 12 }}>
          <label htmlFor="restore-confirm">
            Type <strong>{RESTORE_CONFIRM_PHRASE}</strong> to confirm
          </label>
          <input
            id="restore-confirm"
            className={ui.input}
            value={confirmText}
            onChange={(e) => setConfirmText(e.target.value)}
            autoComplete="off"
          />
        </div>
      ) : null}

      <div className={ui.formActions}>
        <button
          type="button"
          className={ui.btnDanger}
          disabled={!canRestore || restore.isPending}
          onClick={() => file && restore.mutate(file)}
        >
          {restore.isPending ? "Restoring…" : "Restore now — this cannot be undone"}
        </button>
      </div>
      {error ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {error}
        </p>
      ) : null}
      {restored ? (
        <p style={{ color: "var(--color-positive)", marginTop: 8 }}>
          Restore complete. Sign in again to confirm everything looks right.
        </p>
      ) : null}
    </div>
  );
}

export function BackupPage() {
  const status = useQuery({
    queryKey: ["backup-status"],
    queryFn: () => api.get<{ enabled: boolean }>("/backup/status"),
  });

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Backup &amp; Restore</h1>
          <p className={layout.subtitle}>A complete, encrypted copy of this instance's data — download it, keep it safe.</p>
        </div>
      </div>

      {status.isPending ? (
        <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />
      ) : status.isError || !status.data?.enabled ? (
        <div className={layout.panel}>
          <p className={layout.emptyState}>
            Backup &amp; restore isn't set up on this deployment yet. An operator needs to set{" "}
            <code>BACKUP_DATABASE_DSN</code> (see <code>docs/operations/deployment.md</code>) — the standard
            docker-compose install already does this automatically.
          </p>
        </div>
      ) : (
        <>
          <ExportPanel />
          <RestorePanel />
        </>
      )}
    </div>
  );
}

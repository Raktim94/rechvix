import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, apiUrl, ApiError } from "../../lib/api-client";

interface Attachment {
  ID: string;
  Filename: string;
  ContentType: string;
  FileSizeBytes: number;
  CreatedAt: string;
}

export const MAX_ATTACHMENT_BYTES = 8 * 1024 * 1024; // mirrors accounting.app's maxExpenseAttachmentBytes
export const ALLOWED_TYPES = new Set(["image/png", "image/jpeg", "image/webp", "application/pdf"]);

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

export function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = reader.result as string;
      resolve(dataUrl.slice(dataUrl.indexOf(",") + 1));
    };
    reader.onerror = () => reject(new Error("Could not read this file."));
    reader.readAsDataURL(file);
  });
}

/** A receipt/bill for one manual expense (ExpensesPage) — a small,
 * per-row expandable panel rather than its own page, since this is
 * usually a single photo of a paper receipt, not a document library. */
export function ExpenseAttachments({ journalId }: { journalId: string }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const attachments = useQuery({
    queryKey: ["expense-attachments", journalId],
    queryFn: () => api.getListField<Attachment>(`/accounting/expenses/${journalId}/attachments`, "attachments"),
    enabled: open,
  });

  const upload = useMutation({
    mutationFn: async (file: File) => {
      const base64 = await readFileAsBase64(file);
      return api.post(`/accounting/expenses/${journalId}/attachments`, {
        filename: file.name,
        content_type: file.type,
        data_base64: base64,
      });
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["expense-attachments", journalId] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Could not upload this file."),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.delete(`/accounting/expense-attachments/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["expense-attachments", journalId] }),
  });

  function handleFile(file: File | undefined) {
    setError(null);
    if (!file) return;
    if (file.size > MAX_ATTACHMENT_BYTES) {
      setError("File is too large — please use one under 8MB.");
      return;
    }
    if (!ALLOWED_TYPES.has(file.type)) {
      setError("Please attach a PNG, JPEG, WEBP, or PDF file.");
      return;
    }
    upload.mutate(file);
  }

  const count = attachments.data?.length ?? 0;

  return (
    <div>
      <button type="button" className={ui.btnGhost} onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        📎 {open ? "Hide" : count > 0 ? `${count} attached` : "Attach"}
      </button>
      {open ? (
        <div style={{ marginTop: 6, display: "flex", flexDirection: "column", gap: 6, alignItems: "flex-start" }}>
          {attachments.isPending ? (
            <span className={ui.muted}>Loading…</span>
          ) : (attachments.data ?? []).length === 0 ? (
            <span className={ui.muted}>No documents attached yet.</span>
          ) : (
            <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 4 }}>
              {(attachments.data ?? []).map((a) => (
                <li key={a.ID} style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <a href={apiUrl(`/accounting/expense-attachments/${a.ID}`)} target="_blank" rel="noopener noreferrer">
                    {a.Filename}
                  </a>
                  <span className={ui.muted}>({formatBytes(a.FileSizeBytes)})</span>
                  <button type="button" className={ui.btnGhost} disabled={remove.isPending} onClick={() => remove.mutate(a.ID)}>
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}
          <label className={ui.btnSecondary} style={{ cursor: "pointer" }}>
            {upload.isPending ? "Uploading…" : "+ Add document"}
            <input
              type="file"
              accept="image/png,image/jpeg,image/webp,application/pdf"
              style={{ display: "none" }}
              disabled={upload.isPending}
              onChange={(e) => {
                handleFile(e.target.files?.[0]);
                e.target.value = "";
              }}
            />
          </label>
          {error ? (
            <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
              {error}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

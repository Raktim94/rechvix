import { useMutation } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import modal from "../../components/Modal.module.css";
import ui from "../../components/ui.module.css";
import { api } from "../../lib/api-client";
import { LOSS_REASONS, useMoveOpportunity, type Stage } from "./crm";

interface Move {
  id: string;
  stageId: string;
}

/**
 * Moves a deal like useMoveOpportunity, but moving it into a Lost stage first
 * asks why. The answer is saved on the deal's timeline as a note, so the
 * reason survives and shows up in the activity history.
 */
export function useMoveWithReason(stages: Stage[] | undefined): {
  mutate: (v: Move) => void;
  isPending: boolean;
  isError: boolean;
  error: unknown;
  dialog: ReactNode;
} {
  const base = useMoveOpportunity(stages);
  const [asking, setAsking] = useState<Move | null>(null);
  const note = useMutation({
    mutationFn: (v: { id: string; subject: string; notes: string }) => api.post(`/crm/opportunities/${v.id}/activities`, { kind: "NOTE", subject: v.subject, notes: v.notes }),
  });
  return {
    mutate: (v) => {
      if (stages?.find((s) => s.id === v.stageId)?.kind === "LOST") setAsking(v);
      else base.mutate(v);
    },
    isPending: base.isPending,
    isError: base.isError,
    error: base.error,
    dialog: asking ? (
      <LossDialog
        onCancel={() => setAsking(null)}
        onConfirm={(reason, detail) => {
          const v = asking;
          setAsking(null);
          base.mutate(v, { onSuccess: () => note.mutate({ id: v.id, subject: `Lost: ${reason}`, notes: detail }) });
        }}
      />
    ) : null,
  };
}

function LossDialog({ onCancel, onConfirm }: { onCancel: () => void; onConfirm: (reason: string, detail: string) => void }) {
  const [reason, setReason] = useState(LOSS_REASONS[0] ?? "Other");
  const [detail, setDetail] = useState("");
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancel();
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);
  return (
    <div className={modal.overlay} onClick={onCancel}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label="Why was this lead lost?" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Why was this lost?</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={onCancel}>
            ×
          </button>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            onConfirm(reason, detail.trim());
          }}
        >
          <div className={modal.body}>
            <div className={ui.field}>
              <label htmlFor="loss-reason">Reason</label>
              <select id="loss-reason" className={ui.select} autoFocus value={reason} onChange={(e) => setReason(e.target.value)}>
                {LOSS_REASONS.map((r) => (
                  <option key={r}>{r}</option>
                ))}
              </select>
            </div>
            <div className={ui.field}>
              <label htmlFor="loss-detail">Anything to add? (optional)</label>
              <input id="loss-detail" className={ui.input} value={detail} onChange={(e) => setDetail(e.target.value)} maxLength={300} />
            </div>
          </div>
          <div className={modal.footer}>
            <button type="button" className={ui.btnSecondary} onClick={onCancel}>
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary}>
              Mark as lost
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

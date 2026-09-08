import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ui from "./ui.module.css";
import { api } from "../lib/api-client";

interface ShareLink {
  ID: string;
  ExpiresAt: string;
  RevokedAt: string | null;
  CreatedAt: string;
}

/** GET/DELETE /share-links existed (Stage 9) with no screen that could
 * ever list one to revoke — "Share via WhatsApp" creates a fresh,
 * independently-revocable link on every click (SalesDetailPage's own
 * comment already claimed this), but nothing let anyone actually see or
 * revoke one early. Shown only when at least one link exists, so a
 * document nobody has shared yet doesn't grow an empty panel. */
export function ShareLinksPanel({ documentType, documentId }: { documentType: string; documentId: string }) {
  const queryClient = useQueryClient();
  const queryKey = ["share-links", documentType, documentId];

  const links = useQuery({
    queryKey,
    queryFn: () => api.getListField<ShareLink>(`/share-links?document_type=${documentType}&document_id=${documentId}`, "share_links"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api.delete(`/share-links/${id}`),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey }),
  });

  if (!links.data || links.data.length === 0) return null;

  return (
    <div className={ui.field} style={{ marginTop: 16 }}>
      <label>Share links</label>
      <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 6 }}>
        {links.data.map((l) => {
          const isLive = !l.RevokedAt && new Date(l.ExpiresAt) > new Date();
          return (
            <li key={l.ID} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: "var(--text-sm)" }}>
              <span className={ui.badge} data-tone={isLive ? "positive" : "neutral"}>
                {l.RevokedAt ? "Revoked" : isLive ? "Active" : "Expired"}
              </span>
              <span className={ui.muted}>
                Created {new Date(l.CreatedAt).toLocaleString()}
                {isLive ? ` · expires ${new Date(l.ExpiresAt).toLocaleDateString()}` : ""}
              </span>
              {isLive ? (
                <button type="button" className={ui.btnGhost} disabled={revoke.isPending} onClick={() => revoke.mutate(l.ID)}>
                  Revoke
                </button>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

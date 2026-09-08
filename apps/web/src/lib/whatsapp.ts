import { useMutation } from "@tanstack/react-query";
import { api, apiUrl } from "./api-client";

/** A WhatsApp "click to chat" deep link (`wa.me`) — no WhatsApp Business
 * API credential needed, works for any customer with a saved phone
 * number. Assumes an Indian 10-digit mobile number when the customer's
 * on-file number carries no country code, since that's what every
 * contact created via ContactsPage/BillingPage looks like today. */
export function whatsAppShareUrl(phone: string, message: string): string | null {
  const digits = phone.replace(/\D/g, "");
  if (!digits) return null;
  const withCountryCode = digits.length === 10 ? `91${digits}` : digits;
  return `https://wa.me/${withCountryCode}?text=${encodeURIComponent(message)}`;
}

/** apiUrl() returns a same-origin-relative path (e.g. "/api/v1/..."); a
 * link handed to WhatsApp is opened on the RECIPIENT's device, not this
 * one, so it must be absolute. Most self-hosted installs serve the API
 * from the same origin as the SPA (Stage 10a's WEB_DIST_DIR embedding),
 * so window.location.origin is correct there; a deployment with
 * VITE_API_BASE_URL pointed at a separate origin already gets an
 * absolute URL out of apiUrl() itself, left untouched below. */
function absoluteApiUrl(path: string): string {
  const url = apiUrl(path);
  return /^https?:\/\//.test(url) ? url : `${window.location.origin}${url}`;
}

/** Creates a signed, expiring, revocable share link (internal/modules/
 * notifications, Stage 9) on demand, then opens wa.me with a message
 * that carries a real link to GET /share/{token}/pdf — the customer
 * opens it and sees the actual invoice, no app or login needed on
 * their end, no PDF attach-by-hand required on this end. Created fresh
 * per click rather than reused/cached: each link is independently
 * revocable from this same click without needing a "manage this
 * document's share links" UI. Shared by SalesDetailPage's "Share via
 * WhatsApp" button and SalesListPage's row-level action so both send
 * the exact same kind of link. */
export function useShareSalesDocumentOnWhatsApp() {
  return useMutation({
    mutationFn: async (params: { documentId: string; phone: string; message: string }) => {
      const { token } = await api.post<{ token: string }>("/share-links", {
        document_type: "sales_document",
        document_id: params.documentId,
      });
      const pdfUrl = absoluteApiUrl(`/share/${token}/pdf`);
      const url = whatsAppShareUrl(params.phone, `${params.message}\n${pdfUrl}`);
      if (url) window.open(url, "_blank", "noopener,noreferrer");
    },
  });
}

package onlinepay

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	httpx "rechvix/internal/platform/http"
	"rechvix/internal/platform/permissions"
)

type Handlers struct{ svc *Service }

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) Mount(r chi.Router) {
	r.Get("/payments/providers", h.providers)
	r.Put("/payments/providers/{provider}", h.saveProvider)
	r.Post("/sales/documents/{id}/payment-links", h.createLink)
	r.Get("/sales/documents/{id}/payment-links", h.links)
}

// MountPublic registers the provider callbacks and the pages a customer lands on.
func (h *Handlers) MountPublic(r chi.Router) {
	r.Post("/payments/stripe/webhook/{org}", h.stripeWebhook)
	r.Get("/payments/return", h.paypalReturn)
	r.Get("/payments/thanks", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Thank you", "Your payment is being confirmed. You can close this page; the invoice will show as paid shortly.")
	})
	r.Get("/payments/cancelled", func(w http.ResponseWriter, r *http.Request) {
		page(w, "Payment cancelled", "No payment was taken. You can use the same link again whenever you are ready.")
	})
}

func page(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title></head><body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem"><h1>%s</h1><p>%s</p></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(body))
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_REQUEST", err.Error()))
	case errors.Is(err, ErrNotConfigured):
		httpx.WriteError(w, r, httpx.NewBadRequest("PROVIDER_NOT_CONFIGURED", "Set up and switch on that payment provider in Settings first."))
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "Not found."))
	default:
		// Provider refusals carry a message the user can act on (for example an invalid key).
		httpx.WriteError(w, r, httpx.NewBadRequest("PROVIDER_ERROR", err.Error()))
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func publicBase(r *http.Request) string {
	if v := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); v != "" {
		return v
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *Handlers) providers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Providers(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"providers": rows})
}

func (h *Handlers) saveProvider(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Enabled       bool   `json:"enabled"`
		Live          bool   `json:"live"`
		ClientID      string `json:"client_id"`
		Secret        string `json:"secret"`
		WebhookSecret string `json:"webhook_secret"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	if err := h.svc.SaveProvider(r.Context(), principal(r), strings.ToUpper(chi.URLParam(r, "provider")), ProviderInput{Enabled: b.Enabled, Live: b.Live, ClientID: b.ClientID, Secret: b.Secret, WebhookSecret: b.WebhookSecret}); err != nil {
		fail(w, r, err)
		return
	}
	h.providers(w, r)
}

func (h *Handlers) createLink(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	var b struct {
		Provider string `json:"provider"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 2048))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return
	}
	l, err := h.svc.CreateLink(r.Context(), principal(r), id, strings.ToUpper(b.Provider), publicBase(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handlers) links(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	rows, err := h.svc.Links(r.Context(), principal(r), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"links": rows})
}

func (h *Handlers) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	org, err := uuid.Parse(chi.URLParam(r, "org"))
	if err != nil {
		http.Error(w, "bad organisation", http.StatusBadRequest)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if err := h.svc.HandleStripeWebhook(r.Context(), org, payload, r.Header.Get("Stripe-Signature")); err != nil {
		http.Error(w, "webhook rejected", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) paypalReturn(w http.ResponseWriter, r *http.Request) {
	link, err := uuid.Parse(r.URL.Query().Get("link"))
	order := r.URL.Query().Get("token")
	if err != nil || order == "" {
		page(w, "Payment link problem", "This link is not valid.")
		return
	}
	done, err := h.svc.CompletePayPal(r.Context(), link, order)
	if err != nil || !done {
		page(w, "Payment not completed", "We could not confirm your payment. If you were charged, contact the seller with your PayPal receipt.")
		return
	}
	page(w, "Thank you", "Your payment was received. The invoice will show as paid.")
}

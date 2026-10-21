package shop

import (
	"encoding/json"
	"errors"
	"io"
	"net"
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

const cookieName = "rx_portal"

// Mount registers the staff-facing routes.
func (h *Handlers) Mount(r chi.Router) {
	r.Get("/shop/settings", h.getSettings)
	r.Put("/shop/settings", h.putSettings)
	r.Get("/shop/products", h.products)
	r.Put("/shop/products/{id}", h.setProduct)
	r.Get("/shop/orders", h.orders)
	r.Post("/shop/orders/{id}/status", h.orderStatus)
}

// MountPublic registers the visitor-facing routes: catalogue, orders and the client area.
func (h *Handlers) MountPublic(r chi.Router) {
	r.Get("/shop/{slug}", h.catalog)
	r.Post("/shop/{slug}/orders", h.placeOrder)
	r.Post("/shop/{slug}/portal/request-link", h.requestLink)
	r.Get("/shop/{slug}/portal/enter", h.enter)
	r.Get("/shop/{slug}/portal", h.portal)
	r.Post("/shop/{slug}/portal/signout", h.signout)
	r.Get("/shop/{slug}/portal/documents/{id}/file", h.file)
	r.Post("/shop/{slug}/portal/documents/{id}/pay", h.pay)
	r.Post("/shop/{slug}/portal/documents/{id}/sign", h.sign)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var forbidden *permissions.ErrForbidden
	switch {
	case errors.As(err, &forbidden):
		httpx.WriteError(w, r, httpx.NewForbidden("FORBIDDEN", "You do not have permission to perform this action."))
	case errors.Is(err, ErrInvalid):
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_REQUEST", err.Error()))
	case errors.Is(err, ErrLimit):
		httpx.WriteError(w, r, &httpx.AppError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: err.Error()})
	case errors.Is(err, ErrNoMail):
		httpx.WriteError(w, r, httpx.NewBadRequest("EMAIL_NOT_CONFIGURED", err.Error()))
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "Not found."))
	default:
		// Public callers get the provider's or validation message only for errors we raise on purpose; anything else is generic.
		httpx.WriteError(w, r, err)
	}
}

func principal(r *http.Request) permissions.Principal {
	p, _ := httpx.PrincipalFromContext(r.Context())
	return p
}

func decode[T any](w http.ResponseWriter, r *http.Request, limit int64) (T, bool) {
	var v T
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_BODY", "Request body is malformed."))
		return v, false
	}
	return v, true
}

func clientIP(r *http.Request) string {
	for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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

func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") || strings.HasPrefix(publicBase(r), "https://")
}

func (h *Handlers) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.svc.GetSettings(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, s)
}

func (h *Handlers) putSettings(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[Settings](w, r, 16384)
	if !ok {
		return
	}
	if err := h.svc.SaveSettings(r.Context(), principal(r), b); err != nil {
		fail(w, r, err)
		return
	}
	h.getSettings(w, r)
}

func (h *Handlers) products(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Products(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"products": rows})
}

func (h *Handlers) setProduct(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	b, ok := decode[struct {
		Published   bool   `json:"published"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}](w, r, 16384)
	if !ok {
		return
	}
	if err := h.svc.SetPublished(r.Context(), principal(r), id, b.Published, b.Title, b.Description); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) orders(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Orders(r.Context(), principal(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"orders": rows})
}

func (h *Handlers) orderStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return
	}
	b, ok := decode[struct {
		Status string `json:"status"`
	}](w, r, 1024)
	if !ok {
		return
	}
	if err := h.svc.SetOrderStatus(r.Context(), principal(r), id, strings.ToUpper(b.Status)); err != nil {
		fail(w, r, err)
		return
	}
	h.orders(w, r)
}

func (h *Handlers) catalog(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.Catalog(r.Context(), chi.URLParam(r, "slug"), r.URL.Query().Get("q"))
	if err != nil {
		fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handlers) placeOrder(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[struct {
		Name    string      `json:"name"`
		Email   string      `json:"email"`
		Phone   string      `json:"phone"`
		Address string      `json:"address"`
		Notes   string      `json:"notes"`
		Lines   []OrderLine `json:"lines"`
	}](w, r, 65536)
	if !ok {
		return
	}
	o, err := h.svc.PlaceOrder(r.Context(), chi.URLParam(r, "slug"), clientIP(r), OrderInput{Name: b.Name, Email: b.Email, Phone: b.Phone, Address: b.Address, Notes: b.Notes, Lines: b.Lines})
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"reference": o.Reference, "total": o.Total, "currency": o.Currency})
}

func (h *Handlers) requestLink(w http.ResponseWriter, r *http.Request) {
	b, ok := decode[struct {
		Email string `json:"email"`
	}](w, r, 2048)
	if !ok {
		return
	}
	if err := h.svc.RequestLogin(r.Context(), chi.URLParam(r, "slug"), clientIP(r), b.Email, publicBase(r)); err != nil {
		fail(w, r, err)
		return
	}
	// Same answer whether or not the address is a customer.
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "If that address belongs to a customer, a sign-in link is on its way."})
}

func (h *Handlers) enter(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tok, exp, err := h.svc.Enter(r.Context(), slug, r.URL.Query().Get("token"))
	if err != nil {
		http.Redirect(w, r, "/portal/"+slug+"?expired=1", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/api/v1/shop/", Expires: exp, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/portal/"+slug, http.StatusSeeOther)
}

func token(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

func (h *Handlers) portal(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Portal(r.Context(), chi.URLParam(r, "slug"), token(r))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewUnauthorized("NOT_SIGNED_IN", "Sign in to see your documents."))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, p)
}

func (h *Handlers) signout(w http.ResponseWriter, r *http.Request) {
	h.svc.Signout(r.Context(), token(r))
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/api/v1/shop/", MaxAge: -1, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) docID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, httpx.NewBadRequest("INVALID_ID", "id must be a UUID."))
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) file(w http.ResponseWriter, r *http.Request) {
	id, ok := h.docID(w, r)
	if !ok {
		return
	}
	data, ct, name, err := h.svc.Document(r.Context(), chi.URLParam(r, "slug"), token(r), id)
	if err != nil {
		httpx.WriteError(w, r, httpx.NewNotFound("NOT_FOUND", "Not found."))
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (h *Handlers) pay(w http.ResponseWriter, r *http.Request) {
	id, ok := h.docID(w, r)
	if !ok {
		return
	}
	b, ok := decode[struct {
		Provider string `json:"provider"`
	}](w, r, 1024)
	if !ok {
		return
	}
	url, err := h.svc.PayLink(r.Context(), chi.URLParam(r, "slug"), token(r), clientIP(r), id, b.Provider, publicBase(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (h *Handlers) sign(w http.ResponseWriter, r *http.Request) {
	id, ok := h.docID(w, r)
	if !ok {
		return
	}
	url, err := h.svc.SignLink(r.Context(), chi.URLParam(r, "slug"), token(r), clientIP(r), id, publicBase(r))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"url": url})
}

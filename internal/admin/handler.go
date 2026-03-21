package admin

import (
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Handler serves all admin dashboard pages.
type Handler struct {
	repo *Repository
	tmpl *template.Template
}

// NewHandler wires the repository and templates.
func NewHandler(repo *Repository, tmpl *template.Template) *Handler {
	return &Handler{repo: repo, tmpl: tmpl}
}

// Dashboard renders GET /admin/dashboard.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.repo.GetDashboardStats(r.Context())
	if err != nil {
		http.Error(w, "could not load stats", http.StatusInternalServerError)
		return
	}
	h.render(w, "dashboard.html", stats)
}

// Clients renders GET /admin/clients.
func (h *Handler) Clients(w http.ResponseWriter, r *http.Request) {
	landlords, err := h.repo.GetAllLandlords(r.Context())
	if err != nil {
		http.Error(w, "could not load clients", http.StatusInternalServerError)
		return
	}
	h.render(w, "clients.html", landlords)
}

// ClientDetail renders GET /admin/clients/{id}.
func (h *Handler) ClientDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	detail, err := h.repo.GetLandlordDetail(r.Context(), id)
	if err != nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}
	h.render(w, "client_detail.html", detail)
}

// Agents renders GET /admin/agents.
func (h *Handler) Agents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.repo.GetAllAgents(r.Context())
	if err != nil {
		http.Error(w, "could not load agents", http.StatusInternalServerError)
		return
	}
	h.render(w, "agents.html", agents)
}

// Payments renders GET /admin/payments.
func (h *Handler) Payments(w http.ResponseWriter, r *http.Request) {
	payments, err := h.repo.GetRecentPayments(r.Context())
	if err != nil {
		http.Error(w, "could not load payments", http.StatusInternalServerError)
		return
	}
	h.render(w, "payments.html", map[string]any{
		"Payments":  payments,
		"Unmatched": false,
	})
}

// UnmatchedPayments renders GET /admin/payments/unmatched.
func (h *Handler) UnmatchedPayments(w http.ResponseWriter, r *http.Request) {
	payments, err := h.repo.GetUnmatchedPayments(r.Context())
	if err != nil {
		http.Error(w, "could not load unmatched payments", http.StatusInternalServerError)
		return
	}
	h.render(w, "payments.html", map[string]any{
		"Payments":  payments,
		"Unmatched": true,
	})
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

package admin

import (
	"html/template"
	"net/http"

	"github.com/codercollo/rentloop/internal/auth"
	"github.com/go-chi/chi/v5"
)

const baseTemplate = "web/templates/admin_base.html"

// Handler serves all admin dashboard pages.
type Handler struct {
	repo *Repository
}

// NewHandler wires the repository.
// Templates are parsed per-handler so each page gets its own "content" block.
func NewHandler(repo *Repository, _ *template.Template) *Handler {
	return &Handler{repo: repo}
}

// parse loads base + one page template and returns a ready-to-execute template.
// This is the key fix: each page gets its own template.Template instance,
// preventing {{define "content"}} blocks from overwriting each other.
func parse(page string) *template.Template {
	return template.Must(template.ParseFiles(baseTemplate, "web/templates/"+page))
}

// nav returns the shared nav + path data injected into every page.
func nav(r *http.Request, extra map[string]any) map[string]any {
	data := map[string]any{
		"Path": r.URL.Path,
		"Nav": []struct{ URL, Icon, Label string }{
			{"/admin/dashboard", "📊", "Dashboard"},
			{"/admin/clients", "🏠", "Clients"},
			{"/admin/agents", "👤", "Agents"},
			{"/admin/payments", "💳", "Payments"},
			{"/admin/payments/unmatched", "⚠", "Unmatched"},
		},
	}
	for k, v := range extra {
		data[k] = v
	}
	return data
}

// render executes the named base template (which pulls in the page's content block).
func render(w http.ResponseWriter, page string, data any) {
	tmpl := parse(page)
	if err := tmpl.ExecuteTemplate(w, "admin_base.html", data); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
	}
}

// ShowSetup — one-time admin registration, env-gated.
func (h *Handler) ShowSetup(secret string, authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if secret == "" || r.URL.Query().Get("secret") != secret {
			http.NotFound(w, r)
			return
		}
		count, _ := h.repo.CountAdmins(r.Context())
		if count > 0 {
			http.NotFound(w, r)
			return
		}
		tmpl := template.Must(template.ParseFiles("web/templates/admin_login.html"))
		tmpl.ExecuteTemplate(w, "admin_login.html", map[string]any{
			"Setup":  true,
			"Secret": secret,
		})
	}
}

// DoSetup handles the one-time registration POST.
func (h *Handler) DoSetup(secret string, authSvc *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if secret == "" || r.URL.Query().Get("secret") != secret {
			http.NotFound(w, r)
			return
		}
		email := r.FormValue("email")
		password := r.FormValue("password")

		if _, err := authSvc.Register(r.Context(), email, password); err != nil {
			tmpl := template.Must(template.ParseFiles("web/templates/admin_login.html"))
			tmpl.ExecuteTemplate(w, "admin_login.html", map[string]any{
				"Setup": true, "Secret": secret, "Error": err.Error(),
			})
			return
		}

		if admin, err := authSvc.GetByEmail(r.Context(), email); err == nil && admin != nil {
			_ = authSvc.ForceActivate(r.Context(), admin.ID)
		}
		http.Redirect(w, r, "/admin/login", http.StatusFound)
	}
}

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := h.repo.GetDashboardStats(r.Context())
	if err != nil {
		http.Error(w, "could not load stats", http.StatusInternalServerError)
		return
	}
	render(w, "admin_dashboard.html", nav(r, map[string]any{"Stats": stats}))
}

func (h *Handler) Clients(w http.ResponseWriter, r *http.Request) {
	landlords, err := h.repo.GetAllLandlords(r.Context())
	if err != nil {
		http.Error(w, "could not load clients", http.StatusInternalServerError)
		return
	}
	render(w, "admin_clients.html", nav(r, map[string]any{"Landlords": landlords}))
}

func (h *Handler) ClientDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	detail, err := h.repo.GetLandlordDetail(r.Context(), id)
	if err != nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}
	render(w, "admin_client_detail.html", nav(r, map[string]any{
		"Landlord": detail.Landlord,
		"Units":    detail.Units,
		"Payments": detail.Payments,
	}))
}

func (h *Handler) Agents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.repo.GetAllAgents(r.Context())
	if err != nil {
		http.Error(w, "could not load agents", http.StatusInternalServerError)
		return
	}
	render(w, "admin_agents.html", nav(r, map[string]any{"Agents": agents}))
}

func (h *Handler) Payments(w http.ResponseWriter, r *http.Request) {
	payments, err := h.repo.GetRecentPayments(r.Context())
	if err != nil {
		http.Error(w, "could not load payments", http.StatusInternalServerError)
		return
	}
	render(w, "admin_payments.html", nav(r, map[string]any{
		"Payments":  payments,
		"Unmatched": false,
	}))
}

func (h *Handler) UnmatchedPayments(w http.ResponseWriter, r *http.Request) {
	payments, err := h.repo.GetUnmatchedPayments(r.Context())
	if err != nil {
		http.Error(w, "could not load unmatched", http.StatusInternalServerError)
		return
	}
	render(w, "admin_payments.html", nav(r, map[string]any{
		"Payments":  payments,
		"Unmatched": true,
	}))
}

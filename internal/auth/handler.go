package auth

import (
	"html/template"
	"net/http"
	"time"
)

// Handler serves the admin auth pages.
type Handler struct {
	svc       *Service
	templates *template.Template
}

// NewHandler wires the service and parses templates.
func NewHandler(svc *Service, tmpl *template.Template) *Handler {
	return &Handler{svc: svc, templates: tmpl}
}

// ShowLogin renders GET /admin/login.
func (h *Handler) ShowLogin(w http.ResponseWriter, r *http.Request) {
	h.render(w, "admin_login.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

// Login handles POST /admin/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	token, err := h.svc.Login(r.Context(), email, password)
	if err != nil {
		http.Redirect(w, r, "/admin/login?error=Invalid+credentials", http.StatusFound)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "rentloop_admin",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false, // set true in production behind HTTPS
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
	http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
}

// Activate handles GET /admin/activate?token=...
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if err := h.svc.Activate(r.Context(), token); err != nil {
		h.render(w, "admin_login.html", map[string]any{
			"Error": "Activation link is invalid or expired.",
		})
		return
	}
	http.Redirect(w, r, "/admin/login?error=Account+activated!+Please+log+in.", http.StatusFound)
}

// Logout handles POST /admin/logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "rentloop_admin",
		Value:  "",
		MaxAge: -1,
		Path:   "/",
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

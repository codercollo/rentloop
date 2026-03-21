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
	h.render(w, "login.html", map[string]any{"Error": r.URL.Query().Get("error")})
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
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
	http.Redirect(w, r, "/admin/dashboard", http.StatusFound)
}

// ShowRegister renders GET /admin/register.
func (h *Handler) ShowRegister(w http.ResponseWriter, r *http.Request) {
	h.render(w, "register.html", nil)
}

// Register handles POST /admin/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	_, err := h.svc.Register(r.Context(), email, password)
	if err != nil {
		h.render(w, "register.html", map[string]any{"Error": err.Error()})
		return
	}

	// In production send email with activation link.
	// For now redirect to activation confirmation page.
	http.Redirect(w, r, "/admin/login?error=Check+your+email+to+activate", http.StatusFound)
}

// Activate handles GET /admin/activate?token=...
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if err := h.svc.Activate(r.Context(), token); err != nil {
		h.render(w, "activate.html", map[string]any{"Error": err.Error()})
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
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

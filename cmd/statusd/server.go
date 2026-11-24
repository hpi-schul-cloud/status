package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/hpi-schul-cloud/status/internal/store"
	"github.com/hpi-schul-cloud/status/internal/web"
)

type application struct {
	db           *store.Store
	logger       *slog.Logger
	templates    *template.Template
	sessionKey   []byte
	cookieSecure bool
}

type session struct {
	Username string
	Role     string
	CSRF     string
	Expires  int64
}

type publicPageData struct {
	Title        string
	Logo         string
	Intro        string
	SupportEmail string
	Privacy      string
	Imprint      string
	Groups       []store.Group
	Incidents    []store.StatusChange
	OverallClass string
	OverallText  string
}

type adminPageData struct {
	Title    string
	Username string
	Role     string
	CSRF     string
	Error    string
	Notice   string
	Groups   []store.Group
	Statuses []statusOption
}

type statusOption struct {
	ID    string
	Label string
}

func newApplication(db *store.Store, logger *slog.Logger) (*application, error) {
	key := []byte(os.Getenv("SESSION_SECRET"))
	if len(key) < 32 {
		return nil, errors.New("SESSION_SECRET must contain at least 32 bytes")
	}
	tmpl, err := web.Templates()
	if err != nil {
		return nil, err
	}
	secure := strings.ToLower(os.Getenv("COOKIE_SECURE")) != "false"
	return &application{db: db, logger: logger, templates: tmpl, sessionKey: key, cookieSecure: secure}, nil
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.Ping(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /api/v1/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"ping": "pong"}})
	})
	mux.HandleFunc("GET /api/v1/status", a.publicAPI)
	mux.HandleFunc("GET /api/v1/components", a.publicAPI)
	mux.HandleFunc("GET /api/v1/incidents", a.legacyIncidentsAPI)
	mux.HandleFunc("GET /", a.publicPage)
	mux.HandleFunc("GET /assets/status.css", func(w http.ResponseWriter, r *http.Request) {
		css, _ := web.CSS()
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(css)
	})
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)
	mux.Handle("GET /admin", a.requireSession(a.adminPage))
	mux.Handle("POST /admin/components/{id}", a.requireSession(a.adminUpdate))
	mux.Handle("POST /admin/users", a.requireAdmin(a.createUser))
	mux.Handle("POST /admin/password", a.requireSession(a.changePassword))
	return securityHeaders(mux)
}

func (a *application) publicAPI(w http.ResponseWriter, r *http.Request) {
	settings, groups, err := a.db.PublicData(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	incidents, err := a.db.StatusHistory(r.Context(), 100)
	if err != nil {
		a.serverError(w, err)
		return
	}
	status, _, text := overall(groups)
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"page": settings, "status": status, "statusText": text, "groups": groups, "incidents": incidents}})
}

// legacyIncidentsAPI keeps the read-only contract consumed by SchulCloud's
// existing alert adapter. It exposes current non-operational components as
// active incidents; status changes and resolution remain admin-only.
func (a *application) legacyIncidentsAPI(w http.ResponseWriter, r *http.Request) {
	_, groups, err := a.db.PublicData(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": legacyIncidents(groups)})
}

func legacyIncidents(groups []store.Group) []map[string]any {
	incidents := make([]map[string]any, 0)
	for _, group := range groups {
		for _, component := range group.Items {
			if component.Status == "operational" {
				continue
			}
			status := 1
			if component.Status == "partial_outage" || component.Status == "major_outage" {
				status = 2
			}
			message := component.Message
			if message == "" {
				message = component.Status
			}
			incidents = append(incidents, map[string]any{
				"id": len(incidents) + 1, "component_id": 0,
				"name": component.Name, "message": message, "status": status,
				"created_at": component.UpdatedAt, "updated_at": component.UpdatedAt,
				"deleted_at": nil, "visible": 1, "stickied": false,
				"occurred_at": component.UpdatedAt, "user_id": 0,
				"notifications": false, "is_resolved": false,
				"human_status": component.Status, "latest_update_id": 0,
				"latest_status": status, "latest_human_status": component.Status,
				"latest_icon": "", "permalink": "/", "duration": 0,
			})
		}
	}
	return incidents
}

func validState(s string) bool {
	switch s {
	case "operational", "degraded", "partial_outage", "major_outage", "maintenance":
		return true
	}
	return false
}

func (a *application) publicPage(w http.ResponseWriter, r *http.Request) {
	settings, groups, err := a.db.PublicData(r.Context())
	if err != nil {
		a.serverError(w, err)
		return
	}
	incidents, err := a.db.StatusHistory(r.Context(), 50)
	if err != nil {
		a.serverError(w, err)
		return
	}
	status, class, text := overall(groups)
	data := publicPageData{Title: settings["page_title"], Logo: settings["logo_url"], Intro: settings["intro"], SupportEmail: settings["support_email"], Privacy: settings["privacy_url"], Imprint: settings["imprint_url"], Groups: groups, Incidents: incidents, OverallClass: class, OverallText: text}
	if data.Title == "" {
		data.Title = "Systemstatus"
	}
	_ = status
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "public", data); err != nil {
		a.logger.Error("render public page", "error", err)
	}
}

func overall(groups []store.Group) (string, string, string) {
	best := "operational"
	for _, group := range groups {
		for _, c := range group.Items {
			switch c.Status {
			case "major_outage":
				best = "major_outage"
			case "partial_outage":
				if best != "major_outage" {
					best = "partial_outage"
				}
			case "degraded":
				if best == "operational" {
					best = "degraded"
				}
			case "maintenance":
				if best == "operational" {
					best = "maintenance"
				}
			}
		}
	}
	switch best {
	case "major_outage":
		return best, best, "Es gibt derzeit einen größeren Ausfall."
	case "partial_outage":
		return best, best, "Einige Systeme sind teilweise beeinträchtigt."
	case "degraded":
		return best, best, "Einige Systeme sind eingeschränkt verfügbar."
	case "maintenance":
		return best, best, "Bei einigen Systemen finden Wartungsarbeiten statt."
	default:
		return best, "operational", "Alle Systeme funktionieren."
	}
}

func (a *application) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.readSession(r); ok {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	_ = a.templates.ExecuteTemplate(w, "login", map[string]string{"Title": "Systemstatus", "Error": ""})
}
func (a *application) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	role, ok := a.db.VerifyUser(r.Context(), username, r.FormValue("password"))
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_ = a.templates.ExecuteTemplate(w, "login", map[string]string{"Title": "Systemstatus", "Error": "Benutzername oder Passwort ungültig"})
		return
	}
	csrfBytes := make([]byte, 24)
	if _, err := rand.Read(csrfBytes); err != nil {
		a.serverError(w, err)
		return
	}
	sess := session{Username: username, Role: role, CSRF: base64.RawURLEncoding.EncodeToString(csrfBytes), Expires: time.Now().Add(12 * time.Hour).Unix()}
	cookie, err := a.signSession(sess)
	if err != nil {
		a.serverError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "status_session", Value: cookie, Path: "/", HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
func (a *application) logout(w http.ResponseWriter, r *http.Request) {
	sess, ok := a.readSession(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !a.validForm(w, r, sess) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "status_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
func (a *application) adminPage(w http.ResponseWriter, r *http.Request, sess session) {
	data, err := a.adminData(r, sess)
	if err != nil {
		a.serverError(w, err)
		return
	}
	data.Notice = r.URL.Query().Get("notice")
	data.Error = r.URL.Query().Get("error")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := a.templates.ExecuteTemplate(w, "admin", data); err != nil {
		a.logger.Error("render admin page", "error", err)
	}
}
func (a *application) adminData(r *http.Request, sess session) (adminPageData, error) {
	settings, groups, err := a.db.PublicData(r.Context())
	if err != nil {
		return adminPageData{}, err
	}
	data := adminPageData{Title: settings["page_title"], Username: sess.Username, Role: sess.Role, CSRF: sess.CSRF, Groups: groups, Statuses: []statusOption{{"operational", "Funktionsfähig"}, {"degraded", "Eingeschränkt"}, {"partial_outage", "Teilausfall"}, {"major_outage", "Ausfall"}, {"maintenance", "Wartung"}}}
	if data.Title == "" {
		data.Title = "Systemstatus"
	}
	return data, nil
}
func (a *application) adminUpdate(w http.ResponseWriter, r *http.Request, sess session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if !a.validForm(w, r, sess) {
		return
	}
	status := r.FormValue("status")
	message := r.FormValue("message")
	if !validState(status) || len(message) > 2000 {
		http.Redirect(w, r, "/admin?error=Ungültige+Eingabe", 303)
		return
	}
	if err := a.db.SetStatus(r.Context(), r.PathValue("id"), status, message, sess.Username); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/admin?notice=Status+gespeichert", 303)
}
func (a *application) createUser(w http.ResponseWriter, r *http.Request, sess session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if !a.validForm(w, r, sess) {
		return
	}
	err := a.db.CreateUser(r.Context(), strings.TrimSpace(r.FormValue("username")), r.FormValue("password"), r.FormValue("role"))
	if err != nil {
		http.Redirect(w, r, "/admin?error=Benutzer+konnte+nicht+angelegt+werden", 303)
		return
	}
	http.Redirect(w, r, "/admin?notice=Benutzer+angelegt", 303)
}

func (a *application) changePassword(w http.ResponseWriter, r *http.Request, sess session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if !a.validForm(w, r, sess) {
		return
	}
	if err := a.db.ChangePassword(r.Context(), sess.Username, r.FormValue("current_password"), r.FormValue("new_password")); err != nil {
		http.Redirect(w, r, "/admin?error=Passwort+konnte+nicht+geändert+werden", 303)
		return
	}
	http.Redirect(w, r, "/admin?notice=Passwort+geändert", 303)
}

func (a *application) requireSession(next func(http.ResponseWriter, *http.Request, session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.readSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, sess)
	})
}
func (a *application) requireAdmin(next func(http.ResponseWriter, *http.Request, session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.readSession(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if sess.Role != "admin" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r, sess)
	})
}

func (a *application) signSession(sess session) (string, error) {
	payload, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, a.sessionKey)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (a *application) readSession(r *http.Request) (session, bool) {
	cookie, err := r.Cookie("status_session")
	if err != nil {
		return session{}, false
	}
	sess, err := a.parseSession(cookie.Value)
	return sess, err == nil
}
func (a *application) parseSession(value string) (session, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return session{}, errors.New("malformed session")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return session{}, err
	}
	mac := hmac.New(sha256.New, a.sessionKey)
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return session{}, errors.New("invalid session signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return session{}, err
	}
	var sess session
	if err := json.Unmarshal(payload, &sess); err != nil {
		return session{}, err
	}
	if sess.Username == "" || sess.Expires < time.Now().Unix() {
		return session{}, errors.New("expired session")
	}
	return sess, nil
}
func (a *application) validForm(w http.ResponseWriter, r *http.Request, sess session) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return false
		}
	}
	if r.FormValue("csrf") == "" || !hmac.Equal([]byte(r.FormValue("csrf")), []byte(sess.CSRF)) {
		http.Error(w, "invalid csrf token", http.StatusForbidden)
		return false
	}
	return true
}
func (a *application) serverError(w http.ResponseWriter, err error) {
	a.logger.Error("request failed", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https:; style-src 'self'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

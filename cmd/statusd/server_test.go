package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hpi-schul-cloud/status/internal/store"
	"github.com/hpi-schul-cloud/status/internal/web"
)

func TestPublicEndpointsDoNotExposeWritesOrTokens(t *testing.T) {
	handler := (&application{}).routes()
	for _, request := range []struct{ method, path string }{
		{http.MethodPatch, "/api/v1/components/server"},
		{http.MethodPost, "/api/v1/components/server/status"},
		{http.MethodPost, "/admin/tokens"},
		{http.MethodPost, "/admin/tokens/example/delete"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(request.method, request.path, nil))
		if recorder.Code < 400 {
			t.Errorf("%s %s returned %d; want rejected", request.method, request.path, recorder.Code)
		}
	}
}

func TestAdminTemplateHasEditableMultilineMessageAndNoTokenUI(t *testing.T) {
	templates, err := web.Templates()
	if err != nil {
		t.Fatal(err)
	}
	data := adminPageData{
		Title: "Status", Username: "operator", Role: "operator", CSRF: "csrf",
		Groups:   []store.Group{{Name: "Platform", Items: []store.Component{{ID: "api", Name: "API", Status: "degraded", Message: "First line\nSecond line"}}}},
		Statuses: []statusOption{{ID: "operational", Label: "Funktionsfähig"}, {ID: "degraded", Label: "Eingeschränkt"}},
	}
	var rendered bytes.Buffer
	if err := templates.ExecuteTemplate(&rendered, "admin", data); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	if !strings.Contains(html, `<textarea name="message"`) || !strings.Contains(html, "First line\nSecond line") {
		t.Fatal("admin page does not render the saved multiline message in an editable field")
	}
	if strings.Contains(html, "API-Tokens") || strings.Contains(html, "/admin/tokens") {
		t.Fatal("admin page still contains API token controls")
	}
}

func TestOverallChoosesMostSevereComponentState(t *testing.T) {
	groups := []store.Group{{Items: []store.Component{{Status: "degraded"}, {Status: "partial_outage"}, {Status: "maintenance"}}}}
	status, class, text := overall(groups)
	if status != "partial_outage" || class != "partial_outage" || text == "" {
		t.Fatalf("unexpected overall status: %q %q %q", status, class, text)
	}
}

func TestSessionCookieRejectsModifiedSignature(t *testing.T) {
	a := &application{sessionKey: []byte("0123456789abcdef0123456789abcdef")}
	value, err := a.signSession(session{Username: "alice", Role: "admin", CSRF: "csrf", Expires: 9999999999})
	if err != nil {
		t.Fatal(err)
	}
	if len(value) < 2 {
		t.Fatal("empty session cookie")
	}
	if _, err := a.parseSession(value); err != nil {
		t.Fatalf("valid cookie rejected: %v", err)
	}
	if _, err := a.parseSession(value + "x"); err == nil {
		t.Fatal("modified cookie signature accepted")
	}
}

package web

import "testing"

func TestEmbeddedTemplatesParse(t *testing.T) {
	templates, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public", "login", "admin"} {
		if templates.Lookup(name) == nil {
			t.Errorf("template %q was not embedded", name)
		}
	}
	if css, err := CSS(); err != nil || len(css) == 0 {
		t.Fatalf("embedded CSS unavailable: len=%d err=%v", len(css), err)
	}
}

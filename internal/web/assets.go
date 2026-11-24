package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"time"
)

//go:embed page.html status.css
var files embed.FS

func Templates() (*template.Template, error) {
	css, err := files.ReadFile("status.css")
	if err != nil {
		return nil, err
	}
	cssHash := sha256.Sum256(css)
	return template.New("pages").Funcs(template.FuncMap{
		"statusLabel": func(status string) string {
			labels := map[string]string{"operational": "Funktionsfähig", "degraded": "Eingeschränkt", "partial_outage": "Teilausfall", "major_outage": "Ausfall", "maintenance": "Wartungsarbeiten"}
			if label, ok := labels[status]; ok {
				return label
			}
			return "Unbekannt"
		},
		"eq":           func(a, b string) bool { return a == b },
		"assetVersion": func() string { return hex.EncodeToString(cssHash[:8]) },
		"formatTime": func(value string) string {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return value
			}
			return parsed.UTC().Format("02.01.2006, 15:04 UTC")
		},
	}).ParseFS(files, "page.html")
}

func CSS() ([]byte, error) { return files.ReadFile("status.css") }

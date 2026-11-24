package main

import (
	"net/url"
	"testing"
)

func TestDatabaseURLFromEnvUsesURLWhenConfigured(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://external/db")
	t.Setenv("DATABASE_HOST", "ignored")

	got, err := databaseURLFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got != "postgres://external/db" {
		t.Fatalf("databaseURLFromEnv() = %q, want configured URL", got)
	}
}

func TestDatabaseURLFromEnvBuildsURLFromParts(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_HOST", "postgres")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_NAME", "status")
	t.Setenv("DATABASE_USER", "status user")
	t.Setenv("DATABASE_PASSWORD", "p@ss/word")

	got, err := databaseURLFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	password, ok := parsed.User.Password()
	if !ok || password != "p@ss/word" {
		t.Fatalf("parsed password = %q, want original password", password)
	}
	if parsed.User.Username() != "status user" || parsed.Host != "postgres:5432" || parsed.Path != "/status" || parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("unexpected database URL: %s", got)
	}
}

func TestDatabaseURLFromEnvRequiresCompleteParts(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_HOST", "postgres")
	t.Setenv("DATABASE_PORT", "5432")
	t.Setenv("DATABASE_NAME", "status")
	t.Setenv("DATABASE_USER", "status")
	t.Setenv("DATABASE_PASSWORD", "")

	if _, err := databaseURLFromEnv(); err == nil {
		t.Fatal("databaseURLFromEnv() succeeded with missing password")
	}
}

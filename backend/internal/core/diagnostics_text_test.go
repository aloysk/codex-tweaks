package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeDiagnosticTextRedactsKnownCredentials(t *testing.T) {
	key := "sk-" + strings.Repeat("fixture", 8)
	githubToken := "ghp_" + strings.Repeat("x", 36)
	jwt := "eyJ" + strings.Repeat("a", 12) + "." + strings.Repeat("b", 20) + "." + strings.Repeat("c", 20)
	for _, test := range []struct {
		name    string
		message string
		secret  string
	}{
		{"key", "request failed " + key, key},
		{"github", "request failed " + githubToken, githubToken},
		{"jwt", "request failed " + jwt, jwt},
		{"bearer", "Authorization: Bearer fixture-sensitive-value", "fixture-sensitive-value"},
		{"json", `{"password": "fixture sensitive value", "retry": 1}`, "fixture sensitive value"},
		{"quoted escape", `{"password": "fixture\"sensitive value", "retry": 1}`, "sensitive value"},
		{"environment", "MY_SERVICE_API_KEY=fixture-private-value", "fixture-private-value"},
		{"long quoted value", `password="` + strings.Repeat("fixture private ", 4_000), "fixture private"},
		{"query", "https://example.test/path?access_token=fixture-secret&retry=1", "fixture-secret"},
		{"url credentials", "https://fixture-user:fixture-password@example.test/repo", "fixture-password"},
		{"cookie", "Cookie: session=fixture-cookie; tracking=fixture-other", "fixture-cookie"},
		{"pem", "-----BEGIN " + "PRIVATE KEY-----\nfixture-key-material\n-----END PRIVATE KEY-----", "fixture-key-material"},
		{"windows home", `C:\Users\fixture-person\AppData\Local\file`, "fixture-person"},
		{"unix home", "/home/fixture-person/.config/file", "fixture-person"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := SafeDiagnosticText(test.message)
			if strings.Contains(got, test.secret) {
				t.Fatal("sensitive fixture remains in diagnostic")
			}
			if !strings.Contains(got, "[REDACTED") && !strings.Contains(got, "[USER]") {
				t.Fatalf("missing redaction marker: %q", got)
			}
		})
	}
}

func TestSafeDiagnosticTextEscapesControlsAndBoundsUTF8(t *testing.T) {
	message := "first\nforged\r\ttab\x1b[2J\u202e\u2028" + strings.Repeat("你好🙂", 10_000)
	got := SafeDiagnosticText(message)
	if strings.ContainsAny(got, "\n\r\t\x1b\u202e\u2028") {
		t.Fatal("diagnostic contains raw controls or line separators")
	}
	if !strings.Contains(got, `first\nforged\r\ttab\u001b`) {
		t.Fatalf("controls were not made visible: %.100q", got)
	}
	if !utf8.ValidString(got) || len(got) > diagnosticTextMaxBytes || !strings.HasSuffix(got, " [truncated]") {
		t.Fatalf("invalid bounded diagnostic: bytes=%d, valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestSafeDiagnosticTextRedactsBeforeTruncating(t *testing.T) {
	secret := "sk-" + strings.Repeat("fixture", 200)
	message := strings.Repeat("x", diagnosticTextMaxBytes-24) + " " + secret
	got := SafeDiagnosticText(message)
	if strings.Contains(got, "sk-") || strings.Contains(got, "fixture") {
		t.Fatal("a truncated credential prefix remains visible")
	}
	if len(got) > diagnosticTextMaxBytes {
		t.Fatalf("diagnostic is too large: %d", len(got))
	}
}

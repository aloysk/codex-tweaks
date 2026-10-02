package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const diagnosticTextMaxBytes = 4 * 1024

// External error messages may contain user content. Persist a fixed class;
// callers can keep the full error in memory for the requested operation.
func diagnosticErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, errors.ErrUnsupported):
		return "unsupported"
	default:
		return "failure"
	}
}

var diagnosticRedactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`), "[REDACTED PRIVATE KEY]"},
	{regexp.MustCompile(`(?i)(\b(?:authorization|proxy-authorization|cookie|set-cookie)\b["']?\s*[:=]\s*)[^\r\n]+`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(\b(?:[a-z][a-z0-9]*[_-])*(?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|password|passwd|pwd|secret|client[_-]?secret)\b["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\])*(?:"|$)|'(?:\\.|[^'\\])*(?:'|$)|[^\s,;&]+)`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(\b(?:bearer|basic)\s+)[a-z0-9+/=_.-]+`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`), "${1}[REDACTED]@"},
	{regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|gh[pousr]_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+|(?:AKIA|ASIA)[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]+)\b`), "[REDACTED]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`), "[REDACTED JWT]"},
	{regexp.MustCompile(`(?i)([a-z]:[\\/](?:users|documents and settings)[\\/])[^\\/\s]+`), "${1}[USER]"},
	{regexp.MustCompile(`(/(?:Users|home)/)[^/\s]+`), "${1}[USER]"},
}

// SafeDiagnosticText bounds and escapes a diagnostic and masks known credential
// formats. It cannot identify arbitrary private prose: external bodies and user
// content must be omitted at the call site rather than passed through this helper.
func SafeDiagnosticText(message string) string {
	return safeDiagnosticText(message, diagnosticTextMaxBytes)
}

func safeDiagnosticText(message string, maxBytes int) string {
	// Bound the work before applying patterns, but redact before the final output
	// truncation so a token crossing that boundary cannot leave a readable prefix.
	const scanLimit = 8 * diagnosticTextMaxBytes
	inputTruncated := len(message) > scanLimit
	if inputTruncated {
		message = message[:scanLimit]
	}
	message = strings.ToValidUTF8(message, "�")
	for _, rule := range diagnosticRedactions {
		message = rule.pattern.ReplaceAllString(message, rule.replacement)
	}
	var escaped strings.Builder
	for _, value := range message {
		switch value {
		case '\n':
			escaped.WriteString(`\n`)
		case '\r':
			escaped.WriteString(`\r`)
		case '\t':
			escaped.WriteString(`\t`)
		default:
			if unicode.IsControl(value) || unicode.Is(unicode.Cf, value) || unicode.Is(unicode.Zl, value) || unicode.Is(unicode.Zp, value) {
				fmt.Fprintf(&escaped, `\u%04x`, value)
			} else {
				escaped.WriteRune(value)
			}
		}
	}
	message = escaped.String()
	if inputTruncated {
		message += " [truncated]"
	}
	return truncateDiagnosticText(message, maxBytes)
}

func truncateDiagnosticText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	const suffix = " [truncated]"
	end := maxBytes - len(suffix)
	if end < 0 {
		return ""
	}
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + suffix
}

package poc

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	pemPrivateKeyPattern = regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	credentialPattern    = regexp.MustCompile(`(?i)(authorization|bearer[[:space:]]+|set-cookie|"(?:cookie|token|session_token|private_key)"[[:space:]]*:)`)
	highConfidenceSecret = regexp.MustCompile(`(?i)(-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|bearer[[:space:]]+[^[:space:]]+|set-cookie[[:space:]]*:|"(?:authorization|cookie|token|session_token|private_key)"[[:space:]]*:)`)
	queryURLPattern      = regexp.MustCompile(`(?i)https?://[^[:space:]"'<>]*\?[^[:space:]"'<>]+`)
)

func SafeURL(raw string) (*string, FieldStatus) {
	if raw == "" {
		return nil, FieldMissingInSource
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, FieldInvalidFormat
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return nil, FieldRedactedForSafety
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	safe := parsed.String()
	return &safe, FieldPresent
}

// SafeProfileURL keeps a real WeChat Channels profile URL when the page bridge
// returns one.  The collector must not manufacture a profile URL from an
// internal username because that identifier is not itself a public link.
func SafeProfileURL(raw string) (*string, FieldStatus) {
	if raw == "" {
		return nil, FieldMissingInSource
	}
	parsed, err := url.Parse(raw)
	if err != nil || !isSafeProfileURL(parsed) {
		return nil, FieldRedactedForSafety
	}
	safe := parsed.String()
	return &safe, FieldPresent
}

func isSafeProfileURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "https" ||
		parsed.Hostname() != "channels.weixin.qq.com" || parsed.Port() != "" || parsed.User != nil ||
		parsed.Fragment != "" || parsed.Path != "/web/pages/profile" {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) != 1 {
		return false
	}
	for key, values := range query {
		if key != "finderUsername" && key != "username" {
			return false
		}
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return false
		}
	}
	return true
}

func ScanOrdinaryOutput(raw []byte) error {
	switch {
	case pemPrivateKeyPattern.Match(raw):
		return errors.New("ordinary output contains a private-key marker")
	case credentialPattern.Match(raw):
		return errors.New("ordinary output contains a credential marker")
	}
	for _, match := range queryURLPattern.FindAll(raw, -1) {
		parsed, err := url.Parse(string(match))
		if err != nil || !isSafeProfileURL(parsed) {
			return errors.New("ordinary output contains a URL query")
		}
	}
	return nil
}

func RedactString(raw string) (*string, FieldStatus) {
	if raw == "" {
		return nil, FieldMissingInSource
	}
	if highConfidenceSecret.MatchString(raw) || queryURLPattern.MatchString(raw) {
		return nil, FieldRedactedForSafety
	}
	value := strings.Clone(raw)
	return &value, FieldPresent
}

func countRedactions(raw []byte) RedactionCounts {
	queryURLs := 0
	for _, match := range queryURLPattern.FindAll(raw, -1) {
		parsed, err := url.Parse(string(match))
		if err != nil || !isSafeProfileURL(parsed) {
			queryURLs++
		}
	}
	return RedactionCounts{
		CredentialKeys: len(credentialPattern.FindAll(raw, -1)),
		QueryURLs:      queryURLs,
		PEMMarkers:     len(pemPrivateKeyPattern.FindAll(raw, -1)),
	}
}

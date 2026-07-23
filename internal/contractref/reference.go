package contractref

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	eventSubjectPattern = regexp.MustCompile(`^[A-Za-z0-9_$*>-]+(?:\.[A-Za-z0-9_$*>-]+)*$`)
	templatePathPattern = regexp.MustCompile(`^\{[A-Za-z_][A-Za-z0-9_]*\}/`)
)

// HTTP normalizes a bounded HTTP method/path reference. Descriptive prose is
// rejected so it cannot become a platform contract merely because an analyst
// classified it as one.
func HTTP(value string) (string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(value))
	method := "GET"
	reference := ""
	switch len(fields) {
	case 1:
		reference = fields[0]
	case 2:
		if !HTTPMethod(fields[0]) {
			return "", "", false
		}
		method = strings.ToUpper(fields[0])
		reference = fields[1]
	default:
		return "", "", false
	}

	path := reference
	if strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://") {
		parsed, err := url.Parse(reference)
		if err != nil || parsed.Host == "" || parsed.User != nil ||
			(parsed.Scheme != "http" && parsed.Scheme != "https") {
			return "", "", false
		}
		path = parsed.Path
		if path == "" {
			path = "/"
		}
	} else if templatePathPattern.MatchString(reference) {
		path = "/" + reference
	} else if !strings.HasPrefix(reference, "/") {
		return "", "", false
	}
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	if path == "" || strings.ContainsAny(path, "\x00\r\n\t ") {
		return "", "", false
	}
	return method, path, true
}

func HTTPMethod(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "ANY", "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD":
		return true
	default:
		return false
	}
}

// EventSubject accepts NATS-style dot-delimited subjects and wildcard tokens.
// Payload descriptions, queue prose, and slash-delimited UI event names are
// intentionally not platform event contracts.
func EventSubject(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" && eventSubjectPattern.MatchString(value)
}

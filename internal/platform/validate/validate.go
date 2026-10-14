package validate

import (
	"regexp"
	"strings"
)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func Email(s string) bool {
	return emailRegex.MatchString(strings.TrimSpace(s))
}

func NonEmpty(s string) bool {
	return strings.TrimSpace(s) != ""
}

func MinLength(s string, min int) bool {
	return len(s) >= min
}

func MaxLength(s string, max int) bool {
	return len(s) <= max
}

func StringLength(s string, min, max int) bool {
	l := len(s)
	return l >= min && l <= max
}

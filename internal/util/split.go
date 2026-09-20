package util

import "strings"

// Split splits str on separator and drops empty tokens, including an empty final
// token. An empty separator returns no tokens; callers never pass one in
// practice.
func Split(str, separator string) []string {
	if separator == "" {
		return nil
	}
	var tokens []string
	idx := 0
	for {
		found := strings.Index(str[idx:], separator)
		if found < 0 {
			break
		}
		found += idx
		token := str[idx:found]
		if token != "" {
			tokens = append(tokens, token)
		}
		idx = found + len(separator)
	}
	token := str[idx:]
	if token != "" {
		tokens = append(tokens, token)
	}
	return tokens
}

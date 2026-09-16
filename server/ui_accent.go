package main

import "strings"

const defaultUIAccentColor = "#ff5b2e"

func isHexByte(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}

// normalizeUIAccentColor accepts #rgb or #rrggbb (optional leading #) and
// returns a lowercase #rrggbb. Invalid or empty values become the ember default.
func normalizeUIAccentColor(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return defaultUIAccentColor
	}
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	if len(s) == 4 {
		r, g, b := s[1], s[2], s[3]
		if isHexByte(r) && isHexByte(g) && isHexByte(b) {
			return "#" + string([]byte{r, r, g, g, b, b})
		}
		return defaultUIAccentColor
	}
	if len(s) == 7 {
		for i := 1; i < 7; i++ {
			if !isHexByte(s[i]) {
				return defaultUIAccentColor
			}
		}
		return s
	}
	return defaultUIAccentColor
}

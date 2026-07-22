package main

import (
	"sort"
	"strings"
)

type SystemFont struct {
	Family string `json:"family"`
	Source string `json:"source"`
}

func (s *AppService) ListSystemFonts() ([]SystemFont, error) {
	fonts := listPlatformFonts()
	seen := map[string]struct{}{}
	result := make([]SystemFont, 0, len(fonts))
	for _, font := range fonts {
		family := strings.TrimSpace(font.Family)
		if family == "" {
			continue
		}
		key := strings.ToLower(family)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, SystemFont{Family: family, Source: font.Source})
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Family) < strings.ToLower(result[j].Family)
	})
	return result, nil
}

func isValidFontFamily(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 120 {
		return false
	}
	if isAllowed(value, "manrope", "system", "mono") {
		return true
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '-' || r == '_' || r == '.' || r == ',' || r == '&' || r == '+' || r == '(' || r == ')':
		case r > 127: // CJK / localized family names
		default:
			return false
		}
	}
	return true
}

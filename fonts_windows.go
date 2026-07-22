//go:build windows

package main

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func listPlatformFonts() []SystemFont {
	fonts := make([]SystemFont, 0, 256)
	fonts = append(fonts, readWindowsFontRegistry(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`)...)
	fonts = append(fonts, readWindowsFontRegistry(registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`)...)
	return fonts
}

func readWindowsFontRegistry(root registry.Key, path string) []SystemFont {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()

	names, err := key.ReadValueNames(0)
	if err != nil {
		return nil
	}
	fonts := make([]SystemFont, 0, len(names))
	for _, name := range names {
		family := strings.TrimSpace(name)
		family = strings.TrimSuffix(family, " (TrueType)")
		family = strings.TrimSuffix(family, " (OpenType)")
		family = strings.TrimSuffix(family, " (All res)")
		family = strings.TrimSpace(family)
		if family == "" {
			continue
		}
		// Registry values often look like "Arial Bold" — keep base family when possible.
		if idx := strings.LastIndex(family, " & "); idx > 0 {
			family = strings.TrimSpace(family[:idx])
		}
		_ = filepath.Base(family)
		fonts = append(fonts, SystemFont{Family: family, Source: "windows"})
	}
	return fonts
}

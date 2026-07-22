//go:build !windows && !darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func listPlatformFonts() []SystemFont {
	if listed := listFontsViaFCList(); len(listed) > 0 {
		return listed
	}
	home, _ := os.UserHomeDir()
	roots := []string{
		"/usr/share/fonts",
		"/usr/local/share/fonts",
		filepath.Join(home, ".fonts"),
		filepath.Join(home, ".local", "share", "fonts"),
	}
	fonts := make([]SystemFont, 0, 128)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext != ".ttf" && ext != ".otf" && ext != ".ttc" {
				return nil
			}
			family := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			family = strings.ReplaceAll(family, "-", " ")
			family = strings.TrimSpace(family)
			if family != "" {
				fonts = append(fonts, SystemFont{Family: family, Source: "linux"})
			}
			return nil
		})
	}
	return fonts
}

func listFontsViaFCList() []SystemFont {
	out, err := exec.Command("fc-list", ":", "family").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(string(out), "\n")
	fonts := make([]SystemFont, 0, len(lines))
	for _, line := range lines {
		family := strings.TrimSpace(strings.Split(line, ",")[0])
		if family == "" {
			continue
		}
		fonts = append(fonts, SystemFont{Family: family, Source: "linux"})
	}
	return fonts
}

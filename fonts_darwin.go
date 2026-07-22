//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func listPlatformFonts() []SystemFont {
	if listed := listFontsViaNSFontManager(); len(listed) > 0 {
		return listed
	}
	if listed := listFontsViaFCList(); len(listed) > 0 {
		return listed
	}
	return listFontsFromDirectories()
}

func listFontsViaNSFontManager() []SystemFont {
	script := `
from AppKit import NSFontManager
manager = NSFontManager.sharedFontManager()
for family in manager.availableFontFamilies():
    if family:
        print(family)
`
	out, err := exec.Command("python3", "-c", script).Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(string(out), "\n")
	fonts := make([]SystemFont, 0, len(lines))
	for _, line := range lines {
		family := strings.TrimSpace(line)
		if family == "" {
			continue
		}
		fonts = append(fonts, SystemFont{Family: family, Source: "macos"})
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
		fonts = append(fonts, SystemFont{Family: family, Source: "macos"})
	}
	return fonts
}

func listFontsFromDirectories() []SystemFont {
	fonts := make([]SystemFont, 0, 256)
	home, _ := os.UserHomeDir()
	roots := []string{
		"/System/Library/Fonts",
		"/Library/Fonts",
		filepath.Join(home, "Library", "Fonts"),
	}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext != ".ttf" && ext != ".otf" && ext != ".ttc" && ext != ".dfont" {
				return nil
			}
			family := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			family = strings.ReplaceAll(family, "-", " ")
			family = strings.TrimSpace(family)
			if family != "" {
				fonts = append(fonts, SystemFont{Family: family, Source: "macos"})
			}
			return nil
		})
	}
	return fonts
}

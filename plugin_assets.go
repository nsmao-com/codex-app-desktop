package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	pluginIconURLPrefix = "/_nice/plugin-icons/"
	maxPluginIconBytes  = 4 << 20
)

type pluginIconAsset struct {
	path     string
	mimeType string
}

type pluginAssetServer struct {
	mu    sync.RWMutex
	icons map[string]pluginIconAsset
}

func newPluginAssetServer() *pluginAssetServer {
	return &pluginAssetServer{icons: make(map[string]pluginIconAsset)}
}

func (s *pluginAssetServer) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, pluginIconURLPrefix) {
			next.ServeHTTP(response, request)
			return
		}
		s.serveIcon(response, request)
	})
}

func (s *pluginAssetServer) serveIcon(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(request.URL.Path, pluginIconURLPrefix)
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(response, request)
		return
	}

	s.mu.RLock()
	asset, exists := s.icons[token]
	s.mu.RUnlock()
	if !exists {
		http.NotFound(response, request)
		return
	}

	file, err := os.Open(asset.path)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPluginIconBytes {
		http.NotFound(response, request)
		return
	}

	response.Header().Set("Cache-Control", "private, max-age=3600")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'; sandbox")
	response.Header().Set("Content-Type", asset.mimeType)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(response, request, filepath.Base(asset.path), info.ModTime(), file)
}

func (s *pluginAssetServer) updatePluginIconURLs(result map[string]any) {
	icons := make(map[string]pluginIconAsset)
	marketplaces, _ := result["marketplaces"].([]any)
	for _, marketplaceValue := range marketplaces {
		marketplace, _ := marketplaceValue.(map[string]any)
		plugins, _ := marketplace["plugins"].([]any)
		for _, pluginValue := range plugins {
			plugin, _ := pluginValue.(map[string]any)
			iconURL, token, asset, ok := resolvePluginIcon(plugin)
			if !ok {
				continue
			}
			ui, _ := plugin["interface"].(map[string]any)
			ui["logoUrl"] = iconURL
			icons[token] = asset
		}
	}

	s.mu.Lock()
	s.icons = icons
	s.mu.Unlock()
}

func resolvePluginIcon(plugin map[string]any) (string, string, pluginIconAsset, bool) {
	ui, _ := plugin["interface"].(map[string]any)
	logo, _ := ui["logo"].(string)
	if logo == "" {
		return "", "", pluginIconAsset{}, false
	}
	if logoURL, _ := ui["logoUrl"].(string); logoURL != "" {
		return "", "", pluginIconAsset{}, false
	}

	source, _ := plugin["source"].(map[string]any)
	sourceType, _ := source["type"].(string)
	root, _ := source["path"].(string)
	if sourceType != "local" || root == "" {
		return "", "", pluginIconAsset{}, false
	}

	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", "", pluginIconAsset{}, false
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", "", pluginIconAsset{}, false
	}

	iconPath := logo
	if !filepath.IsAbs(iconPath) {
		iconPath = filepath.Join(rootPath, iconPath)
	}
	iconPath, err = filepath.Abs(iconPath)
	if err != nil {
		return "", "", pluginIconAsset{}, false
	}
	iconPath, err = filepath.EvalSymlinks(iconPath)
	if err != nil || !pathWithinRoot(rootPath, iconPath) {
		return "", "", pluginIconAsset{}, false
	}

	mimeType, ok := pluginIconMIME(filepath.Ext(iconPath))
	if !ok {
		return "", "", pluginIconAsset{}, false
	}
	info, err := os.Stat(iconPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPluginIconBytes {
		return "", "", pluginIconAsset{}, false
	}

	tokenSource := iconPath + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
	token := fmt.Sprintf("%x", sha256.Sum256([]byte(tokenSource)))
	return pluginIconURLPrefix + token, token, pluginIconAsset{path: iconPath, mimeType: mimeType}, true
}

func pathWithinRoot(root string, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pluginIconMIME(extension string) (string, bool) {
	switch strings.ToLower(extension) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".svg":
		return "image/svg+xml", true
	case ".ico":
		return "image/x-icon", true
	default:
		return "", false
	}
}

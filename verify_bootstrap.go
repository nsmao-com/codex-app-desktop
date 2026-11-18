//go:build ignore

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"nice_codex_desktop/internal/codex"
)

// Smoke-checks the same Bootstrap / ListModelProviders contracts the UI uses,
// without needing the Wails window or WebView bindings.
func main() {
	if os.Getenv("NICE_CODEX_VERIFY_CHILD") == "1" && len(os.Args) == 2 && os.Args[1] == "--version" {
		executable, _ := os.Executable()
		if strings.Contains(filepath.Dir(executable), "broken-start") {
			os.Exit(3)
		}
		if strings.Contains(filepath.Dir(executable), "empty-version") {
			return
		}
		fmt.Println("codex-cli 0.169.1")
		return
	}
	if os.Getenv("NICE_CODEX_PROVIDER_VERIFY_CHILD") == "1" {
		verifyCodexProviderChild()
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--codex-providers" {
		if err := verifyCodexProviderIdentity(); err != nil {
			fail("Codex providers: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--claude-plugin-child" {
		verifyClaudePluginChild()
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--claude-plugins" {
		if err := verifyClaudePlugins(); err != nil {
			fail("Claude plugins: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--vision" {
		if err := verifyVisionContracts(); err != nil {
			fail("vision: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--discovery" {
		if err := verifyCLIDiscovery(); err != nil {
			fail("CLI discovery: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--claude-health" {
		if err := verifyClaudeHealth(); err != nil {
			fail("Claude health: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--claude-detect" {
		status := detectClaudeRuntime()
		_ = json.NewEncoder(os.Stdout).Encode(status)
		if !status.Available {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--capabilities" {
		if err := verifyCapabilityContracts(); err != nil {
			fail("capabilities: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--codex-detect" {
		detection := codex.Detect()
		_ = json.NewEncoder(os.Stdout).Encode(detection)
		if !detection.Available {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--reload" {
		if err := verifyRuntimeReload(); err != nil {
			fail("runtime reload: %v", err)
		}
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "--fastctx-command" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[2:], "updates": os.Getenv("FASTCTX_DISABLE_UPDATE_CHECK")})
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--fastctx" {
		if err := verifyFastCtxIntegration(); err != nil {
			fail("fastctx integration: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--models" {
		if err := verifyModelConfiguration(); err != nil {
			fail("model configuration: %v", err)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--local-files" {
		if err := verifyLocalFiles(); err != nil {
			fail("local files: %v", err)
		}
		return
	}
	service := &AppService{
		settings:         defaultSettings(),
		settingsPath:     resolveSettingsPath(),
		allowedThreads:   map[string]string{},
		allowedImages:    map[string]struct{}{},
		terminalSessions: map[string]*terminalSession{},
		sessions:         map[string]*SessionRecord{},
		externalRuns:     map[string]*externalRun{},
	}
	if loaded, err := readSettings(service.settingsPath); err == nil {
		service.settings = loaded
	}
	service.sessions = service.loadSessions()

	boot := service.Bootstrap()
	providers, err := service.ListModelProviders()
	if err != nil {
		fail("ListModelProviders: %v", err)
	}

	problems := []string{}
	if !boot.Codex.Available {
		problems = append(problems, "Codex CLI not available")
	}
	if len(boot.AgentProviders) < 1 || boot.AgentProviders[0].Kind != "codex" {
		problems = append(problems, fmt.Sprintf("Bootstrap agentProviders=%d want Codex first", len(boot.AgentProviders)))
	}

	data, _ := providers["data"].([]any)
	if len(data) != 1 {
		problems = append(problems, fmt.Sprintf("ListModelProviders count=%d want 1", len(data)))
	}
	names := make([]string, 0, 1)
	for _, item := range data {
		record, _ := item.(map[string]any)
		name, _ := record["name"].(string)
		kind, _ := record["kind"].(string)
		configured, _ := record["configured"].(bool)
		names = append(names, name)
		if name != "Codex" || kind != "codex" {
			problems = append(problems, "unexpected provider: "+name+"/"+kind)
		}
		if !configured {
			problems = append(problems, kind+" not configured/ready")
		}
	}
	if strings.Join(names, ",") != "Codex" {
		problems = append(problems, "provider labels="+strings.Join(names, ","))
	}

	// Codex-only: workbench modelProvider must be empty.
	mp := strings.TrimSpace(boot.Settings.ModelProvider)
	if mp != "" {
		problems = append(problems, "settings.modelProvider should be empty for Codex-only, got="+mp)
	}

	result := map[string]any{
		"ok":             len(problems) == 0,
		"problems":       problems,
		"providers":      names,
		"workspace":      boot.Settings.Workspace,
		"modelProvider":  boot.Settings.ModelProvider,
		"model":          boot.Settings.Model,
		"sessions":       len(service.sessions),
		"agentProviders": summarizeAgents(boot.AgentProviders),
	}
	home, _ := os.UserHomeDir()
	codexHits := scanCodexRolloutTokenUsage(resolveCodexHome())
	codexUsage := tokenBreakdown{}
	for _, hit := range codexHits {
		codexUsage.Input += hit.Breakdown.Input
		codexUsage.Cached += hit.Breakdown.Cached
		codexUsage.Output += hit.Breakdown.Output
		codexUsage.Reasoning += hit.Breakdown.Reasoning
		codexUsage.Total += hit.Breakdown.Total
	}
	result["usageProbe"] = map[string]any{
		"codex":    codexUsage,
		"gemini":   collectGeminiUsage(resolveGeminiHome(), ""),
		"opencode": collectOpenCodeUsage(home, "", 0),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
	if len(problems) > 0 {
		os.Exit(1)
	}
}

func verifyVisionContracts() error {
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	if err != nil {
		return err
	}
	responses := map[string]string{
		"chat":      `{"choices":[{"finish_reason":"stop","message":{"content":"图片文字"}}]}`,
		"responses": `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"图片文字"}]}]}`,
		"anthropic": `{"stop_reason":"end_turn","content":[{"type":"text","text":"图片文字"}]}`,
	}
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		var payload map[string]any
		var requestPath, auth, key, version string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestPath, auth, key, version = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version")
			_ = json.NewDecoder(r.Body).Decode(&payload)
			_, _ = io.WriteString(w, responses[protocol])
		}))
		config := VisionConfiguration{BaseURL: server.URL + "/v1", Model: "vision-id", APIKey: "verify-secret", Protocol: protocol}
		analysis, analysisErr := analyzeVisionImages(context.Background(), config, "识别数字", [][]byte{image}, visionClient())
		server.Close()
		if analysisErr != nil || analysis.Text != "图片文字" || analysis.Model != "vision-id" || analysis.ImageCount != 1 {
			return fmt.Errorf("%s analysis contract: %v", protocol, analysisErr)
		}
		wantPath, field := "/v1/chat/completions", "messages"
		if protocol == "responses" {
			wantPath, field = "/v1/responses", "input"
		}
		if protocol == "anthropic" {
			wantPath = "/v1/messages"
		}
		if requestPath != wantPath || payload["model"] != "vision-id" || payload["stream"] != false {
			return fmt.Errorf("%s request route/model mismatch", protocol)
		}
		if protocol == "anthropic" {
			if key != "verify-secret" || auth != "" || version != "2023-06-01" || payload["max_tokens"] != float64(8192) {
				return fmt.Errorf("anthropic headers/limit mismatch")
			}
		} else if auth != "Bearer verify-secret" || key != "" {
			return fmt.Errorf("%s auth mismatch", protocol)
		}
		if protocol == "responses" && payload["store"] != false {
			return fmt.Errorf("responses store must be false")
		}
		messages, ok := payload[field].([]any)
		if !ok || len(messages) != 1 {
			return fmt.Errorf("%s missing messages", protocol)
		}
		message, ok := messages[0].(map[string]any)
		if !ok {
			return fmt.Errorf("%s malformed message", protocol)
		}
		content, ok := message["content"].([]any)
		if !ok || len(content) != 3 {
			return fmt.Errorf("%s missing image content", protocol)
		}
		encoded, _ := json.Marshal(content)
		if !strings.Contains(string(encoded), base64.StdEncoding.EncodeToString(image)) || !strings.Contains(string(encoded), "识别数字") {
			return fmt.Errorf("%s image/question missing", protocol)
		}
		imagePart := content[2].(map[string]any)
		if protocol == "anthropic" {
			source, ok := imagePart["source"].(map[string]any)
			if !ok || source["media_type"] != "image/png" || imagePart["type"] != "image" {
				return fmt.Errorf("anthropic image shape")
			}
		} else if protocol == "responses" {
			if imagePart["type"] != "input_image" {
				return fmt.Errorf("responses image shape")
			}
		} else if imagePart["type"] != "image_url" {
			return fmt.Errorf("chat image shape")
		}
	}
	for _, invalid := range []struct{ protocol, body string }{
		{"chat", `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`},
		{"chat", `{"choices":[{"finish_reason":"stop","message":{"content":" "}}]}`},
		{"chat", `{"error":{"message":"private secret"}}`},
		{"chat", `not-json`},
		{"responses", `{"status":"incomplete","output":[]}`},
		{"anthropic", `{"stop_reason":"max_tokens","content":[]}`},
		{"chat", `{"choices":[{"finish_reason":"stop","message":{"content":"` + strings.Repeat("a", 64001) + `"}}]}`},
	} {
		if _, err := parseVisionAnalysis([]byte(invalid.body), invalid.protocol); err == nil {
			return fmt.Errorf("accepted incomplete/invalid %s response", invalid.protocol)
		}
	}
	for _, address := range []string{"https://user:secret@example.com/v1", "https://example.com/v1?key=secret", "http://example.com/v1"} {
		config := VisionConfiguration{BaseURL: address}
		if validateVisionConfiguration(&config, false) == nil {
			return fmt.Errorf("accepted unsafe vision address")
		}
	}
	root, err := os.MkdirTemp("", "nice-vision-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "picture.png")
	if err := os.WriteFile(path, image, 0600); err != nil {
		return err
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"b"},{"id":"a"},{"id":"a"}]}`)
			return
		}
		_, _ = io.WriteString(w, responses["chat"])
	}))
	defer server.Close()
	service := &AppService{settings: defaultSettings(), settingsPath: filepath.Join(root, "settings.json"), allowedImages: map[string]struct{}{}}
	service.settings.Vision = VisionConfiguration{BaseURL: server.URL + "/v1", Model: "vision-id", Protocol: "chat"}
	if _, err := service.AnalyzeComposerImages(context.Background(), "", []string{path}); err == nil {
		return fmt.Errorf("accepted unauthorized attachment")
	}
	if requests.Load() != 0 {
		return fmt.Errorf("unauthorized image sent")
	}
	service.allowedImages[imageAttachmentKey(path)] = struct{}{}
	if _, err := service.AnalyzeComposerImages(context.Background(), "", []string{path}); err != nil {
		return err
	}
	for _, images := range [][]string{nil, {path, path, path, path, path, path, path, path, path}} {
		if _, err := service.AnalyzeComposerImages(context.Background(), "", images); err == nil {
			return fmt.Errorf("image count not enforced")
		}
	}
	if _, err := service.AnalyzeComposerImages(context.Background(), strings.Repeat("x", 32001), []string{path}); err == nil {
		return fmt.Errorf("question limit not enforced")
	}
	if err := os.WriteFile(path, []byte("not an image"), 0600); err != nil {
		return err
	}
	if _, err := service.AnalyzeComposerImages(context.Background(), "", []string{path}); err == nil {
		return fmt.Errorf("accepted spoofed image")
	}
	if requests.Load() != 1 {
		return fmt.Errorf("invalid image sent")
	}
	models, err := service.ListVisionModels(context.Background(), service.settings.Vision)
	if err != nil || strings.Join(models, ",") != "a,b" {
		return fmt.Errorf("model catalog mismatch: %v", err)
	}
	var pageQueries []string
	paged := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "verify-secret" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pageQueries = append(pageQueries, r.URL.Query().Get("after_id"))
		if r.URL.Query().Get("after_id") == "" {
			_, _ = io.WriteString(w, `{"data":[{"id":"claude-b"}],"has_more":true,"last_id":"claude-b"}`)
			return
		}
		if r.URL.Query().Get("after_id") == "claude-b" {
			_, _ = io.WriteString(w, `{"data":[{"id":"claude-a"}],"has_more":false,"last_id":"claude-a"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[],"has_more":true,"last_id":"unexpected"}`)
	}))
	defer paged.Close()
	models, err = service.ListVisionModels(context.Background(), VisionConfiguration{BaseURL: paged.URL, APIKey: "verify-secret", Protocol: "anthropic"})
	if err != nil || strings.Join(models, ",") != "claude-a,claude-b" || strings.Join(pageQueries, ",") != ",claude-b" {
		return fmt.Errorf("anthropic model pagination mismatch: %v / %v / %v", err, models, pageQueries)
	}
	looping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"same"}],"has_more":true,"last_id":"same"}`)
	}))
	defer looping.Close()
	if _, err := service.ListVisionModels(context.Background(), VisionConfiguration{BaseURL: looping.URL, APIKey: "verify-secret", Protocol: "anthropic"}); err == nil {
		return fmt.Errorf("repeated model cursor accepted")
	}
	for _, body := range []string{
		`null`, `{}`, `{"data":null}`, `{"data":"invalid"}`, `{"data":[],"error":{"message":"fixture-secret"}}`,
		`{"data":[{"id":"a"}],"has_more":true}`, `{"data":[],"has_more":true,"last_id":"a"}`,
		`{"data":[{"id":"a"}],"has_more":"true"}`, `{"data":[{"id":"a"}],"has_more":true,"last_id":"bad\nvalue"}`,
	} {
		invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		models, err := service.ListVisionModels(context.Background(), VisionConfiguration{BaseURL: invalid.URL, Protocol: "anthropic"})
		invalid.Close()
		if err == nil || models != nil || strings.Contains(err.Error(), "fixture-secret") {
			return fmt.Errorf("invalid catalog accepted or error leaked")
		}
	}
	for _, mode := range []string{"second-fails", "page-limit", "cursor-encoding", "too-many", "empty"} {
		var calls atomic.Int32
		catalogServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := calls.Add(1)
			switch mode {
			case "second-fails":
				if call > 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
			case "cursor-encoding":
				if call > 1 {
					if r.URL.Query().Get("after_id") != "cursor&after_id=other +中文" || len(r.URL.Query()) != 1 {
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					_, _ = io.WriteString(w, `{"data":[{"id":"second"}],"has_more":false}`)
					return
				}
				_, _ = io.WriteString(w, `{"data":[{"id":"first"}],"has_more":true,"last_id":"cursor&after_id=other +中文"}`)
				return
			case "too-many":
				data := make([]map[string]string, 2001)
				for i := range data {
					data[i] = map[string]string{"id": fmt.Sprintf("model-%d", i)}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
				return
			case "empty":
				_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": fmt.Sprint(call)}}, "has_more": true, "last_id": fmt.Sprint(call)})
		}))
		models, err := service.ListVisionModels(context.Background(), VisionConfiguration{BaseURL: catalogServer.URL, Protocol: "anthropic"})
		catalogServer.Close()
		if mode == "cursor-encoding" || mode == "empty" {
			if err != nil || (mode == "cursor-encoding" && strings.Join(models, ",") != "first,second") || (mode == "empty" && len(models) != 0) {
				return fmt.Errorf("valid catalog %s failed: %v", mode, err)
			}
		} else if err == nil || models != nil {
			return fmt.Errorf("incomplete catalog %s reported success", mode)
		}
		if mode == "page-limit" && calls.Load() != 20 {
			return fmt.Errorf("pagination limit not enforced")
		}
	}
	catalogStarted := make(chan struct{})
	catalogCancelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after_id") == "" {
			_, _ = io.WriteString(w, `{"data":[{"id":"first"}],"has_more":true,"last_id":"first"}`)
			return
		}
		close(catalogStarted)
		<-r.Context().Done()
	}))
	defer catalogCancelServer.Close()
	catalogCtx, catalogCancel := context.WithCancel(context.Background())
	defer catalogCancel()
	catalogResult := make(chan error, 1)
	go func() {
		models, err := service.ListVisionModels(catalogCtx, VisionConfiguration{BaseURL: catalogCancelServer.URL, Protocol: "anthropic"})
		if models != nil {
			catalogResult <- fmt.Errorf("cancelled catalog returned partial models")
			return
		}
		catalogResult <- err
	}()
	select {
	case <-catalogStarted:
	case <-time.After(3 * time.Second):
		return fmt.Errorf("second catalog page not requested")
	}
	catalogCancel()
	select {
	case err := <-catalogResult:
		if err != context.Canceled {
			return fmt.Errorf("catalog cancellation not preserved: %v", err)
		}
	case <-time.After(3 * time.Second):
		return fmt.Errorf("catalog cancellation timed out")
	}
	started, cancelled := make(chan struct{}), make(chan struct{})
	cancelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer cancelServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := analyzeVisionImages(ctx, VisionConfiguration{BaseURL: cancelServer.URL, Model: "vision-id", Protocol: "chat"}, "", [][]byte{image}, visionClient())
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		return fmt.Errorf("cancel request not started")
	}
	cancel()
	select {
	case err := <-result:
		if err == nil {
			return fmt.Errorf("cancel ignored")
		}
	case <-time.After(3 * time.Second):
		return fmt.Errorf("cancel did not stop client")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		return fmt.Errorf("cancel did not reach HTTP server")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/v1/models", http.StatusFound)
	}))
	defer redirect.Close()
	before := requests.Load()
	if _, err := service.ListVisionModels(context.Background(), VisionConfiguration{BaseURL: redirect.URL, APIKey: "verify-secret"}); err == nil || before != requests.Load() {
		return fmt.Errorf("redirect followed")
	}
	fmt.Println(`{"ok":true,"vision":"three protocols, complete responses, attachment allowlist, limits, paginated model catalog, HTTP cancellation and redirects"}`)
	return nil
}

func verifyClaudePluginChild() {
	mode := os.Getenv("NICE_PLUGIN_VERIFY_MODE")
	args := os.Args[2:]
	cwd, _ := os.Getwd()
	if len(args) < 2 || args[0] != "plugin" {
		fail("unexpected plugin command")
	}
	if args[1] == "list" {
		if mode == "bad-list" {
			fmt.Print("broken")
			return
		}
		state, _ := os.ReadFile(filepath.Join(cwd, "plugin-state"))
		if mode == "bad-after" && string(state) == "true" {
			fmt.Print("broken")
			return
		}
		enabled := string(state) == "true"
		if mode == "override" {
			enabled = false
		}
		record := map[string]any{"id": "demo@market", "scope": "project", "projectPath": cwd, "installPath": filepath.Join(cwd, "installed"), "version": "1", "enabled": enabled}
		if mode == "load-error" && enabled {
			record["errors"] = []string{"marketplace not found"}
		}
		_ = json.NewEncoder(os.Stdout).Encode([]map[string]any{record})
		return
	}
	if args[1] == "details" {
		if len(args) != 3 || args[2] != "demo@market" {
			fail("invalid details arguments")
		}
		switch mode {
		case "details-failed":
			os.Exit(2)
		case "details-empty":
			fmt.Print("\x1b[0m  \n")
		case "details-large":
			fmt.Print(strings.Repeat("x", 128*1024+1))
		case "details-cancel":
			time.Sleep(30 * time.Second)
		default:
			fmt.Print("\x1b[32mSkills: demo\x1b[0m\nAlways-on: 120 tokens\n")
		}
		return
	}
	if len(args) != 5 || args[2] != "demo@market" || args[3] != "--scope" || args[4] != "project" {
		fail("invalid scoped arguments")
	}
	if mode == "command-failed" {
		os.Exit(2)
	}
	if mode == "cancel" {
		time.Sleep(30 * time.Second)
		return
	}
	enabled := "false"
	if args[1] == "enable" {
		enabled = "true"
	} else if args[1] != "disable" {
		fail("invalid action")
	}
	if err := os.WriteFile(filepath.Join(cwd, "plugin-state"), []byte(enabled), 0600); err != nil {
		fail("persist fixture: %v", err)
	}
	fmt.Print("OK")
}

func verifyClaudePlugins() error {
	root, err := os.MkdirTemp("", "nice-plugin-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "project 中文")
	other := filepath.Join(root, "other")
	for _, dir := range []string{home, workspace, other, filepath.Join(home, "plugins", "cache", "market", "stale")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	boolFalse, boolTrue := false, true
	records := []claudePluginRecord{
		{ID: "demo@market", Scope: "user", Enabled: &boolTrue, Version: "user-version"},
		{ID: "demo@market", Scope: "project", ProjectPath: workspace, Enabled: &boolFalse, Version: "project-version", FolderVersion: "source-version", HasUserConfig: true, MCPServers: map[string]json.RawMessage{"zeta": json.RawMessage(`{"env":{"TOKEN":"fixture-secret"}}`), "alpha": json.RawMessage(`{"url":"private-endpoint"}`)}, ReadFromFolder: filepath.Join(root, "source")},
		{ID: "demo@market", Scope: "local", ProjectPath: other, Enabled: &boolTrue},
		{ID: "policy@market", Scope: "managed", Enabled: &boolTrue, Errors: []string{"dependency unavailable"}},
	}
	raw, _ := json.Marshal(records)
	parsed, err := parseClaudePluginList(string(raw), workspace)
	if err != nil || len(parsed) != 3 {
		return fmt.Errorf("native scope filtering: %v / %d", err, len(parsed))
	}
	if parsed[0].Scope != "project" || parsed[0].Path != filepath.Join(root, "source") || parsed[1].Scope != "user" {
		return fmt.Errorf("separate scopes or in-place path lost")
	}
	viewJSON, _ := json.Marshal(parsed[0])
	if parsed[0].Version != "project-version" || parsed[0].FolderVersion != "source-version" || !parsed[0].HasUserConfig || strings.Join(parsed[0].MCPServerNames, ",") != "alpha,zeta" || strings.Contains(string(viewJSON), "fixture-secret") || strings.Contains(string(viewJSON), "private-endpoint") {
		return fmt.Errorf("plugin metadata or MCP privacy invalid")
	}
	for _, body := range []string{"null", "{}", "broken", `[{"id":"--all","scope":"user","enabled":true}]`} {
		if _, err := parseClaudePluginList(body, workspace); err == nil {
			return fmt.Errorf("accepted malformed list")
		}
	}
	for _, id := range []string{"", "--all", "a\nb", "a b", strings.Repeat("a", 221)} {
		if validClaudePluginID(id) {
			return fmt.Errorf("accepted unsafe ID")
		}
	}
	if got := listClaudePlugins(home, workspace); len(got) != 0 {
		return fmt.Errorf("cache mistaken for install")
	}
	localRaw, _ := json.Marshal(map[string]any{"plugins": map[string]any{"demo@market": records[:3]}})
	if err := os.WriteFile(filepath.Join(home, "plugins", "installed_plugins.json"), localRaw, 0600); err != nil {
		return err
	}
	local := listClaudePlugins(home, workspace)
	if len(local) != 2 || local[0].Enabled != nil || local[1].Enabled != nil {
		return fmt.Errorf("offline inventory state/scope invalid")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	service := &AppService{settings: defaultSettings(), agentProviders: []AgentProviderRuntime{{Kind: "claude", RuntimeReady: true, Executable: executable}}}
	service.settings.ClaudeWorkspace = workspace
	original := execCommandContext
	defer func() { execCommandContext = original }()
	mode := "normal"
	calls := 0
	execCommandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		calls++
		command := exec.CommandContext(ctx, executable, append([]string{"--claude-plugin-child"}, args...)...)
		command.Env = append(os.Environ(), "NICE_PLUGIN_VERIFY_MODE="+mode)
		return command
	}
	request := ClaudePluginToggleRequest{Name: "demo@market", Scope: "project", Workspace: workspace, Enabled: true}
	catalog, err := service.ReadClaudePluginCatalog(context.Background(), workspace)
	if err != nil || !catalog.Native || len(catalog.Plugins) != 1 {
		return fmt.Errorf("native catalog: %v", err)
	}
	result, err := service.SetClaudePluginEnabled(context.Background(), request)
	if err != nil || !result.Verified || result.Catalog.Plugins[0].Enabled == nil || !*result.Catalog.Plugins[0].Enabled {
		return fmt.Errorf("toggle verify: %v", err)
	}
	before := calls
	if _, err := service.SetClaudePluginEnabled(context.Background(), request); err != nil || calls-before != 1 {
		return fmt.Errorf("idempotent toggle: %v", err)
	}
	if service.providerReloading["claude"] {
		return fmt.Errorf("mutation guard leaked")
	}
	details, err := service.ReadClaudePluginDetails(context.Background(), "demo@market", workspace)
	if err != nil || details.Name != "demo@market" || details.Workspace != workspace || details.Report != "Skills: demo\nAlways-on: 120 tokens" {
		return fmt.Errorf("native details invalid: %v", err)
	}
	for _, nextMode := range []string{"details-failed", "details-empty", "details-large", "load-error", "override"} {
		mode = nextMode
		before := calls
		if _, err := service.ReadClaudePluginDetails(context.Background(), "demo@market", workspace); err == nil {
			return fmt.Errorf("invalid details accepted: %s", mode)
		}
		if (mode == "load-error" || mode == "override") && calls-before != 1 {
			return fmt.Errorf("unloaded plugin queried")
		}
	}
	mode = "normal"
	before = calls
	if _, err := service.ReadClaudePluginDetails(context.Background(), "--all", workspace); err == nil || calls != before {
		return fmt.Errorf("invalid details ID accepted")
	}
	if _, err := service.ReadClaudePluginDetails(context.Background(), "demo@market", other); err == nil || calls != before {
		return fmt.Errorf("stale details workspace accepted")
	}
	if _, err := service.ReadClaudePluginDetails(context.Background(), "missing@market", workspace); err == nil || calls-before != 1 {
		return fmt.Errorf("missing plugin details queried")
	}
	mode = "details-cancel"
	detailsCtx, detailsCancel := context.WithTimeout(context.Background(), time.Second)
	startedDetails := time.Now()
	_, err = service.ReadClaudePluginDetails(detailsCtx, "demo@market", workspace)
	detailsCancel()
	if err == nil || time.Since(startedDetails) > 5*time.Second {
		return fmt.Errorf("details cancellation ignored")
	}
	state, err := os.ReadFile(filepath.Join(workspace, "plugin-state"))
	if err != nil || string(state) != "true" {
		return fmt.Errorf("details changed enablement")
	}
	mode = "normal"
	for _, bad := range []ClaudePluginToggleRequest{
		{Name: "--all", Scope: "user", Workspace: workspace},
		{Name: "demo@market", Scope: "managed", Workspace: workspace},
		{Name: "demo@market", Scope: "project", Workspace: other},
	} {
		before := calls
		if _, err := service.SetClaudePluginEnabled(context.Background(), bad); err == nil || calls != before {
			return fmt.Errorf("unsafe scope/name passed")
		}
	}
	service.externalRuns = map[string]*externalRun{"claude:active": {}}
	before = calls
	if _, err := service.SetClaudePluginEnabled(context.Background(), request); err == nil || calls != before {
		return fmt.Errorf("active turn not guarded")
	}
	service.externalRuns = nil
	service.providerReloading["claude"] = true
	if _, err := service.SetClaudePluginEnabled(context.Background(), request); err == nil {
		return fmt.Errorf("concurrent mutation not guarded")
	}
	delete(service.providerReloading, "claude")
	for _, nextMode := range []string{"override", "bad-after", "load-error", "command-failed", "bad-list"} {
		mode = nextMode
		if err := os.WriteFile(filepath.Join(workspace, "plugin-state"), []byte("false"), 0600); err != nil {
			return err
		}
		result, err := service.SetClaudePluginEnabled(context.Background(), request)
		if (nextMode == "override" || nextMode == "bad-after" || nextMode == "load-error") && (err != nil || result.Verified || result.Message == "") {
			return fmt.Errorf("unverified save incorrectly accepted: %s", nextMode)
		}
		if (nextMode == "command-failed" || nextMode == "bad-list") && err == nil {
			return fmt.Errorf("failed CLI accepted: %s", nextMode)
		}
		if service.providerReloading["claude"] {
			return fmt.Errorf("guard leaked after %s", nextMode)
		}
	}
	// Cancellation must stop the managed child without blocking future sends.
	mode = "cancel"
	if err := os.WriteFile(filepath.Join(workspace, "plugin-state"), []byte("false"), 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	if _, err := service.SetClaudePluginEnabled(ctx, request); err == nil || time.Since(started) > 5*time.Second {
		return fmt.Errorf("child cancellation ignored")
	}
	if service.providerReloading["claude"] {
		return fmt.Errorf("guard leaked after cancellation")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "claudePlugins": []string{"native scopes", "offline cache exclusion", "CLI enablement", "idempotency", "override detection", "malformed responses", "active/concurrent guards", "cancellation", "native details", "details errors and cancellation", "MCP metadata privacy", "installed and loaded versions"}})
}

// Isolated JSON-RPC fixture exercises the real transport and service boundary.
// It never starts a model request or reads a user's Codex home.
func verifyCodexProviderChild() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("codex-cli 0.169.0")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "debug" {
		fmt.Println(`{"models":[{"slug":"gpt-6-astra"}]}`)
		return
	}
	decoder, encoder := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if decoder.Decode(&request) != nil {
			return
		}
		if request.ID == nil {
			continue
		}
		result := map[string]any{}
		response := map[string]any{"id": request.ID, "result": result}
		providerError := func(kind string) {
			delete(response, "result")
			response["error"] = map[string]any{"code": -32600, "message": "Route unavailable", "data": map[string]any{"type": kind, "sensitiveDetail": "must-not-cross-bridge"}}
		}
		switch request.Method {
		case "initialize":
			result["userAgent"] = "provider-verifier"
		case "config/read":
			result["config"] = map[string]any{"model_provider": "current-route", "model": "current-model", "model_reasoning_effort": "medium"}
		case "thread/resume":
			if _, overridden := request.Params["modelProvider"]; overridden {
				fail("resume overrode saved provider")
			}
			id := firstMapString(request.Params, "threadId")
			if strings.HasPrefix(id, "blocked-") {
				providerError(strings.TrimPrefix(id, "blocked-"))
			} else {
				result["modelProvider"] = "saved-route"
				result["thread"] = map[string]any{"id": id, "modelProvider": "saved-route", "cwd": os.Getenv("NICE_CODEX_PROVIDER_VERIFY_ROOT")}
			}
		case "thread/read":
			result["thread"] = map[string]any{"id": request.Params["threadId"], "modelProvider": "saved-route", "cwd": os.Getenv("NICE_CODEX_PROVIDER_VERIFY_ROOT")}
		case "model/list":
			if request.Params["modelProvider"] != "saved-route" {
				fail("model catalog used the default route instead of the saved provider")
			}
			if request.Params["cursor"] == "second" {
				result["data"] = []any{map[string]any{"model": "saved-model-2"}}
			} else {
				result["data"] = []any{map[string]any{"model": "saved-model"}}
				result["nextCursor"] = "second"
			}
		case "thread/fork":
			if request.Params["modelProvider"] != "current-route" || request.Params["model"] != "current-model" {
				fail("fork used stale configuration")
			}
			result["model"], result["modelProvider"] = "current-model", "current-route"
			result["thread"] = map[string]any{"id": "fork-native", "modelProvider": "current-route", "turns": []any{map[string]any{"id": "historic-turn"}}}
		case "thread/start":
			result["model"], result["modelProvider"] = "current-model", "current-route"
			result["thread"] = map[string]any{"id": "start-native", "modelProvider": "current-route"}
		default:
			result["goal"] = nil
		}
		if encoder.Encode(response) != nil {
			return
		}
	}
}

func verifyCodexProviderIdentity() error {
	for _, catalog := range []map[string]any{
		{"data": []any{}, "nextCursor": "repeated"},
		{"data": []any{}, "nextCursor": 123},
		{"data": "invalid"},
	} {
		if _, err := listCodexModelPages("saved-route", func(string, any) (map[string]any, error) { return catalog, nil }); err == nil {
			return fmt.Errorf("invalid model catalog accepted")
		}
	}
	root, err := os.MkdirTemp("", "nicecodex-provider-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	for key, value := range map[string]string{"CODEX_BIN": executable, "CODEX_HOME": root, "NICE_CODEX_PROVIDER_VERIFY_CHILD": "1", "NICE_CODEX_PROVIDER_VERIFY_ROOT": root} {
		previous, existed := os.LookupEnv(key)
		defer func() {
			if existed {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		}()
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	service := &AppService{settings: defaultSettings(), settingsPath: filepath.Join(root, "settings.json"), sessions: map[string]*SessionRecord{}, allowedThreads: map[string]string{}}
	service.settings.Workspace = root
	service.client = codex.NewClient(nil)
	if err := service.client.Start(context.Background(), root, codex.ClientInfo{}); err != nil {
		return err
	}
	defer service.client.Stop()
	source := &SessionRecord{ID: "source", BackendRef: "saved-native", Workspace: root, ProviderID: "stale-route", Model: "saved-model", Effort: "high", Name: "Original"}
	service.sessions[source.ID] = source
	catalog, err := service.ListThreadModels(source.ID)
	if err != nil || catalog["modelProvider"] != "saved-route" || len(catalog["data"].([]any)) != 2 || source.ProviderID != "stale-route" {
		return fmt.Errorf("thread catalog provider/pagination/read-only identity: %v", err)
	}
	response, err := service.ResumeThread(source.ID)
	if err != nil || response["modelProvider"] != "saved-route" || source.ProviderID != "saved-route" {
		return fmt.Errorf("resume identity repair: %v", err)
	}
	other := "different-route"
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: source.ID, ModelProvider: &other, Model: "wrong-model"}); !codexProviderAdmissionError(err) || source.Model != "saved-model" || source.ProviderID != "saved-route" {
		return fmt.Errorf("provider mutation was not atomic: %v", err)
	}
	for _, kind := range []string{"modelProviderMismatch", "modelProviderNotConfigured", "modelProviderPolicyConflict"} {
		source.BackendRef = "blocked-" + kind
		_, err := service.ResumeThread(source.ID)
		if !codexProviderAdmissionError(err) || strings.Contains(err.Error(), "must-not-cross-bridge") {
			return fmt.Errorf("structured provider error lost/leaked: %v", err)
		}
	}
	source.BackendRef = "saved-native"
	response, err = service.ForkThread(source.ID)
	if err != nil {
		return err
	}
	forkID := threadIDFromResult(response)
	fork := service.sessions[forkID]
	if fork == nil || forkID == source.ID || fork.ProviderID != "current-route" || fork.Model != "current-model" || fork.Effort != "medium" || source.ProviderID != "saved-route" || source.Model != "saved-model" {
		return fmt.Errorf("fork identity or source changed")
	}
	thread, _ := response["thread"].(map[string]any)
	if thread["backendRef"] != "fork-native" || len(thread["turns"].([]any)) != 1 {
		return fmt.Errorf("fork history/identity lost")
	}
	draft := &SessionRecord{ID: "draft", Workspace: root}
	service.sessions[draft.ID] = draft
	if _, err := service.ListThreadModels(draft.ID); err == nil {
		return fmt.Errorf("unassigned session used default provider catalog")
	}
	if _, err := service.ensureCodexBackendThread(draft, service.Settings(), root); err != nil || draft.ProviderID != "current-route" || draft.Model != "current-model" {
		return fmt.Errorf("start identity not persisted: %v", err)
	}
	if codexProviderAdmissionError(fmt.Errorf("permission denied reading a file")) {
		return fmt.Errorf("unrelated error misclassified")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"thread provider catalog pagination", "invalid/repeated catalog cursor", "unassigned provider guard", "resume saved provider", "repair stale identity", "atomic provider guard", "structured admission errors", "error data redaction", "fork current native config", "source/history preservation", "start identity persistence"}})
}

func verifyCapabilityContracts() error {
	page := 0
	result, err := listExperimentalFeaturePages(func(params map[string]any) (map[string]any, error) {
		if page > 0 && params["cursor"] != "second" {
			return nil, fmt.Errorf("cursor was not forwarded")
		}
		page++
		if page == 1 {
			return map[string]any{"data": []any{map[string]any{"name": "first"}}, "nextCursor": "second"}, nil
		}
		return map[string]any{"data": []any{map[string]any{"name": "computer_use", "enabled": false}}}, nil
	})
	if err != nil || len(result["data"].([]any)) != 2 || page != 2 {
		return fmt.Errorf("feature pagination failed: %v", err)
	}
	if _, err := listExperimentalFeaturePages(func(map[string]any) (map[string]any, error) {
		return map[string]any{"data": []any{}, "nextCursor": "repeated"}, nil
	}); err == nil {
		return fmt.Errorf("repeated cursor accepted")
	}
	if _, err := listExperimentalFeaturePages(func(map[string]any) (map[string]any, error) {
		return nil, fmt.Errorf("offline")
	}); err == nil {
		return fmt.Errorf("feature read failure hidden")
	}
	if err := verifyFeatureEnablement(map[string]any{"enablement": map[string]any{}}, "computer_use", true); err == nil {
		return fmt.Errorf("ignored feature mutation reported success")
	}
	if err := verifyFeatureEnablement(map[string]any{"enablement": map[string]any{"memories": false}}, "memories", true); err == nil {
		return fmt.Errorf("opposite feature value accepted")
	}
	if err := verifyFeatureEnablement(map[string]any{"enablement": map[string]any{"memories": false}}, "memories", false); err != nil {
		return err
	}
	pluginID := "computer.tools@market.place"
	edit, err := pluginEnablementEdit(pluginID, false)
	if err != nil || edit["keyPath"] != "plugins" || edit["mergeStrategy"] != "upsert" {
		return fmt.Errorf("plugin mutation not a table merge: %v", err)
	}
	value := edit["value"].(map[string]any)
	if len(value) != 1 || value[pluginID].(map[string]any)["enabled"] != false {
		return fmt.Errorf("plugin ID or disabled state lost")
	}
	for _, invalid := range []string{"", "\n", "bad\x00id", strings.Repeat("x", 221)} {
		if _, err := pluginEnablementEdit(invalid, true); err == nil {
			return fmt.Errorf("invalid plugin ID accepted")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"feature pagination", "repeated cursor and read errors", "native enablement acknowledgement", "literal plugin ID and merge payload"}})
}

func verifyClaudeHealth() error {
	cases := []struct {
		name, executable, version, auth, state string
		versionErr, authErr                    error
		available, authenticated               bool
		calls                                  int
	}{
		{name: "missing", state: "not-installed"},
		{name: "version failure", executable: "claude", versionErr: fmt.Errorf("failed"), state: "configuration-error", calls: 1},
		{name: "empty version", executable: "claude", state: "configuration-error", calls: 1},
		{name: "non-version output", executable: "claude", version: "Please sign in to continue", state: "configuration-error", calls: 1},
		{name: "valid", executable: "claude", version: "2.1.278 (Claude Code)", auth: "{\n\t\"loggedIn\": true\n}", state: "ready", available: true, authenticated: true, calls: 2},
		{name: "alternate boolean", executable: "claude", version: "2.1.278", auth: `{"authenticated":true}`, state: "ready", available: true, authenticated: true, calls: 2},
		{name: "signed out exit 1", executable: "claude", version: "2.1.278", auth: `{"loggedIn":false}`, authErr: fmt.Errorf("exit 1"), state: "authentication-required", available: true, calls: 2},
		{name: "unsupported auth command", executable: "claude", version: "2.1.278", auth: "unknown command", authErr: fmt.Errorf("exit 1"), state: "authentication-unknown", available: true, calls: 2},
		{name: "invalid JSON", executable: "claude", version: "2.1.278", auth: `garbage "loggedIn":true`, state: "authentication-unknown", available: true, calls: 2},
		{name: "string boolean", executable: "claude", version: "2.1.278", auth: `{"loggedIn":"true"}`, state: "authentication-unknown", available: true, calls: 2},
		{name: "missing auth field", executable: "claude", version: "2.1.278", auth: `{}`, state: "authentication-unknown", available: true, calls: 2},
		{name: "true with command error", executable: "claude", version: "2.1.278", auth: `{"loggedIn":true}`, authErr: fmt.Errorf("timeout"), state: "authentication-unknown", available: true, calls: 2},
		{name: "conflicting legacy field", executable: "claude", version: "2.1.278", auth: `{"loggedIn":false,"authenticated":true}`, state: "authentication-required", available: true, calls: 2},
	}
	for _, item := range cases {
		calls := 0
		status := probeClaudeRuntime(item.executable, func(_ string, args []string, _ time.Duration) (string, error) {
			calls++
			if len(args) == 1 && args[0] == "--version" {
				return item.version, item.versionErr
			}
			return item.auth, item.authErr
		})
		if status.Status != item.state || status.Available != item.available || status.Authenticated != item.authenticated || calls != item.calls {
			return fmt.Errorf("%s: unexpected status %+v (%d calls)", item.name, status, calls)
		}
		provider := AgentProviderRuntime{Installed: true, Healthy: true, RuntimeReady: true, Status: "ready"}
		applyClaudeRuntimeStatus(&provider, status)
		if provider.Installed != (item.executable != "") || provider.Healthy != item.available || provider.Status != item.state {
			return fmt.Errorf("%s: provider badge differs from runtime", item.name)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "claudeHealthCases": len(cases)})
}

func verifyCLIDiscovery() error {
	for _, alias := range []string{"default", "best", "opusplan", "sonnet[1m]", "haiku", "claude-haiku-5-5"} {
		if args := appendClaudeCompatibilityArgs([]string{"-p", "hello"}, alias); len(args) != 2 {
			return fmt.Errorf("native Claude alias %s lost native tools", alias)
		}
	}
	for _, model := range []string{"claude-haiku-5-5", "claude-fable-5-2"} {
		if knownProviderContextWindow("claude", model) != 1_000_000 {
			return fmt.Errorf("wrong context: %s", model)
		}
	}
	settings := defaultSettings()
	settings.Model, settings.Effort = "Orchestrator (Carbon)", "high"
	args, _ := externalCommandArgsForExecutable("gemini", "agy.exe", "", "", settings, "hello")
	if strings.Contains(strings.Join(args, " "), "--effort") {
		return fmt.Errorf("Orchestrator received effort")
	}
	settings.Model, settings.Effort = "gemini-4-hydrogen-xhigh", "high"
	args, _ = externalCommandArgsForExecutable("gemini", "agy.exe", "", "", settings, "hello")
	if !strings.Contains(strings.Join(args, " "), "--effort xhigh") {
		return fmt.Errorf("Extra effort lost")
	}
	if len(fallbackProviderModels("opencode")) != 0 {
		return fmt.Errorf("OpenCode fallback invents a connected provider")
	}
	if runtime.GOOS == "windows" {
		root, err := os.MkdirTemp("", "nicecodex-discovery-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
		for _, key := range []string{"PATH", "CODEX_BIN", "FNM_DIR", "NVM_HOME", "NICE_CODEX_NODE_ROOT", "NICE_CODEX_NODE_PARENT", "NICE_CODEX_CYCLE_A", "NICE_CODEX_CYCLE_B", "NICE_CODEX_VERIFY_CHILD", "NICE_CODEX_PROVIDER_VERIFY_CHILD", "NICE_CODEX_PROVIDER_VERIFY_ROOT"} {
			value, exists := os.LookupEnv(key)
			defer func(key, value string, exists bool) {
				if exists {
					_ = os.Setenv(key, value)
				} else {
					_ = os.Unsetenv(key)
				}
			}(key, value, exists)
		}
		_ = os.Setenv("NICE_CODEX_VERIFY_CHILD", "1")
		_ = os.Unsetenv("CODEX_BIN")
		self, err := os.Executable()
		if err != nil {
			return err
		}
		binary, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		fnmRoot, nvmRoot := filepath.Join(root, "fnm 中文"), filepath.Join(root, "nvm versions")
		fnmDefault := filepath.Join(fnmRoot, "aliases", "default")
		quotedBin := filepath.Join(root, "quoted bin")
		for _, dir := range []string{
			fnmDefault, quotedBin,
			filepath.Join(fnmRoot, "node-versions", "v22.10.0", "installation"),
			filepath.Join(fnmRoot, "node-versions", "v22.9.0", "installation"),
			filepath.Join(fnmRoot, "node-versions", ".downloads", "installation"),
			filepath.Join(nvmRoot, "v22.10.0"), filepath.Join(nvmRoot, "v9.11.0"),
			filepath.Join(nvmRoot, "v9999999999999999999999999.0.0"),
		} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		_ = os.Setenv("NICE_CODEX_NODE_ROOT", root)
		_ = os.Setenv("NICE_CODEX_NODE_PARENT", "%NICE_CODEX_NODE_ROOT%")
		_ = os.Setenv("FNM_DIR", `%NICE_CODEX_NODE_PARENT%\fnm 中文`)
		_ = os.Setenv("NVM_HOME", nvmRoot)
		_ = os.Setenv("NICE_CODEX_CYCLE_A", "%NICE_CODEX_CYCLE_B%")
		_ = os.Setenv("NICE_CODEX_CYCLE_B", "%NICE_CODEX_CYCLE_A%")
		_ = os.Setenv("PATH", root+`;"%NICE_CODEX_NODE_PARENT%\quoted bin";%NICE_CODEX_CYCLE_A%`)
		codex.EnrichPathForLookups()
		managerPaths := filepath.SplitList(os.Getenv("PATH"))
		pathIndex := func(path string) int {
			for index, candidate := range managerPaths {
				if strings.EqualFold(candidate, path) {
					return index
				}
			}
			return -1
		}
		for _, paths := range [][]string{
			{root, quotedBin, fnmDefault, filepath.Join(fnmRoot, "node-versions", "v22.10.0", "installation"), filepath.Join(fnmRoot, "node-versions", "v22.9.0", "installation")},
			{filepath.Join(nvmRoot, "v22.10.0"), filepath.Join(nvmRoot, "v9.11.0")},
		} {
			previous := -1
			for _, path := range paths {
				index := pathIndex(path)
				if index <= previous {
					return fmt.Errorf("manager installation missing or incorrectly ordered: %s", path)
				}
				previous = index
			}
		}
		for _, path := range []string{filepath.Join(fnmRoot, "node-versions", ".downloads", "installation"), filepath.Join(nvmRoot, "v9999999999999999999999999.0.0")} {
			if pathIndex(path) >= 0 {
				return fmt.Errorf("non-installation directory added to PATH: %s", path)
			}
		}
		for _, dir := range []string{fnmDefault, filepath.Join(nvmRoot, "v22.10.0")} {
			path := filepath.Join(managerPaths[pathIndex(dir)], "codex.exe")
			if err := os.WriteFile(path, binary, 0o700); err != nil {
				return err
			}
			_ = os.Setenv("PATH", dir)
			if detection := codex.Detect(); !detection.Available || detection.Binary != path {
				return fmt.Errorf("discovered manager CLI was not runnable: %+v", detection)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		first, second := filepath.Join(root, "首选 CLI"), filepath.Join(root, "second")
		for _, dir := range []string{filepath.Join(first, "bin"), second} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		preferred := filepath.Join(first, "bin", "codex.exe")
		fallback := filepath.Join(second, "codex.exe")
		for _, path := range []string{preferred, fallback} {
			if err := os.WriteFile(path, binary, 0o700); err != nil {
				return err
			}
		}
		shim := filepath.Join(first, "codex.cmd")
		if err := os.WriteFile(shim, []byte("@\"%~dp0bin\\codex.exe\" %*\r\n"), 0o600); err != nil {
			return err
		}
		_ = os.Setenv("PATH", first+string(os.PathListSeparator)+second)
		for i := 0; i < 3; i++ {
			detection := codex.Detect()
			if !detection.Available || detection.Binary != preferred {
				return fmt.Errorf("PATH/shim priority changed: %+v", detection)
			}
		}
		if err := os.Remove(shim); err != nil {
			return err
		}
		shim = filepath.Join(first, "codex.ps1")
		if err := os.WriteFile(shim, []byte("& \"$basedir/bin/codex.exe\" $args"), 0o600); err != nil {
			return err
		}
		_ = os.Setenv("CODEX_BIN", shim)
		if detection := codex.Detect(); !detection.Available || detection.Binary != preferred {
			return fmt.Errorf("PowerShell override failed: %+v", detection)
		}
		_ = os.Unsetenv("CODEX_BIN")
		if err := os.Remove(shim); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(first, "codex.exe"), []byte("placeholder"), 0o600); err != nil {
			return err
		}
		if detection := codex.Detect(); !detection.Available || detection.Binary != fallback {
			return fmt.Errorf("broken placeholder masks working CLI: %+v", detection)
		}
		if err := os.WriteFile(filepath.Join(first, "codex.exe"), []byte("MZ damaged executable"), 0o600); err != nil {
			return err
		}
		if detection := codex.Detect(); !detection.Available || detection.Binary != fallback {
			return fmt.Errorf("damaged executable masks working CLI: %+v", detection)
		}
		brokenDir := filepath.Join(root, "broken-start")
		bundleDir := filepath.Join(root, "vendor", "x86_64-pc-windows-msvc", "bin")
		for _, dir := range []string{brokenDir, filepath.Join(root, "empty-version"), bundleDir} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "codex.exe"), binary, 0o700); err != nil {
				return err
			}
			_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+second)
			if detection := codex.Detect(); !detection.Available || detection.Binary != fallback {
				return fmt.Errorf("unhealthy installation masks working CLI: %+v", detection)
			}
			_ = os.Setenv("CODEX_BIN", filepath.Join(dir, "codex.exe"))
			if detection := codex.Detect(); detection.Available || detection.Binary != filepath.Join(dir, "codex.exe") || detection.Error == "" {
				return fmt.Errorf("explicit unhealthy installation ignored or diagnostic lost: %+v", detection)
			}
			_ = os.Unsetenv("CODEX_BIN")
		}
		_ = os.Setenv("NICE_CODEX_PROVIDER_VERIFY_CHILD", "1")
		_ = os.Setenv("NICE_CODEX_PROVIDER_VERIFY_ROOT", root)
		client := codex.NewClient(nil)
		if err := client.Start(context.Background(), root, codex.ClientInfo{}); err != nil {
			client.Stop()
			return fmt.Errorf("startup did not use healthy fallback: %w", err)
		}
		status := client.Status()
		client.Stop()
		if status.Binary != fallback || !status.Running {
			return fmt.Errorf("startup and detection disagree: %+v", status)
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := client.Start(cancelled, root, codex.ClientInfo{}); err == nil || client.Status().Running {
			client.Stop()
			return fmt.Errorf("cancelled discovery still started app-server")
		}
		_ = os.Unsetenv("NICE_CODEX_PROVIDER_VERIFY_CHILD")
		_ = os.Setenv("CODEX_BIN", filepath.Join(root, "missing.exe"))
		if codex.Detect().Available {
			return fmt.Errorf("invalid explicit override silently ignored")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"PATH order and repeated discovery", "fnm/nvm paths and numeric version ordering", "nested/cyclic Windows environment expansion", "Windows cmd/PowerShell shims and Unicode paths", "broken placeholder and damaged PE fallback", "failed/empty version and incomplete bundle fallback", "startup uses detected fallback", "cancelled startup", "explicit override errors and diagnostics", "Claude native aliases", "Claude model context", "Antigravity Extra and Orchestrator", "OpenCode native default"}})
}

// Isolated contract checks: no real Codex configuration, package installation or MCP server.
func verifyFastCtxIntegration() error {
	passing := "[PASS] Applied state: matched\n[PASS] Installed binary: ready\n[PASS] MCP server contract: ready\n[PASS] AGENTS guidance: current\n[INFO] fastshell: fastshell is disabled.\n"
	cases := []struct {
		name, output, version, managed, want string
		shell                                bool
		err                                  error
	}{
		{name: "applied", output: passing, version: "0.2.6", managed: "0.2.6", want: "applied"},
		{name: "zero exit before apply", output: "[INFO] Applied state: not applied", want: "not_applied"},
		{name: "updated launcher with old managed copy", output: passing, version: "0.2.6", managed: "0.2.5", want: "needs_apply"},
		{name: "unknown future report", output: "All good", want: "needs_attention"},
		{name: "damaged guidance", output: strings.ReplaceAll(passing, "[PASS] AGENTS", "[FAIL] AGENTS"), want: "needs_attention"},
		{name: "timeout", output: passing, err: fmt.Errorf("deadline"), want: "needs_attention"},
		{name: "shell enable pending", output: passing, version: "0.2.6", managed: "0.2.6", shell: true, want: "needs_apply"},
		{name: "shell disable pending", output: strings.ReplaceAll(passing, "fastshell is disabled.", "fastshell is still applied"), version: "0.2.6", managed: "0.2.6", want: "needs_apply"},
		{name: "shell applied", output: strings.ReplaceAll(passing, "[INFO] fastshell: fastshell is disabled.", "[PASS] fastshell: enabled"), version: "0.2.6", managed: "0.2.6", shell: true, want: "applied"},
	}
	for _, item := range cases {
		if got := fastCtxApplicationState(item.output, item.err, item.version, item.managed, item.shell); got != item.want {
			return fmt.Errorf("%s: got %s, want %s", item.name, got, item.want)
		}
	}
	root, err := os.MkdirTemp("", "nicecodex-fastctx-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	path := filepath.Join(root, "config.toml")
	original := "# user preferences\ntier = \"high\"\n[fastshell] # keep this\nenabled = false # keep note\nmax_running_jobs = 8\n[search]\nmax_cpu_cores = 2\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		return err
	}
	for _, enabled := range []bool{true, true, false} {
		if err := updateProviderTextConfig(path, func(text string) (string, error) {
			return upsertTOMLBool(text, "fastshell", "enabled", enabled), nil
		}); err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if readTOMLBool(string(content), "fastshell", "enabled", !enabled) != enabled {
			return fmt.Errorf("shell preference did not persist")
		}
		for _, preserved := range []string{"# user preferences", "tier = \"high\"", "# keep note", "max_running_jobs = 8", "[search]\nmax_cpu_cores = 2"} {
			if !strings.Contains(string(content), preserved) {
				return fmt.Errorf("lost user setting: %s", preserved)
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	service := &AppService{}
	profile := filepath.Join(root, "Codex 中文 path")
	output, err := service.runFastCtxCommand(executable, []string{"--fastctx-command", "status", "--codex-home", profile}, 5*time.Second, nil)
	if err != nil {
		return err
	}
	var command struct {
		Args    []string `json:"args"`
		Updates string   `json:"updates"`
	}
	if err := json.Unmarshal([]byte(output), &command); err != nil {
		return err
	}
	if len(command.Args) != 3 || command.Args[2] != profile || command.Updates != "1" {
		return fmt.Errorf("command arguments or child environment not preserved: %s", output)
	}
	fastCtxMu.Lock()
	_, checkErr := service.CheckFastCtx(false)
	_, syncErr := service.SyncFastCtx(true, false)
	fastCtxMu.Unlock()
	if checkErr == nil || syncErr == nil {
		return fmt.Errorf("concurrent operations were not rejected")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "statusCases": len(cases), "checks": []string{"atomic shell preferences preserve user settings", "Unicode/space profile argument and child environment", "concurrent operation guards"}})
}

func summarizeAgents(providers []AgentProviderRuntime) []map[string]any {
	out := make([]map[string]any, 0, len(providers))
	for _, provider := range providers {
		models := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			label := model.DisplayName
			if label == "" {
				label = model.Model
			}
			models = append(models, label)
		}
		out = append(out, map[string]any{
			"kind": provider.Kind, "name": provider.Name, "ready": provider.RuntimeReady,
			"status": provider.Status, "models": models,
		})
	}
	return out
}

func verifyRuntimeReload() error {
	root, err := os.MkdirTemp("", "nicecodex-reload-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	for key, value := range map[string]string{"CLAUDE_CONFIG_DIR": root, "ANTHROPIC_MODEL": ""} {
		previous, existed := os.LookupEnv(key)
		defer func() {
			if existed {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		}()
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	path := filepath.Join(root, "settings.json")
	for _, item := range []struct{ config, model string }{
		{`{"model":"gateway-model","env":{"ANTHROPIC_API_KEY":"fixture"}}`, "gateway-model"},
		{`{}`, "default"},
		{`{"model":"claude-sonnet-5-5"}`, "claude-sonnet-5-5"},
		{`{"model":"claude-sonnet-5-5","env":{"ANTHROPIC_MODEL":"claude-opus-5-5"}}`, "claude-opus-5-5"},
	} {
		if err := os.WriteFile(path, []byte(item.config), 0o600); err != nil {
			return err
		}
		got, err := claudeModelForReload("")
		if err != nil || got != item.model {
			return fmt.Errorf("native model reload: got %s, want %s: %v", got, item.model, err)
		}
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		return err
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".claude"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "settings.local.json"), []byte(`{"model":"claude-opus-5-5"}`), 0o600); err != nil {
		return err
	}
	if got, err := claudeModelForReload(workspace); err != nil || got != "claude-opus-5-5" {
		return fmt.Errorf("project model precedence: %s %v", got, err)
	}
	seen := map[string]bool{}
	for _, model := range discoverClaudeModels(root) {
		if seen[model.Model] {
			return fmt.Errorf("duplicate model: %s", model.Model)
		}
		seen[model.Model] = true
		if model.Model == "claude-opus-5-5" && (model.DisplayName != "Claude Opus 5.5" || model.ContextWindow != 1_000_000) {
			return fmt.Errorf("incorrect Opus metadata")
		}
		if model.Model == "claude-sonnet-5-5" && (model.DisplayName != "Claude Sonnet 5.5" || model.ContextWindow != 1_000_000) {
			return fmt.Errorf("incorrect Sonnet metadata")
		}
	}
	for _, model := range []string{"default", "sonnet", "opus", "claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1"} {
		if !seen[model] {
			return fmt.Errorf("missing model: %s", model)
		}
	}
	for _, provider := range []string{"codex", "claude", "grok", "gemini", "opencode"} {
		service := &AppService{providerReloading: map[string]bool{provider: true}}
		if _, err := service.ReloadRuntimeConfiguration(provider); err == nil {
			return fmt.Errorf("duplicate reload allowed: %s", provider)
		}
		service.providerReloading = nil
		if provider == "codex" {
			service.codexPendingDispatches = map[string]bool{"pending": true}
		} else {
			key := provider + ":active"
			service.externalRuns = map[string]*externalRun{key: {turnID: "active"}}
			service.sessions = map[string]*SessionRecord{key: {Provider: provider}}
		}
		if _, err := service.ReloadRuntimeConfiguration(provider); err == nil {
			return fmt.Errorf("active work was not protected: %s", provider)
		}
	}
	service := &AppService{providerReloading: map[string]bool{"codex": true}}
	if service.claimCodexDispatch("new-thread") {
		return fmt.Errorf("dispatch during Codex restart allowed")
	}
	if err := os.WriteFile(path, []byte(`{invalid`), 0o600); err != nil {
		return err
	}
	if _, err := service.ReloadRuntimeConfiguration("claude"); err == nil {
		return fmt.Errorf("invalid configuration accepted")
	}
	if service.providerReloading["claude"] {
		return fmt.Errorf("failed reload left provider locked")
	}
	service.settingsPath = filepath.Join(root, "app-settings.json")
	service.sessions = map[string]*SessionRecord{
		"codex-fixture":  {ID: "codex-fixture", Provider: "codex", ProviderID: "old-gateway", Model: "old-model"},
		"gemini-fixture": {ID: "gemini-fixture", Provider: "gemini", ProviderID: "__gemini__", Model: "old-model"},
	}
	provider := "openai"
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: "codex-fixture", Model: "gpt-5.4", ModelProvider: &provider, ResetModel: true}); err != nil {
		return err
	}
	if record := service.sessions["codex-fixture"]; record.ProviderID != "openai" || record.Model != "gpt-5.4" {
		return fmt.Errorf("old gateway survived Codex reload")
	}
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: "gemini-fixture", ModelProvider: &provider}); err == nil {
		return fmt.Errorf("external session could be converted into Codex")
	}
	if err := service.UpdateSessionPreferences(SessionPreferencesRequest{SessionID: "gemini-fixture", ResetModel: true}); err != nil {
		return err
	}
	if service.sessions["gemini-fixture"].Model != "" {
		return fmt.Errorf("native default did not clear old model")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"API model to native account default", "fresh environment and project model", "official 5.5 IDs and 1M metadata", "five-provider active/repeated reload guards", "Codex dispatch guard", "failed reload cleanup"}})
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

// Validate catalog identity and context settings with isolated configuration.
// No native CLI, network request, or user configuration is used.
func verifyModelConfiguration() error {
	root, err := os.MkdirTemp("", "nicecodex-models-")
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(root), os.TempDir()) || !strings.HasPrefix(filepath.Base(root), "nicecodex-models-") {
		return fmt.Errorf("unexpected verification directory")
	}
	defer os.RemoveAll(root)
	for key, value := range map[string]string{
		"CLAUDE_CONFIG_DIR": root, "ANTHROPIC_MODEL": "", "CLAUDE_CODE_DISABLE_1M_CONTEXT": "",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "", "ANTHROPIC_DEFAULT_OPUS_MODEL": "",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "", "ANTHROPIC_DEFAULT_FABLE_MODEL": "",
	} {
		previous, exists := os.LookupEnv(key)
		defer func() {
			if exists {
				_ = os.Setenv(key, previous)
			} else {
				_ = os.Unsetenv(key)
			}
		}()
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	fixture := `{"model":"claude-sonnet-4-6[1m]","availableModels":["claude-sonnet-4-5","claude-sonnet-4-6","claude-sonnet-4-6[1m]","gateway-claude-a","gateway-claude-b","gpt-proxy"],"modelOverrides":{"team-model":"claude-opus-4-6"}}`
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(fixture), 0o600); err != nil {
		return err
	}
	models := discoverClaudeModels(root)
	seen := map[string]bool{}
	defaults := 0
	for _, model := range models {
		if seen[model.Model] {
			return fmt.Errorf("duplicate model %q", model.Model)
		}
		seen[model.Model] = true
		if model.IsDefault {
			defaults++
			if model.Model != "claude-sonnet-4-6[1m]" {
				return fmt.Errorf("default model was collapsed to %q", model.Model)
			}
		}
	}
	for _, model := range []string{"sonnet", "opus", "claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-4-6[1m]", "gateway-claude-a", "gateway-claude-b", "gpt-proxy", "team-model"} {
		if !seen[model] {
			return fmt.Errorf("model %q disappeared from catalog", model)
		}
	}
	if defaults != 1 {
		return fmt.Errorf("expected exactly one default model")
	}
	for model, expected := range map[string]int64{"sonnet": 0, "opus": 0, "sonnet[1m]": 1_000_000, "claude-sonnet-4-6": 1_000_000, "claude-opus-4-6[1m]": 1_000_000, "claude-opus-4-8": 1_000_000, "claude-haiku-4-5": 200_000, "gateway-unknown": 0} {
		if got := knownProviderContextWindow("claude", model); got != expected {
			return fmt.Errorf("%s context = %d, want %d", model, got, expected)
		}
	}
	for _, model := range []string{"sonnet[1m]", "opus[1m]", "claude-opus-4-6[1m]"} {
		if len(appendClaudeCompatibilityArgs([]string{"-p"}, model)) != 1 {
			return fmt.Errorf("native variant %s entered gateway compatibility mode", model)
		}
	}
	provider := AgentProviderRuntime{Kind: "claude", Models: models}
	policy := providerContextPolicy(provider, UserSettings{ClaudeModel: "opus[1m]"})
	if policy.Tokens != 1_000_000 || policy.ThresholdConfigured {
		return fmt.Errorf("1M variant should keep native auto-compaction")
	}
	if err := os.Setenv("CLAUDE_CODE_DISABLE_1M_CONTEXT", "1"); err != nil {
		return err
	}
	policy = providerContextPolicy(provider, UserSettings{ClaudeModel: "opus[1m]"})
	if policy.Tokens != 200_000 || len(providerConfigurationView(provider, UserSettings{ClaudeModel: "opus[1m]"}).Warnings) == 0 {
		return fmt.Errorf("disabled 1M context was not exposed")
	}
	window, threshold := normalizeCodexContextSettings(1_000_000, 900_000)
	if window != 1_000_000 || threshold != 900_000 {
		return fmt.Errorf("Codex preset was changed")
	}
	config := "model = \"custom-a\"\n# keep provider settings\n[model_providers.gateway]\nname = \"gateway\"\n"
	for _, entry := range []struct {
		key   string
		value int64
	}{{"model_context_window", window}, {"model_auto_compact_token_limit", threshold}} {
		config = upsertTOMLScalar(config, "", entry.key, optionalIntegerLiteral(entry.value))
		if got, ok := readTOMLInteger(config, "", entry.key); !ok || got != entry.value {
			return fmt.Errorf("Codex preset round trip failed")
		}
		config = upsertTOMLScalar(config, "", entry.key, "")
		if _, ok := readTOMLInteger(config, "", entry.key); ok {
			return fmt.Errorf("Codex native reset failed")
		}
	}
	if !strings.Contains(config, "# keep provider settings") || !strings.Contains(config, "[model_providers.gateway]") {
		return fmt.Errorf("Codex preset altered provider config")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": []string{"distinct Claude versions and gateway IDs", "stable default model", "native 1M variants and compaction", "1M environment restriction", "Codex preset and reset preserve other settings"}})
}

// Isolated verification for app-owned files and instruction editing. Use the
// platform's GoFiles from `go list`, replace main.go with this existing script,
// then run with --local-files. No Wails window or native CLI is started.
func verifyLocalFiles() error {
	root, err := os.MkdirTemp("", "nicecodex-local-files-")
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(root)
	if err != nil || !samePath(filepath.Dir(absolute), os.TempDir()) || !strings.HasPrefix(filepath.Base(absolute), "nicecodex-local-files-") {
		return fmt.Errorf("unexpected verification directory: %s", root)
	}
	defer os.RemoveAll(absolute)
	checks := []string{}
	skipped := []string{}
	write := func(path, content string) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o600)
	}
	expectFile := func(path, content string) error {
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(payload) != content {
			return fmt.Errorf("unexpected contents in %s", filepath.Base(path))
		}
		return nil
	}
	project := filepath.Join(root, "project")
	agents := filepath.Join(project, "AGENTS.md")
	source := agentsInstructionSource(project, false)
	original := strings.Repeat("中文🙂", 6000) + "\r\n  keep whitespace  \n"
	if err := write(agents, original); err != nil {
		return err
	}
	loaded := source.read()
	if !loaded.Available || loaded.Content != original {
		return fmt.Errorf("large UTF-8 instructions were not read intact")
	}
	saved, err := source.save(InstructionsSaveRequest{Content: original + "更新", Revision: loaded.Revision})
	if err != nil {
		return err
	}
	if err := expectFile(agents, original+"更新"); err != nil {
		return err
	}
	checks = append(checks, "UTF-8 over 16 KB and whitespace round trip")
	if err := write(agents, "external edit"); err != nil {
		return err
	}
	if _, err := source.save(InstructionsSaveRequest{Content: "stale draft", Revision: saved.Revision}); err == nil || !strings.Contains(err.Error(), "INSTRUCTIONS_CHANGED") {
		return fmt.Errorf("stale revision was not rejected: %v", err)
	}
	if err := expectFile(agents, "external edit"); err != nil {
		return err
	}
	other := agentsInstructionSource(filepath.Join(root, "other-project"), false)
	if _, err := other.save(InstructionsSaveRequest{Content: "wrong project", Revision: source.read().Revision}); err == nil {
		return fmt.Errorf("workspace switch was not rejected")
	}
	checks = append(checks, "external edit and workspace conflict protection")
	loaded = source.read()
	override := filepath.Join(project, "AGENTS.override.md")
	if err := write(override, "higher priority"); err != nil {
		return err
	}
	if _, err := source.save(InstructionsSaveRequest{Content: "stale selection", Revision: loaded.Revision}); err == nil {
		return fmt.Errorf("new override did not invalidate revision")
	}
	if _, err := source.save(InstructionsSaveRequest{Content: "", Revision: source.read().Revision}); err != nil {
		return err
	}
	if err := expectFile(override, ""); err != nil {
		return err
	}
	if err := expectFile(agents, "external edit"); err != nil {
		return err
	}
	checks = append(checks, "override selection and clearing preserve fallback file")
	claudeRoot := filepath.Join(root, "claude")
	claudePath := filepath.Join(claudeRoot, "CLAUDE.local.md")
	if err := write(claudePath, "local rules"); err != nil {
		return err
	}
	claude := claudeInstructionSource(claudeRoot, false)
	if _, err := claude.save(InstructionsSaveRequest{Content: "updated local rules", Revision: claude.read().Revision}); err != nil {
		return err
	}
	if err := expectFile(claudePath, "updated local rules"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(claudeRoot, "CLAUDE.md")); !os.IsNotExist(err) {
		return fmt.Errorf("Claude save created the wrong instruction file")
	}
	checks = append(checks, "Claude local fallback is the actual write target")
	loaded = source.read()
	if _, err := source.save(InstructionsSaveRequest{Content: strings.Repeat("a", maxInstructionFileBytes+1), Revision: loaded.Revision}); err == nil {
		return fmt.Errorf("oversized save was accepted")
	}
	if err := expectFile(agents, "external edit"); err != nil {
		return err
	}
	if err := write(agents, strings.Repeat("a", maxInstructionFileBytes+1)); err != nil {
		return err
	}
	if info := source.read(); info.Available || !strings.Contains(info.ReadError, "INSTRUCTIONS_TOO_LARGE") {
		return fmt.Errorf("oversized read is editable")
	}
	if err := write(agents, string([]byte{0xff, 0xfe})); err != nil {
		return err
	}
	if info := source.read(); info.Available || !strings.Contains(info.ReadError, "INSTRUCTIONS_ENCODING") {
		return fmt.Errorf("invalid UTF-8 is editable")
	}
	checks = append(checks, "size and encoding failures preserve files")
	if err := write(agents, "before staging"); err != nil {
		return err
	}
	providerFileMu.Lock()
	err = writeProviderFileAtomicLocked(agents, []byte("staged draft"), 0o600, func() error {
		if err := os.WriteFile(agents, []byte("edit during staging"), 0o600); err != nil {
			return err
		}
		return fmt.Errorf("INSTRUCTIONS_CHANGED")
	})
	providerFileMu.Unlock()
	if err == nil {
		return fmt.Errorf("before-replace rejection was ignored")
	}
	if err := expectFile(agents, "edit during staging"); err != nil {
		return err
	}
	checks = append(checks, "pre-replacement rejection leaves external content intact")
	link := filepath.Join(root, "linked", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	if err := os.Symlink(agents, link); err != nil {
		skipped = append(skipped, "symlink creation unavailable on this host")
	} else {
		linked := agentsInstructionSource(filepath.Dir(link), false)
		if _, err := linked.save(InstructionsSaveRequest{Content: "linked content", Revision: linked.read().Revision}); err != nil {
			return err
		}
		stat, err := os.Lstat(link)
		if err != nil || stat.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("save replaced the symlink")
		}
		if err := expectFile(agents, "linked content"); err != nil {
			return err
		}
		if err := os.Rename(agents, agents+".nicecodex-backup"); err != nil {
			return err
		}
		if restored := linked.read(); !restored.Available || restored.Content != "linked content" {
			return fmt.Errorf("symlink target backup was not recovered")
		}
		checks = append(checks, "symlink preserved and target backup recovered")
	}
	service := &AppService{settingsPath: filepath.Join(root, "corrupt", "settings.json")}
	paths := []string{service.settingsPath, sessionsPath(service.settingsPath), claudeSessionsPath(service.settingsPath), grokAPISessionsPath(service.settingsPath), usagePath(service.settingsPath)}
	for _, path := range paths {
		if err := write(path, "{broken"); err != nil {
			return err
		}
	}
	var settings UserSettings
	_ = service.readLocalJSON("settings", service.settingsPath, &settings)
	service.sessions = service.loadSessions()
	service.claudeSessions = service.loadClaudeSessions()
	service.grokAPISessions = service.loadGrokAPISessions()
	service.usageCache = service.loadLocalUsage()
	if service.persistSettingsLocked(defaultSettings()) == nil || service.persistSessionsLocked() == nil || service.persistClaudeSessionsLocked() == nil || service.persistGrokAPISessionsLocked() == nil || service.persistLocalUsage(service.usageCache) == nil {
		return fmt.Errorf("unread local data could be overwritten")
	}
	if len(service.RetryLocalStorageWrites().Issues) != 5 {
		return fmt.Errorf("read failures were cleared by retry")
	}
	for _, path := range paths {
		if err := expectFile(path, "{broken"); err != nil {
			return err
		}
	}
	checks = append(checks, "all five unread stores refuse overwrite and retry")
	blocker := filepath.Join(root, "blocked-directory")
	if err := write(blocker, "blocker"); err != nil {
		return err
	}
	retry := &AppService{settingsPath: filepath.Join(blocker, "settings.json"), sessions: map[string]*SessionRecord{}}
	if retry.persistSessionsLocked() == nil {
		return fmt.Errorf("write failure was not returned")
	}
	if issues := retry.LocalStorageStatus().Issues; len(issues) != 1 || !issues[0].CanRetry {
		return fmt.Errorf("write failure was not exposed")
	}
	retry.sessions["new"] = &SessionRecord{ID: "new", Name: "latest in-memory state"}
	if err := os.Remove(blocker); err != nil {
		return err
	}
	if len(retry.RetryLocalStorageWrites().Issues) != 0 {
		return fmt.Errorf("retry did not recover")
	}
	payload, err := os.ReadFile(sessionsPath(retry.settingsPath))
	if err != nil || !strings.Contains(string(payload), "latest in-memory state") {
		return fmt.Errorf("retry did not save current memory")
	}
	checks = append(checks, "write failure exposed and current memory recovered")
	jsoncPath := filepath.Join(root, "native", "opencode.jsonc")
	jsoncOriginal := "{\n  // Keep this comment and the large native identifier.\n  \"nativeId\": 9007199254740993,\n  \"compaction\": {\"auto\": true, \"reserved\": 1000},\n  \"mcp\": {\"keep\": {\"type\": \"local\", \"command\": [\"node\", \"server.js\"], \"oauth\": {\"scope\": \"read\"}}}\n}\n"
	if err := write(jsoncPath, jsoncOriginal); err != nil {
		return err
	}
	if err := updateProviderJSONConfig(jsoncPath, func(config map[string]any) error {
		compaction, err := providerJSONObject(config, "compaction")
		if err != nil {
			return err
		}
		compaction["reserved"] = json.Number("2000")
		return nil
	}); err != nil {
		return err
	}
	jsoncPayload, err := os.ReadFile(jsoncPath)
	if err != nil || !strings.Contains(string(jsoncPayload), "Keep this comment") || !strings.Contains(string(jsoncPayload), "9007199254740993") || !strings.Contains(string(jsoncPayload), "\"reserved\": 2000") {
		return fmt.Errorf("JSONC patch did not preserve comments or native number: %v", err)
	}
	if err := write(jsoncPath, "{broken"); err != nil {
		return err
	}
	if err := updateProviderJSONConfig(jsoncPath, func(config map[string]any) error { config["unsafe"] = true; return nil }); err == nil {
		return fmt.Errorf("broken JSONC was treated as empty configuration")
	}
	if err := expectFile(jsoncPath, "{broken"); err != nil {
		return err
	}
	checks = append(checks, "JSONC comments, large numbers and corrupt-file protection")
	store := newScheduledTaskStore(filepath.Join(root, "scheduler", "settings.json"))
	if err := store.load(); err != nil {
		return err
	}
	task, err := store.upsert(ScheduledTask{Title: "fixture", Prompt: "fixture", Workspace: project, Enabled: true, IntervalMin: 5})
	if err != nil {
		return err
	}
	claimed, ok, err := store.claim(task.ID, "fixture-session", task.NextRunAt)
	if err != nil || !ok {
		return fmt.Errorf("task could not be claimed: %v", err)
	}
	if _, ok, err := store.claim(task.ID, "duplicate", task.NextRunAt+3600); err != nil || ok {
		return fmt.Errorf("active task could be claimed twice")
	}
	claimed.Enabled = false
	if _, err := store.upsert(claimed); err != nil {
		return err
	}
	claimed.Prompt = "changed while running"
	if _, err := store.upsert(claimed); err == nil {
		return fmt.Errorf("active task could be edited")
	}
	if err := store.delete(task.ID); err == nil {
		return fmt.Errorf("active task could be deleted")
	}
	if len(store.due(time.Now().Add(24*time.Hour).Unix())) != 0 {
		return fmt.Errorf("active task became due")
	}
	originalPath := store.path
	if err := write(filepath.Join(root, "finish-blocker"), "blocker"); err != nil {
		return err
	}
	store.path = filepath.Join(root, "finish-blocker", "tasks.json")
	if store.finish(task.ID, "fixture-session", nil) == nil || len(store.active()) != 1 {
		return fmt.Errorf("failed finish lost task ownership")
	}
	store.path = originalPath
	if err := store.finish(task.ID, "fixture-session", nil); err != nil {
		return err
	}
	if len(store.active()) != 0 {
		return fmt.Errorf("finished task still active")
	}
	checks = append(checks, "scheduled claim, mutation guards and finish failure ownership")
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "checks": checks, "skipped": skipped})
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCodexWindowsProcessTreeCleanup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Job Object and taskkill regression")
	}
	// Compile the production cleanup into an isolated process harness. Inject the
	// assignment failure in this temporary copy only, never into the shipped app.
	source, err := os.ReadFile(filepath.Join("internal", "codex", "process_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(source), "package codex", "package main", 1)
	assignment := "windows.AssignProcessToJobObject(job, handle)"
	if strings.Count(code, assignment) != 1 {
		t.Fatal("update the isolated assignment fault injection for the production source")
	}
	code = strings.Replace(code, assignment, "qaAssignJob(job, handle)", 1)
	code += `
func qaAssignJob(job, handle windows.Handle) error {
    if os.Getenv("NICE_QA_FORCE_JOB_FAILURE") == "1" { return fmt.Errorf("injected job assignment failure") }
    return windows.AssignProcessToJobObject(job, handle)
}
func main() {
    if len(os.Args) > 1 && os.Args[1] == "leaf" { time.Sleep(time.Minute); return }
    if len(os.Args) > 1 && os.Args[1] == "parent" {
        var start string
        fmt.Scanln(&start)
        child := exec.Command(os.Args[0], "leaf")
        configureProcess(child)
        if err := child.Start(); err != nil { panic(err) }
        fmt.Println(child.Process.Pid)
        time.Sleep(time.Minute)
        return
    }
    parent := exec.Command(os.Args[0], "parent")
    configureProcess(parent)
    input, err := parent.StdinPipe(); if err != nil { panic(err) }
    output, err := parent.StdoutPipe(); if err != nil { panic(err) }
    if err := parent.Start(); err != nil { panic(err) }
    defer parent.Process.Kill()
    cleanup, guardErr := attachKillOnCloseJob(parent.Process)
    if cleanup == nil { panic("missing cleanup") }
    defer cleanup()
    forced := os.Getenv("NICE_QA_FORCE_JOB_FAILURE") == "1"
    if forced != (guardErr != nil) { panic(fmt.Sprintf("unexpected guard result: %v", guardErr)) }
    fmt.Fprintln(input, "start")
    var childPID int
    if _, err := fmt.Fscan(output, &childPID); err != nil { panic(err) }
    child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(childPID))
    if err != nil { panic(err) }
    defer windows.CloseHandle(child)
    defer windows.TerminateProcess(child, 1)
    unrelated := exec.Command(os.Args[0], "leaf")
    configureProcess(unrelated)
    if err := unrelated.Start(); err != nil { panic(err) }
    defer unrelated.Process.Kill()
    unrelatedHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(unrelated.Process.Pid))
    if err != nil { panic(err) }
    defer windows.CloseHandle(unrelatedHandle)
    if os.Getenv("NICE_QA_PARENT_EXIT") == "1" {
        parent.Process.Kill()
        parent.Wait()
    }
    var callers sync.WaitGroup
    for i := 0; i < 8; i++ { callers.Add(1); go func() { defer callers.Done(); cleanup() }() }
    callers.Wait()
    result, err := windows.WaitForSingleObject(child, 5000)
    if err != nil || result != windows.WAIT_OBJECT_0 { panic("owned MCP child survived cleanup") }
    result, err = windows.WaitForSingleObject(unrelatedHandle, 0)
    if err != nil || result != uint32(windows.WAIT_TIMEOUT) { panic("unrelated process was terminated") }
    parent.Wait()
    unrelated.Process.Kill()
    unrelated.Wait()
    fmt.Println("PASS: owned child exited; unrelated process survived; concurrent cleanup safe")
}
`
	dir := t.TempDir()
	helperSource := filepath.Join(dir, "process_cleanup.go")
	helperBinary := filepath.Join(dir, "process-cleanup-qa.exe")
	if err := os.WriteFile(helperSource, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go.exe"), "build", "-o", helperBinary, helperSource)
	configureBackgroundProcess(build)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile process harness: %v\n%s", err, output)
	}
	for _, scenario := range []struct{ name, forced, parentExit string }{
		{"job-cleanup", "0", "0"},
		{"job-parent-exited", "0", "1"},
		{"assignment-failure-fallback", "1", "0"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, helperBinary)
			command.Env = append(os.Environ(), "NICE_QA_FORCE_JOB_FAILURE="+scenario.forced, "NICE_QA_PARENT_EXIT="+scenario.parentExit)
			configureBackgroundProcess(command)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("process cleanup: %v\n%s", err, output)
			}
			t.Log(strings.TrimSpace(string(output)))
		})
	}
}

// Set this to a captured, read-only CLI transcript to replay the native and live
// readers without starting a provider or requiring credentials in the test suite.
func TestAntigravityTranscriptReplay(t *testing.T) {
	path := os.Getenv("NICE_CODEX_ANTIGRAVITY_TRANSCRIPT")
	if path == "" {
		t.Skip("no captured Antigravity transcript supplied")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	assembler := newAntigravityLiveStepAssembler()
	var streamed strings.Builder
	finalText := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		chunk, _, final, kind := parseExternalEvent("gemini", event)
		if kind == "tool" || kind == "compact" {
			assembler.Barrier()
			continue
		}
		if final {
			if kind == "error" {
				t.Fatal("captured provider run failed")
			}
			finalText = chunk
			continue
		}
		if kind != "text" && kind != "thought" {
			continue
		}
		update := assembler.Next(event, kind, chunk)
		if kind != "text" {
			continue
		}
		if update.replace {
			if !replaceExternalBuilderSuffix(&streamed, update.previous, update.next) {
				t.Fatal("step correction could not replace the emitted suffix")
			}
		} else {
			streamed.WriteString(update.delta)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if finalText == "" || streamed.String() != finalText {
		t.Fatalf("live/final mismatch: live=%d bytes final=%d bytes", streamed.Len(), len(finalText))
	}
	turns, err := loadAntigravityNativeTurns(path)
	if err != nil || len(turns) == 0 {
		t.Fatalf("native history replay failed: %v", err)
	}
	if turns[len(turns)-1].AgentText != finalText {
		t.Fatal("reopened history differs from the terminal CLI response")
	}
	t.Logf("live, terminal and reopened history agree: %d bytes", len(finalText))
}

func TestAntigravityMarkdownChunksRemainLossless(t *testing.T) {
	chunks := []string{"## Analysis", "\n\n", "- Parent\n", "  - Child", "\n\n```go\n", "    value := 1\n", "```\n\n", "| A | B |\n| --- | --- |\n| 1 | 2 |\n"}
	for _, flat := range []bool{false, true} {
		assembler := newAntigravityLiveStepAssembler()
		var actual strings.Builder
		for i, chunk := range chunks {
			state := "ACTIVE"
			if i == len(chunks)-1 {
				state = "DONE"
			}
			step := map[string]any{"step_index": float64(1), "step_type": "agent_response", "state": state, "text_delta": chunk}
			event := map[string]any{"event": "step_update", "step_update": step}
			if flat {
				event = map[string]any{"type": "PLANNER_RESPONSE", "step_index": float64(1), "state": state, "text_delta": chunk}
			}
			decoded, _, _, kind := parseExternalEvent("gemini", event)
			if decoded != chunk || kind != "text" {
				t.Fatalf("flat=%v chunk=%d: got %q (%s), want %q", flat, i, decoded, kind, chunk)
			}
			update := assembler.Next(event, kind, decoded)
			if update.replace {
				t.Fatalf("explicit delta was treated as a snapshot: flat=%v chunk=%d", flat, i)
			}
			actual.WriteString(update.delta)
		}
		if actual.String() != strings.Join(chunks, "") {
			t.Fatalf("Markdown boundaries changed: flat=%v got %q", flat, actual.String())
		}
	}
	if got := antigravityEventWorkspace(map[string]any{"workspace": "  C:/project  "}); got != "C:/project" {
		t.Fatalf("metadata should still be trimmed: %q", got)
	}
}

func TestAntigravityShorterFinalStepCorrection(t *testing.T) {
	assembler := newAntigravityLiveStepAssembler()
	active := map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": float64(1), "state": "ACTIVE", "text_delta": "```go\nincorrect duplicated body\n"}}
	assembler.Next(active, "text", "```go\nincorrect duplicated body\n")
	final := map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": float64(1), "state": "DONE", "text": "```go\nok\n```"}}
	update := assembler.Next(final, "text", "```go\nok\n```")
	if !update.replace || update.segmentSnapshot != "```go\nok\n```" {
		t.Fatalf("final corrected snapshot was not authoritative: %#v", update)
	}
}

// The single-server upsert/remove APIs must preserve unrelated keys and
// unrelated servers in the native Gemini/OpenCode config files. Paths are
// redirected into a temp home via GEMINI_CLI_HOME / OPENCODE_CONFIG.
func TestUpsertAndRemoveExternalMCPServer(t *testing.T) {
	dir := t.TempDir()
	geminiHome := filepath.Join(dir, "gemini")
	opencodeDir := filepath.Join(dir, "opencode")
	if err := os.MkdirAll(geminiHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(opencodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_CLI_HOME", geminiHome)
	t.Setenv("OPENCODE_CONFIG", filepath.Join(opencodeDir, "opencode.json"))

	// Seed the config selected by the runtime on this machine. Developers may
	// already have Antigravity installed, while CI normally exercises legacy
	// Gemini; both paths must preserve unrelated settings and servers.
	geminiPath := externalRuntimeConfigPath("gemini", "global", "")
	if err := os.MkdirAll(filepath.Dir(geminiPath), 0o755); err != nil {
		t.Fatal(err)
	}
	usingAntigravity := geminiUsesAntigravity(geminiHome)
	seed := map[string]any{
		"theme": "dark",
		"mcpServers": map[string]any{
			"keep-me": map[string]any{"type": "stdio", "command": "keep-cmd"},
		},
	}
	seeded, _ := json.Marshal(seed)
	if err := os.WriteFile(geminiPath, seeded, 0o644); err != nil {
		t.Fatal(err)
	}

	service := &AppService{}
	input := MCPServerInput{
		Name:      "added-server",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "server-pkg"},
		Env:       map[string]string{"KEY": "value"},
	}
	if err := service.UpsertExternalMCPServer("gemini", "global", input); err != nil {
		t.Fatalf("upsert gemini: %v", err)
	}

	var after map[string]any
	payload, err := os.ReadFile(geminiPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if after["theme"] != "dark" {
		t.Fatalf("unrelated top-level key lost: %v", after)
	}
	servers, _ := after["mcpServers"].(map[string]any)
	if servers["keep-me"] == nil {
		t.Fatalf("existing server keep-me was dropped: %v", servers)
	}
	added, _ := servers["added-server"].(map[string]any)
	if added == nil || added["command"] != "npx" {
		t.Fatalf("added server missing or malformed: %v", added)
	}
	if args, _ := added["args"].([]any); len(args) != 2 {
		t.Fatalf("args not persisted: %v", added)
	}

	if err := service.RemoveExternalMCPServer("gemini", "global", "added-server"); err != nil {
		t.Fatalf("remove gemini: %v", err)
	}
	payload, _ = os.ReadFile(geminiPath)
	after = map[string]any{}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatal(err)
	}
	servers, _ = after["mcpServers"].(map[string]any)
	if _, exists := servers["added-server"]; exists {
		t.Fatalf("added-server still present after removal: %v", servers)
	}
	if servers["keep-me"] == nil {
		t.Fatalf("keep-me lost after removal: %v", servers)
	}
	if after["theme"] != "dark" {
		t.Fatalf("theme lost after removal: %v", after)
	}

	// Removing the last server deletes the key but keeps other config intact.
	if err := service.RemoveExternalMCPServer("gemini", "global", "keep-me"); err != nil {
		t.Fatalf("remove keep-me: %v", err)
	}
	payload, _ = os.ReadFile(geminiPath)
	after = map[string]any{}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatal(err)
	}
	if _, exists := after["mcpServers"]; exists {
		t.Fatalf("empty mcpServers key should be removed: %v", after)
	}
	if after["theme"] != "dark" {
		t.Fatalf("theme lost after final removal: %v", after)
	}

	// OpenCode uses the "mcp" key and a fresh file.
	if err := service.UpsertExternalMCPServer("opencode", "global", input); err != nil {
		t.Fatalf("upsert opencode: %v", err)
	}
	payload, _ = os.ReadFile(filepath.Join(opencodeDir, "opencode.json"))
	var openCodeConfig map[string]any
	if err := json.Unmarshal(payload, &openCodeConfig); err != nil {
		t.Fatal(err)
	}
	servers, _ = openCodeConfig["mcp"].(map[string]any)
	if servers == nil || servers["added-server"] == nil {
		t.Fatalf("opencode server missing: %v", openCodeConfig)
	}

	// Removing an unknown server fails clearly instead of wiping the file.
	if err := service.RemoveExternalMCPServer("opencode", "global", "nope"); err == nil {
		t.Fatal("expected error removing unknown server")
	}
	payload, _ = os.ReadFile(filepath.Join(opencodeDir, "opencode.json"))
	openCodeConfig = map[string]any{}
	if err := json.Unmarshal(payload, &openCodeConfig); err != nil {
		t.Fatal(err)
	}
	servers, _ = openCodeConfig["mcp"].(map[string]any)
	if servers == nil || servers["added-server"] == nil {
		t.Fatalf("config wiped by failed removal: %v", openCodeConfig)
	}

	// Remote transport writes a URL server.
	remote := MCPServerInput{Name: "remote-server", Transport: "http", Command: "https://example.com/mcp"}
	if err := service.UpsertExternalMCPServer("gemini", "global", remote); err != nil {
		t.Fatalf("upsert remote: %v", err)
	}
	payload, _ = os.ReadFile(geminiPath)
	after = map[string]any{}
	if err := json.Unmarshal(payload, &after); err != nil {
		t.Fatal(err)
	}
	servers, _ = after["mcpServers"].(map[string]any)
	entry, _ := servers["remote-server"].(map[string]any)
	urlKey := "url"
	if usingAntigravity {
		urlKey = "serverUrl"
	}
	if entry == nil || entry[urlKey] != "https://example.com/mcp" {
		t.Fatalf("remote server malformed: %v", entry)
	}
}

func TestMCPServerInputValidation(t *testing.T) {
	if _, err := validateMCPServerName("has space"); err == nil {
		t.Fatal("whitespace name must be rejected")
	}
	if _, err := validateMCPServerName(""); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if _, err := (MCPServerInput{Name: "x", Transport: "stdio"}).serverObject(); err == nil {
		t.Fatal("stdio without command must be rejected")
	}
	if _, err := (MCPServerInput{Name: "x", Transport: "http", Command: "not-a-url"}).serverObject(); err == nil {
		t.Fatal("http without URL must be rejected")
	}
	object, err := (MCPServerInput{Name: "x", Transport: "http", Command: "https://ok/mcp"}).serverObject()
	if err != nil || object["url"] != "https://ok/mcp" {
		t.Fatalf("valid http input rejected: %v %v", object, err)
	}
}

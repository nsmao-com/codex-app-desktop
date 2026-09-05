package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ClientInfo is sent in app-server initialize and becomes the upstream
// User-Agent / originator (e.g. "codex_desktop/0.144.6 ... (codex_desktop; 0.1.0)").
// Some reverse-proxy Codex channels only allow official client names.
type ClientInfo struct {
	Name    string
	Title   string
	Version string
}

type Client struct {
	mu             sync.Mutex
	startMu        sync.Mutex
	writeMu        sync.Mutex
	nextID         atomic.Int64
	command        *exec.Cmd
	stdin          io.WriteCloser
	done           chan struct{}
	pending        map[int64]chan rpcResult
	inboundRequest map[string]json.RawMessage
	status         Status
	onEvent        func(Event)
	// processCleanup tears down the OS process tree (Windows Job Object / process group).
	processCleanup     func()
	processTreeGuarded bool
	transportErr       error
	// streamStates accumulates Codex delta notifications into bounded snapshots.
	// Wails dispatches each emitted event from its own goroutine, so individual
	// deltas can reach the WebView out of order. A per-stream sequence and snapshot
	// lets the frontend accept the newest complete value without relying on bridge
	// delivery order. Pending deltas are coalesced before crossing the bridge.
	streamStates     map[string]streamState
	streamOrder      uint64
	streamFlushTimer *time.Timer
}

type streamState struct {
	sequence     uint64
	text         []byte
	pending      []byte
	method       string
	payload      map[string]any
	lastFlush    time.Time
	pendingOrder uint64
}

const (
	maxStreamSnapshotBytes = 1_000_000
	// Keep the UI responsive on slower WebView2 machines while preserving a
	// visible streaming cadence. The frontend also batches updates at this scale.
	streamFlushInterval   = 48 * time.Millisecond
	streamFlushBytes      = 2 * 1024
	maxStreamPendingBytes = 64 * 1024
)

func NewClient(onEvent func(Event)) *Client {
	return &Client{
		pending:        make(map[int64]chan rpcResult),
		inboundRequest: make(map[string]json.RawMessage),
		streamStates:   make(map[string]streamState),
		onEvent:        onEvent,
		status:         Status{State: "disconnected"},
	}
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Client) Start(ctx context.Context, workspace string, info ClientInfo) error {
	c.startMu.Lock()
	defer c.startMu.Unlock()

	c.mu.Lock()
	if c.status.Running {
		c.mu.Unlock()
		return nil
	}
	c.setStatusLocked(Status{State: "starting", Running: false, Message: "Starting Codex", Workspace: workspace})
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: c.Status()})

	spec, err := resolveCommand()
	if err != nil {
		c.failStart(err, workspace)
		return err
	}
	detection := detectResolvedCommand(spec)
	if !detection.Available {
		err := errors.New(detection.Error)
		c.failStart(err, workspace)
		return err
	}

	// Priority: settings (info) > env vars > official desktop defaults.
	// Unofficial names like "nice_codex_desktop" are rejected by some channels with:
	//   "This channel does not allow the current client"
	clientName := firstNonEmpty(info.Name, os.Getenv("NICE_CODEX_CLIENT_NAME"), "codex_desktop")
	clientTitle := firstNonEmpty(info.Title, os.Getenv("NICE_CODEX_CLIENT_TITLE"), "Codex Desktop")
	clientVersion := firstNonEmpty(info.Version, os.Getenv("NICE_CODEX_CLIENT_VERSION"), "0.1.0")
	clientInfo := map[string]any{
		"name":    clientName,
		"title":   clientTitle,
		"version": clientVersion,
	}

	args := append(append([]string{}, spec.prefixArgs...), "app-server", "--listen", "stdio://")
	env := withEnvOverride(os.Environ(), "CODEX_INTERNAL_ORIGINATOR_OVERRIDE", clientName)
	catalogPath, catalogCleanup, catalogErr := prepareModelCatalog(ctx, spec, workspace, env)
	defer func() { catalogCleanup() }()
	if catalogErr != nil {
		// Do not block other models when an old CLI lacks the catalog command.
		c.emit(Event{Type: "stderr", Data: map[string]any{
			"message": "Astra model metadata compatibility: " + catalogErr.Error(),
		}})
	} else if catalogPath != "" {
		args = append(args, "-c", "model_catalog_json="+strconv.Quote(catalogPath))
	}
	command := exec.Command(spec.path, args...)
	command.Dir = workspace
	// Force originator early (before initialize) so the first HTTP client also
	// carries the spoofed identity. Matches app-server initialize clientInfo.name.
	command.Env = env
	configureProcess(command)

	stdin, err := command.StdinPipe()
	if err != nil {
		c.failStart(err, workspace)
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		c.failStart(err, workspace)
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		c.failStart(err, workspace)
		return err
	}
	if err := command.Start(); err != nil {
		c.failStart(err, workspace)
		return err
	}

	// Bind app-server (+ MCP children) so Stop/exit can reap the whole tree.
	// Best-effort: failure must not block starting Codex.
	cleanup, cleanupErr := attachKillOnCloseJob(command.Process)
	if cleanupErr != nil {
		c.emit(Event{Type: "stderr", Data: map[string]any{
			"message": "process-tree guard unavailable: " + cleanupErr.Error(),
		}})
	}
	processCleanup := cleanup
	removeCatalog := catalogCleanup
	cleanup = sync.OnceFunc(func() {
		if processCleanup != nil {
			processCleanup()
		}
		removeCatalog()
	})
	catalogCleanup = func() {} // Ownership transfers to the process lifetime.

	done := make(chan struct{})
	c.mu.Lock()
	c.command = command
	c.stdin = stdin
	c.done = done
	c.processCleanup = cleanup
	c.processTreeGuarded = cleanupErr == nil
	c.transportErr = nil
	c.streamStates = make(map[string]streamState)
	c.streamOrder = 0
	if c.streamFlushTimer != nil {
		c.streamFlushTimer.Stop()
		c.streamFlushTimer = nil
	}
	c.status = Status{
		State:     "initializing",
		Running:   true,
		Message:   "Negotiating app-server protocol",
		Binary:    detection.Binary,
		Version:   detection.Version,
		Workspace: workspace,
	}
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: c.Status()})

	go c.readLoop(command, stdout)
	go c.stderrLoop(stderr)
	go c.waitLoop(command, done)

	handshakeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	initResult, err := c.Request(handshakeCtx, "initialize", map[string]any{
		"clientInfo": clientInfo,
		"capabilities": map[string]any{
			"experimentalApi":    true,
			"requestAttestation": false,
		},
	})
	if err != nil {
		_ = c.Stop()
		return fmt.Errorf("initialize app-server: %w", err)
	}
	if err := c.Notify("initialized", nil); err != nil {
		_ = c.Stop()
		return fmt.Errorf("acknowledge app-server initialization: %w", err)
	}

	userAgent := parseInitializeUserAgent(initResult)

	c.mu.Lock()
	c.status.State = "ready"
	c.status.Running = true
	c.status.Message = "Codex is ready"
	c.status.UserAgent = userAgent
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: c.Status()})
	if userAgent != "" {
		c.emit(Event{Type: "stderr", Data: map[string]any{
			"message": "upstream client User-Agent: " + userAgent,
		}})
	}
	return nil
}

func (c *Client) Stop() error {
	c.mu.Lock()
	command := c.command
	stdin := c.stdin
	done := c.done
	cleanup := c.processCleanup
	guarded := c.processTreeGuarded
	c.processCleanup = nil
	c.processTreeGuarded = false
	if command == nil || done == nil {
		c.mu.Unlock()
		if cleanup != nil {
			cleanup()
		}
		return nil
	}
	c.status.State = "stopping"
	c.status.Message = "Stopping Codex"
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: c.Status()})

	if !guarded && cleanup != nil {
		// Without a Job/process group, EOF may let the parent exit before the
		// fallback can discover its MCP descendants. Tear down the live tree first.
		cleanup()
	}
	if stdin != nil {
		c.writeMu.Lock()
		_ = stdin.Close()
		c.writeMu.Unlock()
	}

	// Soft wait for graceful exit after stdin close.
	graceTimer := time.NewTimer(1500 * time.Millisecond)
	select {
	case <-done:
		graceTimer.Stop()
		if cleanup != nil {
			cleanup()
		}
		return nil
	case <-graceTimer.C:
	}

	// Hard stop: kill entire process tree (app-server + MCP python/node/npx/…).
	pid := 0
	if command.Process != nil {
		pid = command.Process.Pid
	}
	if cleanup != nil {
		// Job Object / process-group cleanup tears down the whole tree at once.
		cleanup()
	} else if pid > 0 {
		// Keep the parent alive until taskkill has discovered its descendants.
		killProcessTree(pid)
		_ = command.Process.Kill()
	}

	hardTimer := time.NewTimer(2 * time.Second)
	select {
	case <-done:
		hardTimer.Stop()
		return nil
	case <-hardTimer.C:
		return errors.New("timed out while stopping Codex")
	}
}

func (c *Client) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	resultChannel := make(chan rpcResult, 1)

	c.mu.Lock()
	if !c.status.Running || c.stdin == nil {
		c.mu.Unlock()
		return nil, errors.New("Codex app-server is not running")
	}
	done := c.done
	c.pending[id] = resultChannel
	c.mu.Unlock()

	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := c.write(message); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case result := <-resultChannel:
		return result.result, result.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-done:
		return nil, errors.New("Codex app-server stopped before the request completed")
	}
}

func (c *Client) Notify(method string, params any) error {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	return c.write(message)
}

func (c *Client) ResolveServerRequest(requestKey string, result any) error {
	c.mu.Lock()
	requestID, ok := c.inboundRequest[requestKey]
	c.mu.Unlock()
	if !ok {
		return errors.New("the approval request is no longer pending")
	}

	if err := c.write(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(requestID),
		"result":  result,
	}); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.inboundRequest, requestKey)
	c.mu.Unlock()
	return nil
}

func (c *Client) write(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	running := c.status.Running
	c.mu.Unlock()
	if !running || stdin == nil {
		return errors.New("Codex app-server is not running")
	}
	_, err = stdin.Write(payload)
	return err
}

func (c *Client) readLoop(command *exec.Cmd, stdout io.Reader) {
	defer c.flushPendingStreamsFor(command)
	// JSON Decoder grows with the current JSON-RPC value and has no Scanner token
	// ceiling, so large histories and tool results cannot kill the stdout reader.
	decoder := json.NewDecoder(stdout)
	for {
		var message wireMessage
		if err := decoder.Decode(&message); err != nil {
			if !errors.Is(err, io.EOF) {
				c.failTransport(command, fmt.Errorf("decode app-server message: %w", err))
			}
			return
		}
		c.handleMessage(message)
	}
}

func (c *Client) failTransport(command *exec.Cmd, err error) {
	c.flushPendingStreamsFor(command)
	c.mu.Lock()
	if c.command != command || !c.status.Running {
		c.mu.Unlock()
		return
	}
	message := "Codex app-server output stream failed: " + err.Error()
	c.transportErr = errors.New(message)
	process := command.Process
	c.mu.Unlock()

	c.emit(Event{Type: "transport-error", Data: map[string]any{
		"message":     message,
		"restartable": true,
	}})
	// A dead stdout reader with a live process leaves every RPC pending forever.
	// Terminate this app-server so waitLoop releases callers and reconnect can start cleanly.
	if process != nil {
		_ = process.Kill()
	}
}

func (c *Client) stderrLoop(stderr io.Reader) {
	reader := bufio.NewReader(stderr)
	for {
		line, err := reader.ReadString('\n')
		message := strings.TrimSpace(line)
		if message != "" {
			c.emit(Event{Type: "stderr", Data: map[string]any{"message": message}})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				c.emit(Event{Type: "stderr", Data: map[string]any{"message": "stderr stream failed: " + err.Error()}})
			}
			return
		}
	}
}

func (c *Client) handleMessage(message wireMessage) {
	if message.Method == "" && len(message.ID) > 0 {
		id, err := strconv.ParseInt(string(message.ID), 10, 64)
		if err != nil {
			return
		}
		c.mu.Lock()
		channel := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if channel == nil {
			return
		}
		if message.Error != nil {
			channel <- rpcResult{err: errors.New(message.Error.Message)}
			return
		}
		channel <- rpcResult{result: message.Result}
		return
	}

	if message.Method == "" {
		return
	}
	data := decodeJSON(message.Params)
	if len(message.ID) > 0 {
		// Approval/user-input requests are a barrier for streamed text. Flush the
		// visible prefix before showing the request so the timeline stays ordered.
		c.flushPendingStreams()
		requestKey := string(message.ID)
		if !isSupportedServerRequest(message.Method) {
			_ = c.write(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(message.ID),
				"error": map[string]any{
					"code":    -32601,
					"message": "Nice Codex does not support this request type yet",
				},
			})
			c.emit(Event{Type: "unsupported-request", Method: message.Method, Data: map[string]any{
				"message": "Codex requested an interaction this client does not support yet.",
			}})
			return
		}
		c.mu.Lock()
		c.inboundRequest[requestKey] = append(json.RawMessage(nil), message.ID...)
		c.mu.Unlock()
		c.emit(Event{Type: "request", Method: message.Method, RequestKey: requestKey, Data: data})
		return
	}
	if c.queueStreamNotification(message.Method, data) {
		return
	}
	// Structured notifications (item/turn completion, status, usage, etc.) are
	// barriers. Do not let a buffered text batch appear after its terminal row.
	c.flushPendingStreams()
	c.cleanupStreamNotification(message.Method, data)
	c.emit(Event{Type: "notification", Method: message.Method, Data: data})
}

// queueStreamNotification accumulates a stream delta and emits a bounded,
// cumulative snapshot at a modest cadence. Wails creates a goroutine and a
// synchronous WebView call for every Event.Emit; forwarding every token can
// therefore exhaust memory on slower machines. The sequence/snapshot pair is
// retained so the frontend remains safe if Wails still delivers two batches out
// of order.
func (c *Client) queueStreamNotification(method string, value any) bool {
	field := streamFieldForMethod(method)
	if field == "" {
		return false
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return false
	}
	threadID := stringValue(payload["threadId"])
	itemID := stringValue(payload["itemId"])
	turnID := stringValue(payload["turnId"])
	delta := stringValue(payload["delta"])
	if threadID == "" || itemID == "" {
		return false
	}
	// Keep empty/snapshot-only variants on the legacy path. Some older app-server
	// builds put the complete text in `text` without a `delta`; forwarding it
	// unchanged preserves that compatibility behavior.
	if delta == "" {
		return false
	}
	key := threadID + "\x00" + turnID + "\x00" + itemID + "\x00" + field
	now := time.Now()
	var events []Event
	c.mu.Lock()
	state := c.streamStates[key]
	state.sequence++
	if state.pendingOrder == 0 {
		c.streamOrder++
		state.pendingOrder = c.streamOrder
	}
	accepted := boundedUTF8Bytes(delta, maxStreamSnapshotBytes-len(state.text))
	if len(accepted) > 0 {
		state.text = append(state.text, accepted...)
		pending := boundedUTF8Bytes(string(accepted), maxStreamPendingBytes-len(state.pending))
		state.pending = append(state.pending, pending...)
	}
	state.method = method
	// decodeJSON creates a fresh map for each notification and the read loop is
	// the only caller that can still observe it. Retain it until the coalesced
	// flush instead of cloning a metadata map for every token.
	state.payload = payload
	shouldFlush := len(state.pending) >= streamFlushBytes
	if !shouldFlush && !state.lastFlush.IsZero() {
		shouldFlush = now.Sub(state.lastFlush) >= streamFlushInterval
	}
	if !shouldFlush && state.lastFlush.IsZero() {
		// Deliver the first visible chunk immediately; subsequent chunks are
		// coalesced by the interval/byte thresholds above.
		shouldFlush = true
	}
	c.streamStates[key] = state
	if shouldFlush {
		events = c.takePendingStreamsLocked(now, []string{key})
	} else {
		c.scheduleStreamFlushLocked(streamFlushInterval - now.Sub(state.lastFlush))
	}
	c.mu.Unlock()
	for _, event := range events {
		c.emit(event)
	}
	return true
}

func (c *Client) flushPendingStreams() {
	c.mu.Lock()
	c.stopStreamFlushTimerLocked()
	events := c.takePendingStreamsLocked(time.Now(), nil)
	c.mu.Unlock()
	for _, event := range events {
		c.emit(event)
	}
}

func (c *Client) flushPendingStreamsFor(command *exec.Cmd) {
	c.mu.Lock()
	if command != nil && c.command != command {
		c.mu.Unlock()
		return
	}
	c.stopStreamFlushTimerLocked()
	events := c.takePendingStreamsLocked(time.Now(), nil)
	c.mu.Unlock()
	for _, event := range events {
		c.emit(event)
	}
}

func (c *Client) scheduleStreamFlushLocked(delay time.Duration) {
	if c.streamFlushTimer != nil {
		return
	}
	if delay <= 0 {
		delay = streamFlushInterval
	}
	c.streamFlushTimer = time.AfterFunc(delay, func() {
		c.mu.Lock()
		c.streamFlushTimer = nil
		events := c.takePendingStreamsLocked(time.Now(), nil)
		c.mu.Unlock()
		for _, event := range events {
			c.emit(event)
		}
	})
}

func (c *Client) stopStreamFlushTimerLocked() {
	if c.streamFlushTimer == nil {
		return
	}
	c.streamFlushTimer.Stop()
	c.streamFlushTimer = nil
}

// takePendingStreamsLocked snapshots pending stream state in source order. The
// caller must hold c.mu; returned payloads are detached before the lock is
// released because Wails serializes them asynchronously.
func (c *Client) takePendingStreamsLocked(now time.Time, keys []string) []Event {
	if len(c.streamStates) == 0 {
		return nil
	}
	if keys == nil {
		keys = make([]string, 0, len(c.streamStates))
		for key, state := range c.streamStates {
			if len(state.pending) > 0 {
				keys = append(keys, key)
			}
		}
	} else {
		filtered := keys[:0]
		for _, key := range keys {
			if state, ok := c.streamStates[key]; ok && len(state.pending) > 0 {
				filtered = append(filtered, key)
			}
		}
		keys = filtered
	}
	sort.SliceStable(keys, func(left, right int) bool {
		return c.streamStates[keys[left]].pendingOrder < c.streamStates[keys[right]].pendingOrder
	})
	events := make([]Event, 0, len(keys))
	for _, key := range keys {
		state, ok := c.streamStates[key]
		if !ok || len(state.pending) == 0 {
			continue
		}
		payload := cloneStreamPayload(state.payload)
		payload["delta"] = string(state.pending)
		payload["streamSequence"] = state.sequence
		payload["streamText"] = string(append([]byte(nil), state.text...))
		payload["streamMode"] = "replace"
		events = append(events, Event{Type: "notification", Method: state.method, Data: payload})
		state.pending = nil
		state.pendingOrder = 0
		state.lastFlush = now
		c.streamStates[key] = state
	}
	return events
}

func cloneStreamPayload(payload map[string]any) map[string]any {
	clone := make(map[string]any, len(payload)+3)
	for key, value := range payload {
		switch key {
		case "delta", "text", "streamSequence", "streamText", "streamMode":
			// These fields are rebuilt from the bounded stream state below.
			continue
		default:
			clone[key] = value
		}
	}
	return clone
}

func boundedUTF8Bytes(value string, maxBytes int) []byte {
	if maxBytes <= 0 || value == "" {
		return nil
	}
	if len(value) <= maxBytes {
		return []byte(value)
	}
	return []byte(utf8Prefix(value, maxBytes))
}

func utf8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func (c *Client) cleanupStreamNotification(method string, value any) {
	scope := ""
	switch method {
	case "item/completed":
		scope = "item"
	case "turn/completed", "turn/failed", "turn/aborted", "turn/cancelled", "turn/interrupted":
		scope = "turn"
	case "thread/archived", "thread/deleted", "thread/closed":
		scope = "thread"
	default:
		return
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return
	}
	threadID := stringValue(payload["threadId"])
	turnID := stringValue(payload["turnId"])
	itemID := stringValue(payload["itemId"])
	if turn, ok := payload["turn"].(map[string]any); ok && turnID == "" {
		turnID = stringValue(turn["id"])
	}
	if item, ok := payload["item"].(map[string]any); ok && itemID == "" {
		itemID = stringValue(item["id"])
	}
	if threadID == "" {
		return
	}
	if scope == "item" && itemID == "" {
		return
	}
	c.mu.Lock()
	for key := range c.streamStates {
		parts := strings.SplitN(key, "\x00", 4)
		if len(parts) != 4 || parts[0] != threadID {
			continue
		}
		turnMatches := turnID == "" || parts[1] == turnID
		itemMatches := itemID == "" || parts[2] == itemID
		if scope == "thread" || (scope == "turn" && turnMatches) || (scope == "item" && turnMatches && itemMatches) {
			delete(c.streamStates, key)
		}
	}
	c.mu.Unlock()
}

func streamFieldForMethod(method string) string {
	switch method {
	case "item/agentMessage/delta", "item/plan/delta":
		return "text"
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		return "output"
	case "item/reasoning/summaryTextDelta", "item/reasoning/delta":
		return "reasoningSummary"
	case "item/reasoning/textDelta":
		return "reasoningContent"
	default:
		return ""
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func isSupportedServerRequest(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/tool/requestUserInput",
		"mcpServer/elicitation/request",
		"item/permissions/requestApproval",
		"applyPatchApproval",
		"execCommandApproval":
		return true
	default:
		return false
	}
}

func (c *Client) waitLoop(command *exec.Cmd, done chan struct{}) {
	err := command.Wait()
	// The process can exit immediately after its final delta. Flush that last
	// bounded batch before clearing the transport state so no visible suffix is
	// lost when the terminal notification is absent or delayed.
	c.flushPendingStreamsFor(command)

	c.mu.Lock()
	if c.command != command {
		c.mu.Unlock()
		return
	}
	pending := c.pending
	c.pending = make(map[int64]chan rpcResult)
	c.inboundRequest = make(map[string]json.RawMessage)
	cleanup := c.processCleanup
	transportErr := c.transportErr
	c.transportErr = nil
	c.processCleanup = nil
	c.stopStreamFlushTimerLocked()
	c.processTreeGuarded = false
	c.streamStates = make(map[string]streamState)
	c.streamOrder = 0
	c.command = nil
	c.stdin = nil
	c.done = nil
	c.status.Running = false
	stopping := c.status.State == "stopping"
	if stopping {
		c.status.State = "disconnected"
		c.status.Message = "Codex is stopped"
	} else {
		c.status.State = "error"
		if transportErr != nil {
			c.status.Message = transportErr.Error()
		} else if err != nil {
			c.status.Message = err.Error()
		} else {
			c.status.Message = "Codex app-server exited unexpectedly"
		}
	}
	status := c.status
	close(done)
	c.mu.Unlock()

	// Process exited unexpectedly: still close the job so orphan MCP children die.
	if cleanup != nil {
		cleanup()
	} else if command.Process != nil && command.Process.Pid > 0 && status.State == "error" {
		killProcessTree(command.Process.Pid)
	}

	for _, channel := range pending {
		channel <- rpcResult{err: errors.New("Codex app-server stopped")}
	}
	if !stopping {
		c.emit(Event{Type: "transport-error", Data: map[string]any{
			"message":     status.Message,
			"restartable": transportErr != nil,
		}})
	}
	c.emit(Event{Type: "status", Data: status})
}

func (c *Client) failStart(err error, workspace string) {
	c.mu.Lock()
	c.status = Status{State: "error", Running: false, Message: err.Error(), Workspace: workspace}
	status := c.status
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: status})
}

func (c *Client) setStatusLocked(status Status) {
	c.status = status
}

func (c *Client) emit(event Event) {
	if c.onEvent != nil {
		c.onEvent(event)
	}
}

func decodeJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// withEnvOverride returns a copy of env with key=value, replacing any existing key.
func withEnvOverride(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	return append(out, prefix+value)
}

func parseInitializeUserAgent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.UserAgent)
}

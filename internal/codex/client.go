package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxMessageSize = 64 * 1024 * 1024

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
}

func NewClient(onEvent func(Event)) *Client {
	return &Client{
		pending:        make(map[int64]chan rpcResult),
		inboundRequest: make(map[string]json.RawMessage),
		onEvent:        onEvent,
		status:         Status{State: "disconnected"},
	}
}

func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Client) Start(ctx context.Context, workspace string) error {
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

	args := append(append([]string{}, spec.prefixArgs...), "app-server", "--listen", "stdio://")
	command := exec.Command(spec.path, args...)
	command.Dir = workspace
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

	detection := Detect()
	done := make(chan struct{})
	c.mu.Lock()
	c.command = command
	c.stdin = stdin
	c.done = done
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
	_, err = c.Request(handshakeCtx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "nice_codex_desktop",
			"title":   "Nice Codex",
			"version": "0.1.0",
		},
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

	c.mu.Lock()
	c.status.State = "ready"
	c.status.Running = true
	c.status.Message = "Codex is ready"
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: c.Status()})
	return nil
}

func (c *Client) Stop() error {
	c.mu.Lock()
	command := c.command
	stdin := c.stdin
	done := c.done
	if command == nil || done == nil {
		c.mu.Unlock()
		return nil
	}
	c.status.State = "stopping"
	c.status.Message = "Stopping Codex"
	c.mu.Unlock()
	c.emit(Event{Type: "status", Data: c.Status()})

	if stdin != nil {
		c.writeMu.Lock()
		_ = stdin.Close()
		c.writeMu.Unlock()
	}

	select {
	case <-done:
		return nil
	case <-time.After(1500 * time.Millisecond):
	}

	if command.Process != nil {
		_ = command.Process.Kill()
	}
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
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
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maxMessageSize)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		c.handleLine(line)
	}
	if err := scanner.Err(); err != nil && c.isCurrentCommand(command) {
		c.emit(Event{Type: "transport-error", Data: map[string]any{"message": err.Error()}})
	}
}

func (c *Client) stderrLoop(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 32*1024), 2*1024*1024)
	for scanner.Scan() {
		message := strings.TrimSpace(scanner.Text())
		if message != "" {
			c.emit(Event{Type: "stderr", Data: map[string]any{"message": message}})
		}
	}
}

func (c *Client) handleLine(line []byte) {
	var message wireMessage
	if err := json.Unmarshal(line, &message); err != nil {
		c.emit(Event{Type: "transport-error", Data: map[string]any{"message": "Invalid app-server message"}})
		return
	}

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
	c.emit(Event{Type: "notification", Method: message.Method, Data: data})
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

	c.mu.Lock()
	if c.command != command {
		c.mu.Unlock()
		return
	}
	pending := c.pending
	c.pending = make(map[int64]chan rpcResult)
	c.inboundRequest = make(map[string]json.RawMessage)
	c.command = nil
	c.stdin = nil
	c.done = nil
	c.status.Running = false
	if c.status.State == "stopping" || err == nil {
		c.status.State = "disconnected"
		c.status.Message = "Codex is stopped"
	} else {
		c.status.State = "error"
		c.status.Message = err.Error()
	}
	status := c.status
	close(done)
	c.mu.Unlock()

	for _, channel := range pending {
		channel <- rpcResult{err: errors.New("Codex app-server stopped")}
	}
	c.emit(Event{Type: "status", Data: status})
}

func (c *Client) isCurrentCommand(command *exec.Cmd) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.command == command && c.status.Running
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

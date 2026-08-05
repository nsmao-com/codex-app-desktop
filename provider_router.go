package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultProviderRouterPort              = 15722
	defaultProviderFailureThreshold        = 4
	defaultProviderRecoverySuccesses       = 2
	defaultProviderCooldownSeconds         = 60
	defaultProviderFirstByteTimeoutSeconds = 60
	providerRouterMemoryBodyLimit          = 1 << 20
	providerRouterMaxBodySize              = 64 << 20
	providerRouterID                       = "nicecodex_router"
)

type ProviderRouterUpstream struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"baseUrl"`
	APIKey   string `json:"apiKey"`
	AuthMode string `json:"authMode"`
	Enabled  bool   `json:"enabled"`
}

type ProviderRouterConfig struct {
	Enabled                  bool                     `json:"enabled"`
	Port                     int                      `json:"port"`
	FailureThreshold         int                      `json:"failureThreshold"`
	RecoverySuccessThreshold int                      `json:"recoverySuccessThreshold"`
	CooldownSeconds          int                      `json:"cooldownSeconds"`
	FirstByteTimeoutSeconds  int                      `json:"firstByteTimeoutSeconds"`
	Upstreams                []ProviderRouterUpstream `json:"upstreams"`
	CodexApplied             bool                     `json:"codexApplied,omitempty"`
	PreviousCodexProvider    string                   `json:"previousCodexProvider,omitempty"`
}

type ProviderRouterUpstreamInput struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	BaseURL         string `json:"baseUrl"`
	APIKey          string `json:"apiKey"`
	KeepExistingKey bool   `json:"keepExistingKey"`
	ClearAPIKey     bool   `json:"clearApiKey"`
	AuthMode        string `json:"authMode"`
	Enabled         bool   `json:"enabled"`
}

type ProviderRouterSaveRequest struct {
	Enabled                  bool                          `json:"enabled"`
	Port                     int                           `json:"port"`
	FailureThreshold         int                           `json:"failureThreshold"`
	RecoverySuccessThreshold int                           `json:"recoverySuccessThreshold"`
	CooldownSeconds          int                           `json:"cooldownSeconds"`
	FirstByteTimeoutSeconds  int                           `json:"firstByteTimeoutSeconds"`
	Upstreams                []ProviderRouterUpstreamInput `json:"upstreams"`
}

type ProviderRouterUpstreamView struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	BaseURL             string `json:"baseUrl"`
	AuthMode            string `json:"authMode"`
	Enabled             bool   `json:"enabled"`
	HasAPIKey           bool   `json:"hasApiKey"`
	State               string `json:"state"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	RecoverySuccesses   int    `json:"recoverySuccesses"`
	OpenUntil           int64  `json:"openUntil"`
	LastStatus          int    `json:"lastStatus"`
	LastLatencyMs       int64  `json:"lastLatencyMs"`
	LastError           string `json:"lastError"`
	RequestCount        int64  `json:"requestCount"`
}

type ProviderRouterView struct {
	Enabled                  bool                         `json:"enabled"`
	Running                  bool                         `json:"running"`
	Port                     int                          `json:"port"`
	ListenURL                string                       `json:"listenUrl"`
	FailureThreshold         int                          `json:"failureThreshold"`
	RecoverySuccessThreshold int                          `json:"recoverySuccessThreshold"`
	CooldownSeconds          int                          `json:"cooldownSeconds"`
	FirstByteTimeoutSeconds  int                          `json:"firstByteTimeoutSeconds"`
	CodexApplied             bool                         `json:"codexApplied"`
	LastError                string                       `json:"lastError"`
	Upstreams                []ProviderRouterUpstreamView `json:"upstreams"`
}

type providerCircuitState struct {
	consecutiveFailures int
	recoverySuccesses   int
	openUntil           time.Time
	halfOpenInFlight    bool
	lastStatus          int
	lastLatency         time.Duration
	lastError           string
	requestCount        int64
}

type providerRouter struct {
	mu        sync.Mutex
	config    ProviderRouterConfig
	states    map[string]*providerCircuitState
	server    *http.Server
	listener  net.Listener
	transport *http.Transport
	lastError string
}

func defaultProviderRouterConfig() ProviderRouterConfig {
	return ProviderRouterConfig{
		Port:                     defaultProviderRouterPort,
		FailureThreshold:         defaultProviderFailureThreshold,
		RecoverySuccessThreshold: defaultProviderRecoverySuccesses,
		CooldownSeconds:          defaultProviderCooldownSeconds,
		FirstByteTimeoutSeconds:  defaultProviderFirstByteTimeoutSeconds,
		Upstreams:                []ProviderRouterUpstream{},
	}
}

func newProviderRouter() *providerRouter {
	config := defaultProviderRouterConfig()
	return &providerRouter{
		config:    config,
		states:    make(map[string]*providerCircuitState),
		transport: newProviderRouterTransport(config.FirstByteTimeoutSeconds),
	}
}

func newProviderRouterTransport(firstByteTimeoutSeconds int) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: time.Duration(firstByteTimeoutSeconds) * time.Second,
	}
}

func (r *providerRouter) configure(config ProviderRouterConfig) error {
	config = cloneProviderRouterConfig(config)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port))

	r.mu.Lock()
	if !config.Enabled {
		oldServer := r.server
		oldTransport := r.transport
		r.server = nil
		r.listener = nil
		r.states = reconcileProviderStates(r.states, r.config.Upstreams, config.Upstreams)
		r.config = config
		r.lastError = ""
		r.transport = newProviderRouterTransport(config.FirstByteTimeoutSeconds)
		r.mu.Unlock()
		shutdownProviderRouterServer(oldServer)
		if oldTransport != nil {
			oldTransport.CloseIdleConnections()
		}
		return nil
	}

	if r.listener != nil && r.listener.Addr().String() == address {
		oldTransport := r.transport
		transportChanged := r.config.FirstByteTimeoutSeconds != config.FirstByteTimeoutSeconds
		if transportChanged {
			r.transport = newProviderRouterTransport(config.FirstByteTimeoutSeconds)
		}
		r.states = reconcileProviderStates(r.states, r.config.Upstreams, config.Upstreams)
		r.config = config
		r.lastError = ""
		r.mu.Unlock()
		if oldTransport != nil && transportChanged {
			oldTransport.CloseIdleConnections()
		}
		return nil
	}
	r.mu.Unlock()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		message := fmt.Sprintf("local route %s is unavailable: %v", address, err)
		r.mu.Lock()
		// On startup there is no prior listener to preserve. Keep the saved form
		// visible so the user can change the occupied port without re-entering keys.
		if r.listener == nil {
			r.states = reconcileProviderStates(r.states, r.config.Upstreams, config.Upstreams)
			r.config = config
		}
		r.lastError = message
		r.mu.Unlock()
		return err
	}
	server := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	transport := newProviderRouterTransport(config.FirstByteTimeoutSeconds)

	r.mu.Lock()
	oldServer := r.server
	oldTransport := r.transport
	r.server = server
	r.listener = listener
	r.transport = transport
	r.states = reconcileProviderStates(r.states, r.config.Upstreams, config.Upstreams)
	r.config = config
	r.lastError = ""
	r.mu.Unlock()

	go r.serve(server, listener)
	shutdownProviderRouterServer(oldServer)
	if oldTransport != nil {
		oldTransport.CloseIdleConnections()
	}
	return nil
}

func (r *providerRouter) serve(server *http.Server, listener net.Listener) {
	err := server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	r.mu.Lock()
	if r.server == server {
		r.server = nil
		r.listener = nil
		r.lastError = err.Error()
	}
	r.mu.Unlock()
}

func shutdownProviderRouterServer(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

func (r *providerRouter) close() {
	r.mu.Lock()
	server := r.server
	transport := r.transport
	r.server = nil
	r.listener = nil
	r.mu.Unlock()
	shutdownProviderRouterServer(server)
	if transport != nil {
		transport.CloseIdleConnections()
	}
}

func (r *providerRouter) setError(message string) {
	r.mu.Lock()
	r.lastError = strings.TrimSpace(message)
	r.mu.Unlock()
}

func (r *providerRouter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet && (request.URL.Path == "/healthz" || request.URL.Path == "/nicecodex/health") {
		r.writeHealth(writer)
		return
	}
	// The router owns stored provider credentials. Browser origins must not be
	// able to turn localhost into a credentialed cross-site request proxy.
	if strings.TrimSpace(request.Header.Get("Origin")) != "" {
		http.Error(writer, "browser-origin requests are not allowed", http.StatusForbidden)
		return
	}

	config, transport := r.requestSnapshot()
	if !config.Enabled || transport == nil {
		http.Error(writer, "NiceCodex local route is disabled", http.StatusServiceUnavailable)
		return
	}

	body, err := captureProviderRequestBody(request.Body, request.ContentLength)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	defer body.Close()

	var lastFailure string
	attempted := 0
	for _, upstream := range config.Upstreams {
		if !upstream.Enabled {
			continue
		}
		acquired, halfOpenProbe := r.acquireProvider(upstream)
		if !acquired {
			continue
		}
		attempted++
		started := time.Now()
		response, roundTripErr := r.roundTrip(request, body, upstream, transport)
		latency := time.Since(started)
		if roundTripErr != nil {
			if request.Context().Err() != nil {
				if halfOpenProbe {
					r.releaseProviderProbe(upstream)
				}
				return
			}
			lastFailure = safeProviderError(roundTripErr)
			r.recordProviderFailure(upstream, 0, latency, lastFailure)
			continue
		}
		if providerRetryableStatus(response.StatusCode) {
			lastFailure = fmt.Sprintf("upstream returned HTTP %d", response.StatusCode)
			closeProviderResponse(response.Body)
			r.recordProviderFailure(upstream, response.StatusCode, latency, lastFailure)
			continue
		}

		r.recordProviderSuccess(upstream, response.StatusCode, latency)
		_ = writeProviderResponse(writer, response, upstream.ID)
		return
	}

	if attempted == 0 {
		lastFailure = "all enabled providers are cooling down"
	}
	if lastFailure == "" {
		lastFailure = "no enabled provider is available"
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Retry-After", strconv.Itoa(config.CooldownSeconds))
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{
			"type":    "provider_unavailable",
			"message": lastFailure,
		},
	})
}

func (r *providerRouter) requestSnapshot() (ProviderRouterConfig, *http.Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneProviderRouterConfig(r.config), r.transport
}

func (r *providerRouter) roundTrip(
	incoming *http.Request,
	body *providerReplayBody,
	upstream ProviderRouterUpstream,
	transport *http.Transport,
) (*http.Response, error) {
	target, err := buildProviderTargetURL(upstream.BaseURL, incoming.URL)
	if err != nil {
		return nil, err
	}
	outgoing := incoming.Clone(incoming.Context())
	outgoing.URL = target
	outgoing.RequestURI = ""
	outgoing.Host = target.Host
	outgoing.Header = incoming.Header.Clone()
	removeHopByHopHeaders(outgoing.Header)
	outgoing.Header.Del("X-NiceCodex-Upstream")
	applyProviderAuthentication(outgoing.Header, upstream)
	outgoing.Body, err = body.Open()
	if err != nil {
		return nil, err
	}
	outgoing.ContentLength = body.Size()
	return transport.RoundTrip(outgoing)
}

func (r *providerRouter) acquireProvider(upstream ProviderRouterUpstream) (bool, bool) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.config.Enabled || !providerRouterUpstreamMatches(r.config.Upstreams, upstream) {
		return false, false
	}
	state := r.states[upstream.ID]
	if state == nil {
		state = &providerCircuitState{}
		r.states[upstream.ID] = state
	}
	if state.openUntil.IsZero() {
		return true, false
	}
	if now.Before(state.openUntil) || state.halfOpenInFlight {
		return false, false
	}
	state.halfOpenInFlight = true
	return true, true
}

func (r *providerRouter) releaseProviderProbe(upstream ProviderRouterUpstream) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.config.Enabled || !providerRouterUpstreamMatches(r.config.Upstreams, upstream) {
		return
	}
	if state := r.states[upstream.ID]; state != nil {
		state.halfOpenInFlight = false
	}
}

func (r *providerRouter) recordProviderFailure(
	upstream ProviderRouterUpstream,
	status int,
	latency time.Duration,
	message string,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.config.Enabled || !providerRouterUpstreamMatches(r.config.Upstreams, upstream) {
		return
	}
	state := r.states[upstream.ID]
	if state == nil {
		state = &providerCircuitState{}
		r.states[upstream.ID] = state
	}
	wasHalfOpen := !state.openUntil.IsZero()
	state.requestCount++
	state.lastStatus = status
	state.lastLatency = latency
	state.lastError = message
	state.halfOpenInFlight = false
	state.recoverySuccesses = 0
	state.consecutiveFailures++
	if wasHalfOpen || state.consecutiveFailures >= r.config.FailureThreshold {
		state.openUntil = time.Now().Add(time.Duration(r.config.CooldownSeconds) * time.Second)
	}
}

func (r *providerRouter) recordProviderSuccess(
	upstream ProviderRouterUpstream,
	status int,
	latency time.Duration,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.config.Enabled || !providerRouterUpstreamMatches(r.config.Upstreams, upstream) {
		return
	}
	state := r.states[upstream.ID]
	if state == nil {
		state = &providerCircuitState{}
		r.states[upstream.ID] = state
	}
	state.requestCount++
	state.lastStatus = status
	state.lastLatency = latency
	state.lastError = ""
	state.halfOpenInFlight = false
	if !state.openUntil.IsZero() {
		state.recoverySuccesses++
		if state.recoverySuccesses < r.config.RecoverySuccessThreshold {
			return
		}
	}
	state.consecutiveFailures = 0
	state.recoverySuccesses = 0
	state.openUntil = time.Time{}
}

func (r *providerRouter) resetCircuits() {
	r.mu.Lock()
	r.states = preserveProviderStates(nil, r.config.Upstreams)
	r.mu.Unlock()
}

func (r *providerRouter) view() ProviderRouterView {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	view := ProviderRouterView{
		Enabled:                  r.config.Enabled,
		Running:                  r.listener != nil,
		Port:                     r.config.Port,
		ListenURL:                providerRouterListenURL(r.config.Port),
		FailureThreshold:         r.config.FailureThreshold,
		RecoverySuccessThreshold: r.config.RecoverySuccessThreshold,
		CooldownSeconds:          r.config.CooldownSeconds,
		FirstByteTimeoutSeconds:  r.config.FirstByteTimeoutSeconds,
		CodexApplied:             r.config.CodexApplied,
		LastError:                r.lastError,
		Upstreams:                make([]ProviderRouterUpstreamView, 0, len(r.config.Upstreams)),
	}
	for _, upstream := range r.config.Upstreams {
		state := r.states[upstream.ID]
		entry := ProviderRouterUpstreamView{
			ID: upstream.ID, Name: upstream.Name, BaseURL: upstream.BaseURL,
			AuthMode: upstream.AuthMode, Enabled: upstream.Enabled,
			HasAPIKey: strings.TrimSpace(upstream.APIKey) != "",
			State:     "idle",
		}
		if !upstream.Enabled {
			entry.State = "disabled"
		} else if state != nil {
			entry.ConsecutiveFailures = state.consecutiveFailures
			entry.RecoverySuccesses = state.recoverySuccesses
			if !state.openUntil.IsZero() {
				entry.OpenUntil = state.openUntil.UnixMilli()
			}
			entry.LastStatus = state.lastStatus
			entry.LastLatencyMs = state.lastLatency.Milliseconds()
			entry.LastError = state.lastError
			entry.RequestCount = state.requestCount
			switch {
			case !state.openUntil.IsZero() && now.Before(state.openUntil):
				entry.State = "open"
			case !state.openUntil.IsZero():
				entry.State = "half-open"
			case state.consecutiveFailures > 0:
				entry.State = "warning"
			case state.requestCount > 0:
				entry.State = "healthy"
			}
		}
		view.Upstreams = append(view.Upstreams, entry)
	}
	return view
}

func (r *providerRouter) configSnapshot() ProviderRouterConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneProviderRouterConfig(r.config)
}

func (r *providerRouter) updateCodexMetadata(applied bool, previous string) {
	r.mu.Lock()
	r.config.CodexApplied = applied
	r.config.PreviousCodexProvider = previous
	r.mu.Unlock()
}

func (r *providerRouter) writeHealth(writer http.ResponseWriter) {
	view := r.view()
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := http.StatusOK
	if !view.Running {
		status = http.StatusServiceUnavailable
	}
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"ok":        view.Running,
		"running":   view.Running,
		"providers": len(view.Upstreams),
	})
}

type providerReplayBody struct {
	memory []byte
	path   string
	size   int64
}

func captureProviderRequestBody(source io.ReadCloser, contentLength int64) (*providerReplayBody, error) {
	if source == nil || source == http.NoBody || contentLength == 0 {
		return &providerReplayBody{}, nil
	}
	defer source.Close()
	if contentLength > providerRouterMaxBodySize {
		return nil, fmt.Errorf("request body exceeds %d MB", providerRouterMaxBodySize>>20)
	}

	result := &providerReplayBody{}
	buffer := make([]byte, 32*1024)
	var memory bytes.Buffer
	var temp *os.File
	for {
		count, readErr := source.Read(buffer)
		if count > 0 {
			result.size += int64(count)
			if result.size > providerRouterMaxBodySize {
				if temp != nil {
					temp.Close()
					_ = os.Remove(temp.Name())
				}
				return nil, fmt.Errorf("request body exceeds %d MB", providerRouterMaxBodySize>>20)
			}
			if temp == nil && result.size <= providerRouterMemoryBodyLimit {
				_, _ = memory.Write(buffer[:count])
			} else {
				if temp == nil {
					var err error
					temp, err = os.CreateTemp("", "nicecodex-route-body-*")
					if err != nil {
						return nil, err
					}
					result.path = temp.Name()
					if _, err = temp.Write(memory.Bytes()); err != nil {
						temp.Close()
						_ = os.Remove(result.path)
						return nil, err
					}
					memory.Reset()
				}
				if _, err := temp.Write(buffer[:count]); err != nil {
					temp.Close()
					_ = os.Remove(result.path)
					return nil, err
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				if temp != nil {
					temp.Close()
					_ = os.Remove(result.path)
				}
				return nil, readErr
			}
			break
		}
	}
	if temp != nil {
		if err := temp.Close(); err != nil {
			_ = os.Remove(result.path)
			return nil, err
		}
	} else {
		result.memory = append([]byte(nil), memory.Bytes()...)
	}
	return result, nil
}

func (b *providerReplayBody) Open() (io.ReadCloser, error) {
	if b == nil || b.size == 0 {
		return http.NoBody, nil
	}
	if b.path != "" {
		return os.Open(b.path)
	}
	return io.NopCloser(bytes.NewReader(b.memory)), nil
}

func (b *providerReplayBody) Size() int64 {
	if b == nil {
		return 0
	}
	return b.size
}

func (b *providerReplayBody) Close() {
	if b == nil {
		return
	}
	b.memory = nil
	if b.path != "" {
		_ = os.Remove(b.path)
		b.path = ""
	}
}

func buildProviderTargetURL(base string, incoming *url.URL) (*url.URL, error) {
	target, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	requestPath := incoming.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	target.RawPath = joinProviderURLPath(target.EscapedPath(), requestPath)
	decodedPath, decodeErr := url.PathUnescape(target.RawPath)
	if decodeErr != nil {
		return nil, decodeErr
	}
	target.Path = decodedPath
	if target.RawQuery != "" && incoming.RawQuery != "" {
		target.RawQuery += "&" + incoming.RawQuery
	} else if incoming.RawQuery != "" {
		target.RawQuery = incoming.RawQuery
	}
	target.Fragment = ""
	return target, nil
}

// Merge the incoming API path with the upstream base path without duplicating
// shared suffixes such as /v1 in /api/v1 + /v1/responses.
func joinProviderURLPath(basePath, requestPath string) string {
	basePath = strings.TrimSuffix(basePath, "/")
	if basePath == "/" {
		basePath = ""
	}
	if requestPath == "" {
		requestPath = "/"
	}
	if basePath == "" {
		return requestPath
	}
	if requestPath == "/" {
		return basePath + "/"
	}

	end := len(requestPath)
	if end > 1 && requestPath[end-1] == '/' {
		end--
	}
	for end > 0 {
		prefix := requestPath[:end]
		if strings.HasSuffix(basePath, prefix) {
			return basePath + requestPath[end:]
		}
		next := strings.LastIndex(requestPath[:end], "/")
		if next <= 0 {
			break
		}
		end = next
	}
	return basePath + "/" + strings.TrimPrefix(requestPath, "/")
}

func applyProviderAuthentication(header http.Header, upstream ProviderRouterUpstream) {
	key := strings.TrimSpace(upstream.APIKey)
	if upstream.AuthMode == "passthrough" {
		return
	}
	header.Del("Authorization")
	header.Del("X-Api-Key")
	if key == "" {
		return
	}
	switch upstream.AuthMode {
	case "x-api-key":
		header.Set("X-Api-Key", key)
	default:
		header.Set("Authorization", "Bearer "+key)
	}
}

func providerRetryableStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden ||
		status == http.StatusRequestTimeout || status == http.StatusConflict ||
		status == http.StatusTooEarly || status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

func closeProviderResponse(body io.ReadCloser) {
	if body == nil {
		return
	}
	_ = body.Close()
}

func writeProviderResponse(writer http.ResponseWriter, response *http.Response, upstreamID string) error {
	defer response.Body.Close()
	removeHopByHopHeaders(response.Header)
	for key, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.Header().Set("X-NiceCodex-Upstream", upstreamID)
	writer.WriteHeader(response.StatusCode)

	streaming := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
	var responseController *http.ResponseController
	if streaming {
		responseController = http.NewResponseController(writer)
	}
	buffer := make([]byte, 32*1024)
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, writeErr := writer.Write(buffer[:count]); writeErr != nil {
				return writeErr
			}
			if responseController != nil {
				_ = responseController.Flush()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

var providerHopByHopHeaders = []string{
	"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopByHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, key := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(key))
		}
	}
	for _, key := range providerHopByHopHeaders {
		header.Del(key)
	}
}

func safeProviderError(err error) string {
	if err == nil {
		return ""
	}
	message := ""
	var urlError *url.Error
	if errors.As(err, &urlError) {
		message = strings.TrimSpace(urlError.Op + ": " + urlError.Err.Error())
	} else {
		message = strings.TrimSpace(err.Error())
	}
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}

func preserveProviderStates(
	current map[string]*providerCircuitState,
	upstreams []ProviderRouterUpstream,
) map[string]*providerCircuitState {
	next := make(map[string]*providerCircuitState, len(upstreams))
	for _, upstream := range upstreams {
		if state := current[upstream.ID]; state != nil {
			next[upstream.ID] = state
		} else {
			next[upstream.ID] = &providerCircuitState{}
		}
	}
	return next
}

func reconcileProviderStates(
	current map[string]*providerCircuitState,
	previous []ProviderRouterUpstream,
	nextUpstreams []ProviderRouterUpstream,
) map[string]*providerCircuitState {
	previousByID := make(map[string]ProviderRouterUpstream, len(previous))
	for _, upstream := range previous {
		previousByID[upstream.ID] = upstream
	}
	next := make(map[string]*providerCircuitState, len(nextUpstreams))
	for _, upstream := range nextUpstreams {
		before, existed := previousByID[upstream.ID]
		unchanged := existed && before.BaseURL == upstream.BaseURL && before.APIKey == upstream.APIKey &&
			before.AuthMode == upstream.AuthMode && before.Enabled == upstream.Enabled
		if unchanged && current[upstream.ID] != nil {
			next[upstream.ID] = current[upstream.ID]
		} else {
			next[upstream.ID] = &providerCircuitState{}
		}
	}
	return next
}

func providerRouterUpstreamMatches(
	upstreams []ProviderRouterUpstream,
	candidate ProviderRouterUpstream,
) bool {
	for _, upstream := range upstreams {
		if upstream.ID == candidate.ID {
			return upstream.BaseURL == candidate.BaseURL && upstream.APIKey == candidate.APIKey &&
				upstream.AuthMode == candidate.AuthMode && upstream.Enabled == candidate.Enabled
		}
	}
	return false
}

func cloneProviderRouterConfig(config ProviderRouterConfig) ProviderRouterConfig {
	config.Upstreams = append([]ProviderRouterUpstream(nil), config.Upstreams...)
	return config
}

func providerRouterListenURL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

func providerRouterConfigPath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "provider-router.json")
}

func loadProviderRouterConfig(settingsPath string) (ProviderRouterConfig, error) {
	config := defaultProviderRouterConfig()
	path := providerRouterConfigPath(settingsPath)
	if err := recoverProviderFileBackup(path); err != nil {
		return config, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, err
	}
	if err := json.Unmarshal(payload, &config); err != nil {
		return defaultProviderRouterConfig(), err
	}
	return sanitizeProviderRouterConfig(config, false)
}

func writeProviderRouterConfig(settingsPath string, config ProviderRouterConfig) error {
	path := providerRouterConfigPath(settingsPath)
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return writeProviderFileAtomic(path, payload)
}

func sanitizeProviderRouterConfig(config ProviderRouterConfig, requireEnabledProvider bool) (ProviderRouterConfig, error) {
	if config.Port == 0 {
		config.Port = defaultProviderRouterPort
	}
	if config.Port < 1024 || config.Port > 65535 {
		return ProviderRouterConfig{}, errors.New("local route port must be between 1024 and 65535")
	}
	config.FailureThreshold = clampProviderSetting(config.FailureThreshold, defaultProviderFailureThreshold, 1, 20)
	config.RecoverySuccessThreshold = clampProviderSetting(config.RecoverySuccessThreshold, defaultProviderRecoverySuccesses, 1, 10)
	config.CooldownSeconds = clampProviderSetting(config.CooldownSeconds, defaultProviderCooldownSeconds, 5, 300)
	config.FirstByteTimeoutSeconds = clampProviderSetting(config.FirstByteTimeoutSeconds, defaultProviderFirstByteTimeoutSeconds, 5, 120)
	if len(config.Upstreams) > 16 {
		return ProviderRouterConfig{}, errors.New("up to 16 route providers are supported")
	}

	seen := make(map[string]struct{}, len(config.Upstreams))
	enabled := 0
	for index := range config.Upstreams {
		upstream := &config.Upstreams[index]
		upstream.ID = strings.TrimSpace(upstream.ID)
		if upstream.ID == "" {
			upstream.ID = newUUID()
		}
		if len(upstream.ID) > 120 {
			return ProviderRouterConfig{}, errors.New("provider id is too long")
		}
		if _, exists := seen[upstream.ID]; exists {
			return ProviderRouterConfig{}, errors.New("provider ids must be unique")
		}
		seen[upstream.ID] = struct{}{}
		upstream.Name = strings.TrimSpace(upstream.Name)
		if len(upstream.Name) > 80 || strings.ContainsAny(upstream.Name, "\r\n") {
			return ProviderRouterConfig{}, errors.New("provider name is invalid")
		}
		if upstream.Name == "" {
			upstream.Name = fmt.Sprintf("Provider %d", index+1)
		}
		baseURL, err := sanitizeProviderBaseURL(upstream.BaseURL, config.Port)
		if err != nil {
			return ProviderRouterConfig{}, fmt.Errorf("%s: %w", upstream.Name, err)
		}
		upstream.BaseURL = baseURL
		upstream.APIKey = strings.TrimSpace(upstream.APIKey)
		if len(upstream.APIKey) > 4096 || strings.ContainsAny(upstream.APIKey, "\r\n") {
			return ProviderRouterConfig{}, fmt.Errorf("%s: API key is invalid", upstream.Name)
		}
		upstream.AuthMode = strings.ToLower(strings.TrimSpace(upstream.AuthMode))
		if upstream.AuthMode == "" {
			upstream.AuthMode = "bearer"
		}
		if upstream.AuthMode != "bearer" && upstream.AuthMode != "x-api-key" && upstream.AuthMode != "passthrough" {
			return ProviderRouterConfig{}, fmt.Errorf("%s: invalid authentication mode", upstream.Name)
		}
		if upstream.Enabled {
			enabled++
		}
	}
	if config.Enabled && requireEnabledProvider && enabled == 0 {
		return ProviderRouterConfig{}, errors.New("enable at least one route provider")
	}
	return config, nil
}

func sanitizeProviderBaseURL(value string, localPort int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("base URL is required")
	}
	if len(value) > 2048 {
		return "", errors.New("base URL is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("base URL must use http or https")
	}
	if parsed.User != nil {
		return "", errors.New("base URL must not contain credentials")
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "" {
		return "", errors.New("base URL must include a host")
	}
	port := parsed.Port()
	portNumber := 0
	if port != "" {
		portNumber, err = strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("base URL has an invalid port")
		}
	}
	localAddress := hostname == "localhost"
	if ip := net.ParseIP(hostname); ip != nil {
		localAddress = ip.IsLoopback() || ip.IsUnspecified()
	}
	if localAddress && portNumber == localPort {
		return "", errors.New("base URL cannot point back to the local route")
	}
	parsed.Fragment = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func clampProviderSetting(value, fallback, minimum, maximum int) int {
	if value == 0 {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func providerRouterConfigFromRequest(
	request ProviderRouterSaveRequest,
	existing ProviderRouterConfig,
) (ProviderRouterConfig, error) {
	existingKeys := make(map[string]string, len(existing.Upstreams))
	for _, upstream := range existing.Upstreams {
		existingKeys[upstream.ID] = upstream.APIKey
	}
	config := ProviderRouterConfig{
		Enabled:                  request.Enabled,
		Port:                     request.Port,
		FailureThreshold:         request.FailureThreshold,
		RecoverySuccessThreshold: request.RecoverySuccessThreshold,
		CooldownSeconds:          request.CooldownSeconds,
		FirstByteTimeoutSeconds:  request.FirstByteTimeoutSeconds,
		CodexApplied:             existing.CodexApplied,
		PreviousCodexProvider:    existing.PreviousCodexProvider,
		Upstreams:                make([]ProviderRouterUpstream, 0, len(request.Upstreams)),
	}
	for _, input := range request.Upstreams {
		key := input.APIKey
		if input.ClearAPIKey {
			key = ""
		} else if strings.TrimSpace(key) == "" && input.KeepExistingKey {
			key = existingKeys[strings.TrimSpace(input.ID)]
		}
		config.Upstreams = append(config.Upstreams, ProviderRouterUpstream{
			ID: input.ID, Name: input.Name, BaseURL: input.BaseURL,
			APIKey: key, AuthMode: input.AuthMode, Enabled: input.Enabled,
		})
	}
	return sanitizeProviderRouterConfig(config, true)
}

func (s *AppService) ReadProviderRouterConfig() ProviderRouterView {
	if s.providerRouter == nil {
		config := defaultProviderRouterConfig()
		return ProviderRouterView{
			Port: config.Port, ListenURL: providerRouterListenURL(config.Port),
			FailureThreshold:         config.FailureThreshold,
			RecoverySuccessThreshold: config.RecoverySuccessThreshold,
			CooldownSeconds:          config.CooldownSeconds,
			FirstByteTimeoutSeconds:  config.FirstByteTimeoutSeconds,
			Upstreams:                []ProviderRouterUpstreamView{},
		}
	}
	return s.providerRouter.view()
}

func (s *AppService) SaveProviderRouterConfig(request ProviderRouterSaveRequest) (ProviderRouterView, error) {
	if s.providerRouter == nil {
		return ProviderRouterView{}, errors.New("local provider route is unavailable")
	}
	previous := s.providerRouter.configSnapshot()
	config, err := providerRouterConfigFromRequest(request, previous)
	if err != nil {
		return s.providerRouter.view(), err
	}
	rollback := func(cause error) (ProviderRouterView, error) {
		var rollbackErrors []string
		if err := s.providerRouter.configure(previous); err != nil {
			rollbackErrors = append(rollbackErrors, "runtime: "+err.Error())
		}
		if err := writeProviderRouterConfig(s.settingsPath, previous); err != nil {
			rollbackErrors = append(rollbackErrors, "settings: "+err.Error())
		}
		if len(rollbackErrors) > 0 {
			return s.providerRouter.view(), fmt.Errorf("%v (rollback failed: %s)", cause, strings.Join(rollbackErrors, "; "))
		}
		return s.providerRouter.view(), cause
	}
	if err := s.providerRouter.configure(config); err != nil {
		return rollback(err)
	}
	if err := writeProviderRouterConfig(s.settingsPath, config); err != nil {
		return rollback(err)
	}
	if previous.CodexApplied && !config.Enabled {
		view, restoreErr := s.RestoreCodexProviderRoute()
		if restoreErr != nil {
			return rollback(restoreErr)
		}
		return view, nil
	}
	if config.CodexApplied && previous.Port != config.Port {
		view, applyErr := s.ApplyProviderRouterToCodex()
		if applyErr != nil {
			return rollback(applyErr)
		}
		return view, nil
	}
	return s.providerRouter.view(), nil
}

func (s *AppService) ResetProviderRouterCircuits() ProviderRouterView {
	if s.providerRouter != nil {
		s.providerRouter.resetCircuits()
		return s.providerRouter.view()
	}
	return ProviderRouterView{}
}

func (s *AppService) ApplyProviderRouterToCodex() (ProviderRouterView, error) {
	if s.providerRouter == nil {
		return ProviderRouterView{}, errors.New("local provider route is unavailable")
	}
	config := s.providerRouter.configSnapshot()
	view := s.providerRouter.view()
	if !config.Enabled || !view.Running {
		return view, errors.New("start the local provider route before applying it to Codex")
	}
	path := codexConfigPath()
	if path == "" {
		return view, errors.New("Codex config path is unavailable")
	}
	if err := recoverProviderFileBackup(path); err != nil {
		return view, err
	}
	payload, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return view, err
	}
	hadConfig := err == nil
	originalPayload := append([]byte(nil), payload...)
	text := string(payload)
	previousProvider := readTOMLString(text, "", "model_provider")
	if previousProvider == providerRouterID {
		previousProvider = config.PreviousCodexProvider
	}
	text = upsertProviderRouterTOMLValue(text, "", "model_provider", strconv.Quote(providerRouterID))
	section := "model_providers." + providerRouterID
	text = upsertProviderRouterTOMLValue(text, section, "name", strconv.Quote("NiceCodex Local Router"))
	text = upsertProviderRouterTOMLValue(text, section, "base_url", strconv.Quote(providerRouterListenURL(config.Port)+"/v1"))
	text = upsertProviderRouterTOMLValue(text, section, "wire_api", strconv.Quote("responses"))
	text = upsertProviderRouterTOMLValue(text, section, "requires_openai_auth", "false")
	if err := writeProviderFileAtomic(path, []byte(text)); err != nil {
		return view, err
	}
	config.CodexApplied = true
	config.PreviousCodexProvider = previousProvider
	if err := writeProviderRouterConfig(s.settingsPath, config); err != nil {
		rollbackErr := restoreProviderFile(path, originalPayload, hadConfig)
		if rollbackErr != nil {
			return view, fmt.Errorf("save route metadata: %v (restore Codex config: %v)", err, rollbackErr)
		}
		return view, err
	}
	s.providerRouter.updateCodexMetadata(true, previousProvider)
	return s.providerRouter.view(), nil
}

func (s *AppService) RestoreCodexProviderRoute() (ProviderRouterView, error) {
	if s.providerRouter == nil {
		return ProviderRouterView{}, errors.New("local provider route is unavailable")
	}
	config := s.providerRouter.configSnapshot()
	path := codexConfigPath()
	if path == "" {
		return s.providerRouter.view(), errors.New("Codex config path is unavailable")
	}
	if err := recoverProviderFileBackup(path); err != nil {
		return s.providerRouter.view(), err
	}
	persistRestoredMetadata := func() (ProviderRouterView, error) {
		config.CodexApplied = false
		config.PreviousCodexProvider = ""
		if err := writeProviderRouterConfig(s.settingsPath, config); err != nil {
			return s.providerRouter.view(), err
		}
		s.providerRouter.updateCodexMetadata(false, "")
		return s.providerRouter.view(), nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return persistRestoredMetadata()
		}
		return s.providerRouter.view(), err
	}
	if readTOMLString(string(payload), "", "model_provider") != providerRouterID {
		return persistRestoredMetadata()
	}
	originalPayload := append([]byte(nil), payload...)
	text := string(payload)
	if config.PreviousCodexProvider == "" {
		text = removeProviderRouterRootTOMLKey(text, "model_provider")
	} else {
		text = upsertProviderRouterTOMLValue(text, "", "model_provider", strconv.Quote(config.PreviousCodexProvider))
	}
	if err := writeProviderFileAtomic(path, []byte(text)); err != nil {
		return s.providerRouter.view(), err
	}
	view, metadataErr := persistRestoredMetadata()
	if metadataErr != nil {
		rollbackErr := writeProviderFileAtomic(path, originalPayload)
		if rollbackErr != nil {
			return s.providerRouter.view(), fmt.Errorf("save route metadata: %v (restore Codex config: %v)", metadataErr, rollbackErr)
		}
		return s.providerRouter.view(), metadataErr
	}
	return view, nil
}

func upsertProviderRouterTOMLValue(text, section, key, literal string) string {
	keyLine := key + " = " + literal
	keyPattern := `(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=.*$`
	keyRe := regexp.MustCompile(keyPattern)
	if section == "" {
		sectionIndex := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(text)
		rootEnd := len(text)
		if sectionIndex != nil {
			rootEnd = sectionIndex[0]
		}
		root := text[:rootEnd]
		if keyRe.MatchString(root) {
			return keyRe.ReplaceAllStringFunc(root, func(string) string { return keyLine }) + text[rootEnd:]
		}
		root = strings.TrimRight(root, "\r\n")
		if root != "" {
			root += "\n"
		}
		return root + keyLine + "\n\n" + strings.TrimLeft(text[rootEnd:], "\r\n")
	}

	sectionHeader := "[" + section + "]"
	sectionRe := regexp.MustCompile(`(?ms)(^\s*\[` + regexp.QuoteMeta(section) + `\]\s*\r?\n)(.*?)(?=^\s*\[|\z)`)
	if sectionRe.MatchString(text) {
		return sectionRe.ReplaceAllStringFunc(text, func(block string) string {
			parts := sectionRe.FindStringSubmatch(block)
			if len(parts) < 3 {
				return block
			}
			body := parts[2]
			if keyRe.MatchString(body) {
				body = keyRe.ReplaceAllStringFunc(body, func(string) string { return keyLine })
			} else {
				body = strings.TrimRight(body, "\r\n")
				if body != "" {
					body += "\n"
				}
				body += keyLine + "\n"
			}
			return parts[1] + body
		})
	}
	text = strings.TrimRight(text, "\r\n")
	if text != "" {
		text += "\n\n"
	}
	return text + sectionHeader + "\n" + keyLine + "\n"
}

func removeProviderRouterRootTOMLKey(text, key string) string {
	sectionIndex := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(text)
	rootEnd := len(text)
	if sectionIndex != nil {
		rootEnd = sectionIndex[0]
	}
	root := text[:rootEnd]
	keyRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=.*\r?\n?`)
	root = keyRe.ReplaceAllString(root, "")
	return strings.TrimRight(root, "\r\n") + "\n\n" + strings.TrimLeft(text[rootEnd:], "\r\n")
}

func writeProviderFileAtomic(path string, payload []byte) error {
	if err := recoverProviderFileBackup(path); err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".nicecodex-route-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(payload); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}

	backupPath := path + ".nicecodex-backup"
	_ = os.Remove(backupPath)
	hadExisting := false
	if _, statErr := os.Stat(path); statErr == nil {
		if err := os.Rename(path, backupPath); err != nil {
			return err
		}
		hadExisting = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(tempPath, path); err != nil {
		if hadExisting {
			_ = os.Rename(backupPath, path)
		}
		return err
	}
	if hadExisting {
		_ = os.Remove(backupPath)
	}
	return nil
}

func recoverProviderFileBackup(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	backupPath := path + ".nicecodex-backup"
	if _, err := os.Stat(backupPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(backupPath, path)
}

func restoreProviderFile(path string, payload []byte, existed bool) error {
	if existed {
		return writeProviderFileAtomic(path, payload)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

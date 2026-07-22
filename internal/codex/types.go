package codex

import "encoding/json"

type Status struct {
	State     string `json:"state"`
	Running   bool   `json:"running"`
	Message   string `json:"message"`
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Workspace string `json:"workspace"`
}

type Event struct {
	Type       string `json:"type"`
	Method     string `json:"method,omitempty"`
	RequestKey string `json:"requestKey,omitempty"`
	Data       any    `json:"data,omitempty"`
}

type Detection struct {
	Available bool   `json:"available"`
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Error     string `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

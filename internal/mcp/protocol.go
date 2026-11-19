package mcp

import (
	"encoding/json"
	"strings"

	"memory/internal/engine"
)

// JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Protocol versions supported by this server.
const (
	ProtocolVersion20250618 = "2025-06-18"
	ProtocolVersion20250326 = "2025-03-26"
	ProtocolVersion20241105 = "2024-11-05"
	DefaultProtocolVersion  = ProtocolVersion20250618
)

var supportedProtocolVersions = map[string]bool{
	ProtocolVersion20250618: true,
	ProtocolVersion20250326: true,
	ProtocolVersion20241105: true,
}

// NegotiateProtocolVersion selects the protocol version requested by the client
// if supported; otherwise it falls back to the newest supported version.
func NegotiateProtocolVersion(requested string) string {
	if supportedProtocolVersions[requested] {
		return requested
	}
	return DefaultProtocolVersion
}

// Instructions constants.
const (
	InstructionHeader      = "Persistent memory shared across your sessions, stored locally.\nTools: search, get, upsert, delete. The text below is the user's own global instructions file; follow it."
	MaxInstructionBytes    = 16 * 1024
	InstructionTruncSuffix = "\n…truncated; call `get` with no arguments for the full file."
)

// FormatInstructions builds the initialize instructions text from the default
// file, prepending the 2-line header and capping at 16 KiB.
func FormatInstructions(eng *engine.Engine) string {
	if eng == nil {
		return InstructionHeader
	}
	df, err := eng.DefaultFile()
	if err != nil || df.Body == "" {
		return InstructionHeader
	}
	full := InstructionHeader + "\n\n" + df.Body
	if len(full) <= MaxInstructionBytes {
		return full
	}
	targetLen := MaxInstructionBytes - len(InstructionTruncSuffix)
	if targetLen <= len(InstructionHeader) {
		return InstructionHeader + InstructionTruncSuffix
	}
	truncated := full[:targetLen]
	lastNL := strings.LastIndex(truncated, "\n")
	if lastNL > len(InstructionHeader) {
		truncated = truncated[:lastNL]
	}
	return truncated + InstructionTruncSuffix
}

// Request is an incoming JSON-RPC 2.0 message.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is an outgoing JSON-RPC 2.0 message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ClientInfo describes the connected client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeParams contains the client parameters for the initialize method.
type InitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      ClientInfo     `json:"clientInfo"`
}

// ServerCapabilities defines capabilities supported by this server.
type ServerCapabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability indicates tool support.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo describes the server identity.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the result returned by initialize.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

// Property defines a JSON Schema property.
type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Minimum     *int      `json:"minimum,omitempty"`
	Maximum     *int      `json:"maximum,omitempty"`
	Items       *Property `json:"items,omitempty"`
}

// InputSchema defines a strict JSON Schema for tool arguments.
type InputSchema struct {
	Type                 string              `json:"type"`
	Properties           map[string]Property `json:"properties"`
	Required             []string            `json:"required,omitempty"`
	AdditionalProperties bool                `json:"additionalProperties"`
}

// Tool describes an MCP tool.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// ToolsListResult is returned by tools/list.
type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

// CallToolParams contains parameters for tools/call.
type CallToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ContentItem represents a content block in a tool result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// CallToolResult represents the outcome of a tools/call request.
type CallToolResult struct {
	Content           []ContentItem `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError,omitempty"`
}

// CancelledParams holds parameters for notifications/cancelled.
type CancelledParams struct {
	RequestID any    `json:"requestId"`
	Reason    string `json:"reason,omitempty"`
}

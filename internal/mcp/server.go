package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"memory/internal/engine"
)

const (
	defaultServerVersion = "0.1.0"
	defaultServerName    = "mem"
	maxScanBufferSize    = 4 * 1024 * 1024 // 4 MiB to handle up to 1 MiB bodies
)

// Option configures the MCP server.
type Option func(*options)

type options struct {
	logger        *log.Logger
	serverVersion string
	serverName    string
}

// WithLogger sets the logger for error output. Default writes errors to os.Stderr.
func WithLogger(l *log.Logger) Option {
	return func(o *options) {
		if l != nil {
			o.logger = l
		}
	}
}

// WithServerVersion overrides the server version reported in initialize.
func WithServerVersion(v string) Option {
	return func(o *options) {
		if v != "" {
			o.serverVersion = v
		}
	}
}

type writer struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *writer) writeResponse(resp Response) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.w.Write(data)
	return err
}

func (w *writer) writeRaw(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	_, err := w.w.Write(data)
	return err
}

type session struct {
	eng           *engine.Engine
	w             *writer
	logger        *log.Logger
	serverVersion string
	serverName    string

	clientMu   sync.Mutex
	clientName string

	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc
}

func newSession(eng *engine.Engine, w *writer, opts options) *session {
	return &session{
		eng:           eng,
		w:             w,
		logger:        opts.logger,
		serverVersion: opts.serverVersion,
		serverName:    opts.serverName,
		clientName:    "mcp",
		inFlight:      make(map[string]context.CancelFunc),
	}
}

func (s *session) registerInFlight(id string, cancel context.CancelFunc) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	s.inFlight[id] = cancel
}

func (s *session) unregisterInFlight(id string) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	delete(s.inFlight, id)
}

func (s *session) cancelInFlight(id string) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	if cancel, ok := s.inFlight[id]; ok {
		cancel()
	}
}

// Serve starts the stdio MCP server on the provided in/out streams.
// It stops serving and returns nil when stdin reaches EOF.
func Serve(ctx context.Context, in io.Reader, out io.Writer, eng *engine.Engine, opts ...Option) error {
	o := options{
		logger:        log.New(os.Stderr, "[mcp] ", log.LstdFlags),
		serverVersion: defaultServerVersion,
		serverName:    defaultServerName,
	}
	for _, opt := range opts {
		opt(&o)
	}

	w := &writer{w: out}
	sess := newSession(eng, w, o)

	scanner := bufio.NewScanner(in)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxScanBufferSize)

	var wg sync.WaitGroup
	defer wg.Wait()

	serverCtx, serverCancel := context.WithCancel(ctx)
	defer serverCancel()

	for scanner.Scan() {
		line := scanner.Bytes()
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = w.writeResponse(Response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error: &RPCError{
					Code:    CodeParseError,
					Message: fmt.Sprintf("parse error: %v", err),
				},
			})
			continue
		}

		// Handle notifications (no ID)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			sess.handleNotification(req)
			continue
		}

		// Handle request concurrently
		reqIDStr := string(req.ID)
		reqCtx, reqCancel := context.WithCancel(serverCtx)
		sess.registerInFlight(reqIDStr, reqCancel)

		wg.Add(1)
		go func(r Request, c context.Context, cancel context.CancelFunc, idStr string) {
			defer wg.Done()
			defer cancel()
			defer sess.unregisterInFlight(idStr)

			sess.handleRequest(c, r)
		}(req, reqCtx, reqCancel, reqIDStr)
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	return nil
}

func (s *session) handleNotification(req Request) {
	switch req.Method {
	case "notifications/initialized":
		// Client confirmed initialization. No action needed.
	case "notifications/cancelled":
		var p CancelledParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &p); err == nil {
				idBytes, _ := json.Marshal(p.RequestID)
				s.cancelInFlight(string(idBytes))
			}
		}
	default:
		// Ignore unknown notifications silently as per JSON-RPC / MCP specs.
	}
}

func (s *session) handleRequest(ctx context.Context, req Request) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Printf("recovered panic in request %s: %v", req.Method, r)
			if req.Method == "tools/call" {
				_ = s.w.writeResponse(Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: CallToolResult{
						IsError: true,
						Content: []ContentItem{
							{
								Type: "text",
								Text: fmt.Sprintf("internal error: panic: %v", r),
							},
						},
					},
				})
			} else {
				_ = s.w.writeResponse(Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &RPCError{
						Code:    CodeInternalError,
						Message: fmt.Sprintf("internal server panic: %v", r),
					},
				})
			}
		}
	}()

	switch req.Method {
	case "initialize":
		s.handleInitialize(ctx, req)
	case "ping":
		_ = s.w.writeResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		})
	case "tools/list":
		_ = s.w.writeResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolsListResult{
				Tools: BuiltinTools(),
			},
		})
	case "tools/call":
		s.handleCallTool(ctx, req)
	default:
		_ = s.w.writeResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeMethodNotFound,
				Message: fmt.Sprintf("method %q not found", req.Method),
			},
		})
	}
}

func (s *session) handleInitialize(_ context.Context, req Request) {
	var params InitializeParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}

	clientName := params.ClientInfo.Name
	if clientName == "" {
		clientName = "mcp"
	}
	s.clientMu.Lock()
	s.clientName = clientName
	s.clientMu.Unlock()

	protoVer := NegotiateProtocolVersion(params.ProtocolVersion)
	instructions := FormatInstructions(s.eng)

	res := InitializeResult{
		ProtocolVersion: protoVer,
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{
				ListChanged: false,
			},
		},
		ServerInfo: ServerInfo{
			Name:    s.serverName,
			Version: s.serverVersion,
		},
		Instructions: instructions,
	}

	_ = s.w.writeResponse(Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  res,
	})
}

func (s *session) handleCallTool(ctx context.Context, req Request) {
	var params CallToolParams
	if len(req.Params) == 0 {
		_ = s.w.writeResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInvalidParams,
				Message: "missing params for tools/call",
			},
		})
		return
	}

	dec := json.NewDecoder(bytes.NewReader(req.Params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&params); err != nil {
		_ = s.w.writeResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("invalid tools/call params shape: %v", err),
			},
		})
		return
	}

	var result CallToolResult
	switch params.Name {
	case "search":
		result = s.executeSearch(ctx, params.Arguments)
	case "get":
		result = s.executeGet(ctx, params.Arguments)
	case "upsert":
		result = s.executeUpsert(ctx, params.Arguments)
	case "delete":
		result = s.executeDelete(ctx, params.Arguments)
	default:
		result = CallToolResult{
			IsError: true,
			Content: []ContentItem{
				{
					Type: "text",
					Text: fmt.Sprintf("unknown tool %q; available tools are: search, get, upsert, delete", params.Name),
				},
			},
		}
	}

	_ = s.w.writeResponse(Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	})
}

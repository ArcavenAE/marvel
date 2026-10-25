package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/arcavenae/marvel/internal/api"
	"github.com/arcavenae/marvel/internal/config"
	"github.com/arcavenae/marvel/internal/daemonrpc"
)

// The MCP server a read-only seat uses to hand its work over. It speaks the
// newline-delimited JSON-RPC 2.0 subset of MCP revision 2025-06-18 that one
// tool needs, on stdin and stdout, and nothing else. The design is
// docs/design/read-only-seat-handoff.md.

const (
	mcpLatestVersion = "2025-06-18"
	mcpToolName      = "submit_handoff"
	// mcpMaxLine bounds one input line. The text cap is 64 KiB; the rest is
	// JSON escaping and the envelope.
	mcpMaxLine = 512 * 1024
	// mcpPutTimeout bounds one handoff.put exchange.
	mcpPutTimeout = 30 * time.Second
)

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
)

var mcpKnownVersions = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// mcpHandoffOptions carries what the server reads from outside: the
// environment (its own, never the root flags), the exchange timeout and the
// build version reported in serverInfo.
type mcpHandoffOptions struct {
	Env     func(string) string
	Timeout time.Duration
	Version string
}

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "MCP servers marvel offers to its seats"}
	cmd.AddCommand(mcpHandoffCmd())
	return cmd
}

func mcpHandoffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "handoff",
		Short: "Serve the submit_handoff tool over MCP on stdin and stdout",
		Long: `Serve one MCP tool, submit_handoff, so a seat that cannot write a file or run
a shell command can still hand its work to its successor. The tool sends the
text to the daemon as marvel handoff put does, with the seat's own heartbeat
token, and the daemon writes the role's declared handoff file.

The server dials only the daemon socket named by its own MARVEL_SOCKET and
refuses to start with --socket, --cluster or --identity set. It is started by
the seat's harness, not by hand.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if socketPath != "" || clusterName != "" || identityPath != "" || cmd.Flags().Changed("cluster") {
				return errors.New("marvel mcp handoff dials only the MARVEL_SOCKET of its own environment; it takes no --socket, --cluster or --identity")
			}
			return serveMCPHandoff(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), mcpHandoffOptions{
				Env: os.Getenv, Timeout: mcpPutTimeout, Version: version,
			})
		},
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type rpcErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrorBody   `json:"error,omitempty"`
}

type mcpServer struct {
	out         io.Writer
	opt         mcpHandoffOptions
	initialized bool
}

// serveMCPHandoff serves the submit_handoff tool on in and out. It returns an
// error only when the input is unusable (a line over the cap) or the output
// cannot be written; a client closing stdin is a clean end.
func serveMCPHandoff(in io.Reader, out, _ io.Writer, opt mcpHandoffOptions) error {
	s := &mcpServer{out: out, opt: opt}
	br := bufio.NewReaderSize(in, 64*1024)
	for {
		line, tooLong, err := readMCPLine(br)
		if tooLong {
			if werr := s.fail(nil, rpcInvalidRequest, "line is over the 512 KiB cap"); werr != nil {
				return werr
			}
			return errors.New("input line is over the 512 KiB cap")
		}
		if len(bytes.TrimSpace(line)) > 0 {
			if werr := s.handle(line); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read stdin: %w", err)
		}
	}
}

// readMCPLine reads one line without its newline. A line over mcpMaxLine is
// reported as tooLong and its remainder is not read.
func readMCPLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		chunk, isPrefix, rerr := br.ReadLine()
		line = append(line, chunk...)
		if len(line) > mcpMaxLine {
			return nil, true, nil
		}
		if rerr != nil {
			return line, false, rerr
		}
		if !isPrefix {
			return line, false, nil
		}
	}
}

func (s *mcpServer) write(r rpcReply) error {
	r.JSONRPC = "2.0"
	if len(r.ID) == 0 {
		r.ID = json.RawMessage("null")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode reply: %w", err)
	}
	if _, err := s.out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}

func (s *mcpServer) fail(id json.RawMessage, code int, msg string) error {
	return s.write(rpcReply{ID: id, Error: &rpcErrorBody{Code: code, Message: msg}})
}

func (s *mcpServer) handle(line []byte) error {
	if !utf8.Valid(line) {
		return s.fail(nil, rpcParseError, "the line is not valid UTF-8")
	}
	if !json.Valid(line) {
		return s.fail(nil, rpcParseError, "the line is not valid JSON")
	}
	trimmed := bytes.TrimSpace(line)
	if trimmed[0] == '[' {
		return s.fail(nil, rpcInvalidRequest, "batches are not supported")
	}
	if trimmed[0] != '{' {
		return s.fail(nil, rpcInvalidRequest, "a request is a JSON object")
	}
	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return s.fail(nil, rpcInvalidRequest, "not a JSON-RPC request")
	}
	hasID := len(req.ID) > 0
	id := req.ID
	if hasID && !validRPCID(req.ID) {
		return s.fail(nil, rpcInvalidRequest, "id must be a string or a number")
	}
	if !hasID {
		id = nil
	}
	if req.JSONRPC != "2.0" {
		return s.fail(id, rpcInvalidRequest, `jsonrpc must be "2.0"`)
	}
	if req.Method == nil {
		if len(req.Result) > 0 || len(req.Error) > 0 {
			return nil // a client reply to a request this server never sends
		}
		return s.fail(id, rpcInvalidRequest, "method is required")
	}
	if !hasID {
		return nil // a notification gets no reply, whatever it is
	}
	switch *req.Method {
	case "initialize":
		return s.initialize(id, req.Params)
	case "ping":
		return s.write(rpcReply{ID: id, Result: struct{}{}})
	case "tools/list":
		if !s.initialized {
			return s.fail(id, rpcInvalidRequest, "tools/list before initialize")
		}
		return s.write(rpcReply{ID: id, Result: map[string]any{"tools": []any{mcpToolDef()}}})
	case "tools/call":
		if !s.initialized {
			return s.fail(id, rpcInvalidRequest, "tools/call before initialize")
		}
		return s.call(id, req.Params)
	default:
		return s.fail(id, rpcMethodNotFound, "method not found")
	}
}

func validRPCID(raw json.RawMessage) bool {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return false
	}
	if b[0] == '"' {
		return true
	}
	var n json.Number
	return json.Unmarshal(b, &n) == nil && b[0] != 'n'
}

func (s *mcpServer) initialize(id, params json.RawMessage) error {
	if s.initialized {
		return s.fail(id, rpcInvalidRequest, "already initialized")
	}
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return s.fail(id, rpcInvalidParams, "params must be an object")
		}
	}
	v := mcpLatestVersion
	if mcpKnownVersions[p.ProtocolVersion] {
		v = p.ProtocolVersion
	}
	s.initialized = true
	return s.write(rpcReply{ID: id, Result: map[string]any{
		"protocolVersion": v,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": "marvel-handoff", "version": s.opt.Version},
	}})
}

func mcpToolDef() map[string]any {
	return map[string]any{
		"name":  mcpToolName,
		"title": "Submit this seat's handoff",
		"description": "Hand this seat's work to its successor. marvel writes the role's declared handoff file and " +
			"appends the role's marker. Call it only when marvel asks this seat for a handoff. Pass the whole handoff as text.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "The handoff: what was done, what is left, what the successor needs. At most 65536 bytes of UTF-8.",
				},
			},
			"required":             []string{"text"},
			"additionalProperties": false,
		},
	}
}

func (s *mcpServer) call(id, params json.RawMessage) error {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return s.fail(id, rpcInvalidParams, "params must be an object with name and arguments")
	}
	if p.Name != mcpToolName {
		return s.fail(id, rpcInvalidParams, "unknown tool")
	}
	var args map[string]json.RawMessage
	if len(p.Arguments) == 0 || json.Unmarshal(p.Arguments, &args) != nil || args == nil {
		return s.fail(id, rpcInvalidParams, "arguments must be an object with exactly text")
	}
	raw, ok := args["text"]
	if len(args) != 1 || !ok || len(raw) == 0 || raw[0] != '"' {
		return s.fail(id, rpcInvalidParams, "arguments must be exactly text, a string")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return s.fail(id, rpcInvalidParams, "text must be a string")
	}
	if msg := checkHandoffText(text); msg != "" {
		return s.toolResult(id, true, msg)
	}
	msg, err := s.put(text)
	return s.toolResultOrErr(id, msg, err)
}

func (s *mcpServer) toolResultOrErr(id json.RawMessage, msg string, err error) error {
	if err != nil {
		return s.toolResult(id, true, err.Error())
	}
	return s.toolResult(id, false, msg)
}

func (s *mcpServer) toolResult(id json.RawMessage, isError bool, text string) error {
	return s.write(rpcReply{ID: id, Result: map[string]any{
		"content": []any{map[string]any{"type": "text", "text": drawn(text)}},
		"isError": isError,
	}})
}

// checkHandoffText returns why text is refused, or "" when it passes.
func checkHandoffText(text string) string {
	if len(text) > api.MaxHandoffPutBytes {
		return fmt.Sprintf("handoff text is too large: the limit is %d bytes", api.MaxHandoffPutBytes)
	}
	if strings.TrimSpace(text) == "" {
		return "handoff text is empty: pass the handoff as text"
	}
	for _, r := range text {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return "handoff text holds a control character; use plain text"
		}
	}
	return ""
}

// put sends text to the daemon the environment names, through handoffPut, and
// returns the success line. Every failure comes back as an error, and success
// only after a decoded result.
func (s *mcpServer) put(text string) (string, error) {
	sock := s.opt.Env(config.SocketEnv)
	if sock == "" {
		return "", fmt.Errorf("no daemon socket: %s is not set; this tool works only inside a seat marvel started", config.SocketEnv)
	}
	if daemonrpc.IsMRVL(sock) || daemonrpc.IsSSH(sock) {
		return "", fmt.Errorf("%s names a remote daemon; the handoff goes only to a local socket", config.SocketEnv)
	}
	token := api.HeartbeatTokenFrom(s.opt.Env)
	send := func(req daemonrpc.Request) (*daemonrpc.Response, error) {
		resp, err := daemonrpc.SendRequestWith(sock, req, daemonrpc.DialOptions{Timeout: s.opt.Timeout})
		if err != nil {
			var to *daemonrpc.TimeoutError
			if errors.As(err, &to) {
				return nil, fmt.Errorf("the daemon did not answer in time; the handoff may or may not have been written, so call again: %w", err)
			}
			return nil, err
		}
		return resp, nil
	}
	var out bytes.Buffer
	err := handoffPut(strings.NewReader(text), &out, token, send)
	if err != nil {
		return "", errors.New(maskToken(err.Error(), token))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

func maskToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[token]")
}

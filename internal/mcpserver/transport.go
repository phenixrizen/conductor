package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	MaxFrameBytes  = (1 << 20) + (64 << 10)
	MaxResultBytes = 6 << 20
	MaxJSONDepth   = 64
)

// Transport applies bounds before the SDK decodes input. SDK v1.7's stdio reader
// has no frame limit; bounding after decoding would leave allocation unbounded.
// Close owns both streams and must unblock reads and writes on disconnect.
type Transport struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
}

func (t *Transport) Connect(context.Context) (mcp.Connection, error) {
	if t.Reader == nil || t.Writer == nil {
		return nil, errors.New("stdio streams are required")
	}
	scanner := bufio.NewScanner(t.Reader)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes+1)
	return &connection{reader: t.Reader, writer: t.Writer, scanner: scanner}, nil
}

type connection struct {
	reader    io.ReadCloser
	writer    io.WriteCloser
	scanner   *bufio.Scanner
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func (c *connection) Read(ctx context.Context) (jsonrpc.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !c.scanner.Scan() {
		if c.scanner.Err() != nil {
			return nil, errors.New("MCP frame read failed or exceeded limit")
		}
		return nil, io.EOF
	}
	data := c.scanner.Bytes()
	if len(data) > MaxFrameBytes || validateJSON(data) != nil {
		return nil, errors.New("invalid or excessive MCP JSON frame")
	}
	message, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		return nil, errors.New("invalid MCP JSON-RPC message")
	}
	return message, nil
}
func (c *connection) Write(ctx context.Context, message jsonrpc.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := jsonrpc.EncodeMessage(message)
	if err != nil || len(data) > MaxResultBytes {
		return errors.New("MCP result cannot be encoded within limit")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.writer.Write(append(data, '\n'))
	return err
}
func (c *connection) Close() error {
	c.closeOnce.Do(func() { _ = c.reader.Close(); _ = c.writer.Close() })
	return nil
}
func (*connection) SessionID() string { return "" }

// Reject duplicate object keys (including inside retained content) and excessive
// nesting without exposing source text in an error. A bounded decoder token walk
// runs before the SDK's recursive unmarshalling path.
func validateJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := jsonValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func jsonValue(d *json.Decoder, depth int) error {
	if depth > MaxJSONDepth {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return errors.New("duplicate JSON object key")
			}
			keys[key] = true
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

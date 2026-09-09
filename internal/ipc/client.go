package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
)

type Client struct {
	path        string
	protocol    Protocol
	expectedUID uint32
}

func NewClient(path string, protocol Protocol) *Client {
	return &Client{path: path, protocol: protocol}
}

func (c *Client) Probe(ctx context.Context) (Health, error) {
	var health Health
	err := c.withConnection(ctx, func(conn net.Conn, reader *bufio.Reader) error {
		return exchange(conn, reader, http.MethodGet, "/ipc/v1/health", "", nil, &health)
	})
	if err == nil && (health.State != "ready" || health.Scope != "runtime-bootstrap") {
		err = errors.New("IPC_STATE_UNKNOWN")
	}
	return health, err
}

func (c *Client) Inspect(ctx context.Context, grant string) (Health, error) {
	var health Health
	err := c.withConnection(ctx, func(conn net.Conn, reader *bufio.Reader) error {
		return exchange(conn, reader, http.MethodPost, "/ipc/v1/operations/runtime.inspect", grant, InspectRequest{}, &health)
	})
	return health, err
}

// One connection, one handshake and at most one operation. No transparent retry,
// redirect or pooling can replay a request or use an unnegotiated connection.
func (c *Client) withConnection(parent context.Context, operation func(net.Conn, *bufio.Reader) error) error {
	ctx, cancel := context.WithTimeout(parent, RequestTimeout)
	defer cancel()
	conn, err := DialPeer(ctx, c.path, c.expectedUID)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	var reply HandshakeReply
	if err = exchange(conn, reader, http.MethodPost, "/ipc/v1/handshake", "", c.protocol, &reply); err != nil {
		return err
	}
	if !reply.Accepted || reply.Protocol != c.protocol {
		return &RemoteError{Code: "IPC_VERSION_MISMATCH", Status: 409}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return operation(conn, reader)
}

func exchange(conn net.Conn, reader *bufio.Reader, method, path, grant string, input, output any) error {
	var data []byte
	var err error
	if input != nil {
		data, err = json.Marshal(input)
		if err != nil {
			return errors.New("IPC_INVALID_REQUEST")
		}
	}
	request, err := http.NewRequest(method, "http://mgrd"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if grant != "" {
		request.Header.Set("Authorization", "Bearer "+grant)
	}
	if err = request.Write(conn); err != nil {
		return errors.New("IPC_TRANSPORT_FAILURE")
	}
	var header bytes.Buffer
	fragmented := false
	for {
		line, err := reader.ReadSlice('\n')
		if header.Len()+len(line) > MaxHeaderBytes {
			return errors.New("IPC_RESPONSE_TOO_LARGE")
		}
		header.Write(line)
		if err == bufio.ErrBufferFull {
			fragmented = true
			continue
		}
		if err != nil {
			return errors.New("IPC_TRANSPORT_FAILURE")
		}
		if !fragmented && bytes.Equal(line, []byte("\r\n")) {
			break
		}
		fragmented = false
	}
	bounded := io.MultiReader(bytes.NewReader(header.Bytes()), io.LimitReader(reader, MaxResponseBytes+MaxHeaderBytes))
	response, err := http.ReadResponse(bufio.NewReader(bounded), request)
	if err != nil {
		return errors.New("IPC_INVALID_RESPONSE")
	}
	defer response.Body.Close()
	if response.ProtoMajor != 1 || response.ProtoMinor != 1 {
		return errors.New("IPC_INVALID_RESPONSE")
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || response.Header.Get("Content-Encoding") != "" {
		return errors.New("IPC_INVALID_RESPONSE")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return errors.New("IPC_TRANSPORT_FAILURE")
	}
	if len(body) > MaxResponseBytes {
		return errors.New("IPC_RESPONSE_TOO_LARGE")
	}
	if response.StatusCode != 200 {
		var problem Problem
		if json.Unmarshal(body, &problem) != nil || len(problem.Code) == 0 || len(problem.Code) > 64 {
			return errors.New("IPC_INVALID_RESPONSE")
		}
		return &RemoteError{Code: problem.Code, Status: response.StatusCode}
	}
	if json.Unmarshal(body, output) != nil {
		return errors.New("IPC_INVALID_RESPONSE")
	}
	return nil
}

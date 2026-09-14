// Package esl is a minimal FreeSWITCH Event Socket (inbound) client (D-12).
// It supports authentication, blocking "api" commands, "bgapi", event
// subscription in plain format and automatic reconnection.
package esl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is one FreeSWITCH event decoded from text/event-plain.
type Event struct {
	Name    string
	Headers map[string]string
	Body    string
}

// Get returns a header value ("" when absent).
func (e Event) Get(key string) string { return e.Headers[key] }

type packet struct {
	headers map[string]string
	body    string
}

// Client is a single ESL connection. Use Dial to create one and Run for a
// supervised reconnecting loop.
type Client struct {
	addr     string
	password string
	log      *slog.Logger

	mu      sync.Mutex // serialises commands; ESL answers in order
	conn    net.Conn
	reader  *bufio.Reader
	replies chan packet
	closed  chan struct{}
	once    sync.Once

	handler func(Event)
}

// Dial connects and authenticates.
func Dial(ctx context.Context, host string, port int, password string, log *slog.Logger) (*Client, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Client{
		addr:     addr,
		password: password,
		log:      log,
		conn:     conn,
		reader:   bufio.NewReaderSize(conn, 1<<20),
		replies:  make(chan packet, 16),
		closed:   make(chan struct{}),
	}
	// First packet is the auth request.
	p, err := c.readPacket()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read auth request: %w", err)
	}
	if p.headers["Content-Type"] != "auth/request" {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected greeting %q", p.headers["Content-Type"])
	}
	if _, err := fmt.Fprintf(conn, "auth %s\n\n", password); err != nil {
		_ = conn.Close()
		return nil, err
	}
	p, err = c.readPacket()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read auth reply: %w", err)
	}
	if !strings.HasPrefix(p.headers["Reply-Text"], "+OK") {
		_ = conn.Close()
		return nil, fmt.Errorf("auth failed: %s", p.headers["Reply-Text"])
	}
	go c.readLoop()
	return c, nil
}

// OnEvent installs the event handler. Must be called before Subscribe.
func (c *Client) OnEvent(h func(Event)) { c.handler = h }

// Close terminates the connection.
func (c *Client) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.conn.Close()
}

// Done is closed when the connection is lost.
func (c *Client) Done() <-chan struct{} { return c.closed }

// API runs a blocking api command and returns its response body.
func (c *Client) API(ctx context.Context, cmd string) (string, error) {
	p, err := c.command(ctx, "api "+cmd)
	if err != nil {
		return "", err
	}
	body := strings.TrimSpace(p.body)
	if strings.HasPrefix(body, "-ERR") {
		return body, fmt.Errorf("esl: %s", body)
	}
	return body, nil
}

// BgAPI runs a background api command and returns the Job-UUID.
func (c *Client) BgAPI(ctx context.Context, cmd string) (string, error) {
	p, err := c.command(ctx, "bgapi "+cmd)
	if err != nil {
		return "", err
	}
	return p.headers["Job-UUID"], nil
}

// Subscribe registers for the given event names in plain format.
func (c *Client) Subscribe(ctx context.Context, events ...string) error {
	p, err := c.command(ctx, "event plain "+strings.Join(events, " "))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(p.headers["Reply-Text"], "+OK") {
		return fmt.Errorf("subscribe: %s", p.headers["Reply-Text"])
	}
	return nil
}

func (c *Client) command(ctx context.Context, line string) (packet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return packet{}, errors.New("esl: connection closed")
	default:
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(dl)
	}
	if _, err := io.WriteString(c.conn, line+"\n\n"); err != nil {
		c.fail(err)
		return packet{}, err
	}
	select {
	case p := <-c.replies:
		return p, nil
	case <-ctx.Done():
		return packet{}, ctx.Err()
	case <-c.closed:
		return packet{}, errors.New("esl: connection closed")
	}
}

func (c *Client) fail(err error) {
	c.once.Do(func() {
		c.log.Warn("esl connection lost", "error", err)
		close(c.closed)
		_ = c.conn.Close()
	})
}

func (c *Client) readLoop() {
	for {
		p, err := c.readPacket()
		if err != nil {
			c.fail(err)
			return
		}
		switch p.headers["Content-Type"] {
		case "text/event-plain":
			ev := parseEvent(p.body)
			if c.handler != nil {
				c.handler(ev)
			}
		case "text/disconnect-notice":
			c.fail(errors.New("disconnect notice"))
			return
		default:
			select {
			case c.replies <- p:
			case <-c.closed:
				return
			}
		}
	}
}

func (c *Client) readPacket() (packet, error) {
	h := map[string]string{}
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return packet{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		h[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	p := packet{headers: h}
	if cl := h["Content-Length"]; cl != "" {
		n, err := strconv.Atoi(cl)
		if err != nil {
			return packet{}, fmt.Errorf("bad content-length %q", cl)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.reader, buf); err != nil {
			return packet{}, err
		}
		p.body = string(buf)
	}
	return p, nil
}

// parseEvent decodes the plain event format: URL-encoded "Key: value" lines,
// an empty line, then an optional body of Content-Length bytes.
func parseEvent(raw string) Event {
	ev := Event{Headers: map[string]string{}}
	headerPart, body, _ := strings.Cut(raw, "\n\n")
	for _, line := range strings.Split(headerPart, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if dec, err := url.QueryUnescape(v); err == nil {
			v = dec
		}
		ev.Headers[strings.TrimSpace(k)] = v
	}
	ev.Body = body
	ev.Name = ev.Headers["Event-Name"]
	return ev
}

// Supervisor keeps one Client connected, re-subscribing after reconnects.
type Supervisor struct {
	host     string
	port     int
	password string
	log      *slog.Logger
	events   []string
	handler  func(Event)
	onUp     func(*Client)

	mu     sync.RWMutex
	client *Client
}

// NewSupervisor creates a reconnecting ESL supervisor.
func NewSupervisor(host string, port int, password string, log *slog.Logger) *Supervisor {
	return &Supervisor{host: host, port: port, password: password, log: log}
}

// Events sets the event subscription list and handler.
func (s *Supervisor) Events(handler func(Event), names ...string) {
	s.events = names
	s.handler = handler
}

// OnConnect registers a callback invoked after every successful (re)connect.
func (s *Supervisor) OnConnect(f func(*Client)) { s.onUp = f }

// Run blocks until ctx is cancelled, maintaining the connection.
func (s *Supervisor) Run(ctx context.Context) {
	backoff := time.Second
	for {
		c, err := Dial(ctx, s.host, s.port, s.password, s.log)
		if err != nil {
			s.log.Warn("esl connect failed", "addr", fmt.Sprintf("%s:%d", s.host, s.port), "error", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 15*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if s.handler != nil {
			c.OnEvent(s.handler)
		}
		if len(s.events) > 0 {
			if err := c.Subscribe(ctx, s.events...); err != nil {
				s.log.Warn("esl subscribe failed", "error", err)
				_ = c.Close()
				continue
			}
		}
		s.mu.Lock()
		s.client = c
		s.mu.Unlock()
		s.log.Info("esl connected", "addr", fmt.Sprintf("%s:%d", s.host, s.port))
		if s.onUp != nil {
			s.onUp(c)
		}
		select {
		case <-ctx.Done():
			_ = c.Close()
			return
		case <-c.Done():
		}
		s.mu.Lock()
		s.client = nil
		s.mu.Unlock()
	}
}

// Client returns the live client or nil when disconnected.
func (s *Supervisor) Client() *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Connected reports whether a live connection exists.
func (s *Supervisor) Connected() bool { return s.Client() != nil }

// API runs an api command on the live connection.
func (s *Supervisor) API(ctx context.Context, cmd string) (string, error) {
	c := s.Client()
	if c == nil {
		return "", errors.New("esl: not connected")
	}
	return c.API(ctx, cmd)
}

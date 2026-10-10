package nats

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nrf24l01/actfb/internal/config"
)

const (
	defaultConnectTimeout = 5 * time.Second
	defaultCheckTimeout   = 3 * time.Second
	defaultDrainTimeout   = 10 * time.Second
)

// Client is a small wrapper around a NATS connection.
//
// New establishes the initial connection immediately.
// Check performs a real round-trip to the NATS server.
// Close gracefully drains subscriptions and pending publishes.
type Client struct {
	conn *nats.Conn

	closed     chan struct{}
	closedOnce sync.Once
}

// New creates and connects a NATS client.
//
// It fails if the initial connection cannot be established. Reconnects after
// the initial connection are handled by nats.go itself.
func New(cfg config.NatsConfig) (*Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	serverURL := "nats://" + net.JoinHostPort(
		cfg.Host,
		strconv.Itoa(cfg.Port),
	)

	client := &Client{
		closed: make(chan struct{}),
	}

	options := []nats.Option{
		nats.Timeout(defaultConnectTimeout),
		nats.DrainTimeout(defaultDrainTimeout),

		nats.ClosedHandler(func(_ *nats.Conn) {
			client.closedOnce.Do(func() {
				close(client.closed)
			})
		}),
	}

	// Authentication is optional, but user/password must be provided together.
	if cfg.User != "" {
		options = append(
			options,
			nats.UserInfo(cfg.User, cfg.Password),
		)
	}

	conn, err := nats.Connect(serverURL, options...)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS %q: %w", serverURL, err)
	}

	client.conn = conn

	return client, nil
}

// Check verifies that the connection is alive by performing a real round-trip
// to the NATS server.
//
// If ctx has no deadline, a short default timeout is applied.
func (c *Client) Check(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return errors.New("NATS client is not initialized")
	}

	if c.conn.IsClosed() {
		return errors.New("NATS connection is closed")
	}

	checkCtx := ctx
	cancel := func() {}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		checkCtx, cancel = context.WithTimeout(
			ctx,
			defaultCheckTimeout,
		)
	}
	defer cancel()

	if err := c.conn.FlushWithContext(checkCtx); err != nil {
		return fmt.Errorf("check NATS connection: %w", err)
	}

	return nil
}

// Publish publishes a Core NATS message.
//
// Publish is asynchronous. Use Flush when the caller must know that all
// buffered data has reached the server.
func (c *Client) Publish(subject string, data []byte) error {
	if err := validateSubject(subject); err != nil {
		return err
	}
	if err := c.ensureUsable(); err != nil {
		return err
	}

	if err := c.conn.Publish(subject, data); err != nil {
		return fmt.Errorf("publish NATS message to %q: %w", subject, err)
	}

	return nil
}

// PublishSync publishes a message and waits for a server round-trip, ensuring
// the client's buffered writes have been processed by the server.
//
// Core NATS does not provide per-message persistence acknowledgements; use
// JetStream when that guarantee is required.
func (c *Client) PublishSync(
	ctx context.Context,
	subject string,
	data []byte,
) error {
	if err := c.Publish(subject, data); err != nil {
		return err
	}

	if err := c.Flush(ctx); err != nil {
		return fmt.Errorf(
			"flush NATS publish to %q: %w",
			subject,
			err,
		)
	}

	return nil
}

// Request performs a Core NATS request/reply operation.
func (c *Client) Request(
	ctx context.Context,
	subject string,
	data []byte,
) ([]byte, error) {
	if err := validateSubject(subject); err != nil {
		return nil, err
	}
	if err := c.ensureUsable(); err != nil {
		return nil, err
	}

	msg, err := c.conn.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf(
			"request NATS subject %q: %w",
			subject,
			err,
		)
	}

	return msg.Data, nil
}

// Subscribe creates an asynchronous subscription.
func (c *Client) Subscribe(
	subject string,
	handler nats.MsgHandler,
) (*nats.Subscription, error) {
	if err := validateSubject(subject); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("NATS message handler is nil")
	}
	if err := c.ensureUsable(); err != nil {
		return nil, err
	}

	sub, err := c.conn.Subscribe(subject, handler)
	if err != nil {
		return nil, fmt.Errorf(
			"subscribe to NATS subject %q: %w",
			subject,
			err,
		)
	}

	return sub, nil
}

// QueueSubscribe creates an asynchronous queue-group subscription.
//
// Subscribers using the same subject and queue name form a queue group;
// each message is delivered to one member of that group.
func (c *Client) QueueSubscribe(
	subject string,
	queue string,
	handler nats.MsgHandler,
) (*nats.Subscription, error) {
	if err := validateSubject(subject); err != nil {
		return nil, err
	}
	if strings.TrimSpace(queue) == "" {
		return nil, errors.New("NATS queue name is empty")
	}
	if handler == nil {
		return nil, errors.New("NATS message handler is nil")
	}
	if err := c.ensureUsable(); err != nil {
		return nil, err
	}

	sub, err := c.conn.QueueSubscribe(subject, queue, handler)
	if err != nil {
		return nil, fmt.Errorf(
			"queue subscribe to NATS subject %q, queue %q: %w",
			subject,
			queue,
			err,
		)
	}

	return sub, nil
}

// Flush performs a round-trip to the server.
//
// If ctx has no deadline, a short default timeout is applied.
func (c *Client) Flush(ctx context.Context) error {
	if err := c.ensureUsable(); err != nil {
		return err
	}

	flushCtx := ctx
	cancel := func() {}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		flushCtx, cancel = context.WithTimeout(
			ctx,
			defaultCheckTimeout,
		)
	}
	defer cancel()

	if err := c.conn.FlushWithContext(flushCtx); err != nil {
		return fmt.Errorf("flush NATS connection: %w", err)
	}

	return nil
}

// IsConnected reports whether nats.go currently considers the connection
// connected. For a real health check, prefer Check.
func (c *Client) IsConnected() bool {
	return c != nil &&
		c.conn != nil &&
		c.conn.IsConnected()
}

// Close gracefully shuts down the NATS connection.
//
// Drain stops accepting new work, drains active subscriptions and pending
// publishes, and then closes the connection. If ctx expires before draining
// completes, the connection is force-closed and ctx.Err() is returned.
func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return nil
	}

	if c.conn.IsClosed() {
		return nil
	}

	if err := c.conn.Drain(); err != nil {
		if errors.Is(err, nats.ErrConnectionClosed) {
			return nil
		}

		// Drain cannot proceed in some connection states. Ensure resources are
		// still released before returning the error.
		c.conn.Close()

		return fmt.Errorf("drain NATS connection: %w", err)
	}

	select {
	case <-c.closed:
		return nil

	case <-ctx.Done():
		c.conn.Close()

		return fmt.Errorf(
			"close NATS connection: %w",
			ctx.Err(),
		)
	}
}

// ForceClose immediately closes the connection without draining.
//
// Prefer Close for normal application shutdown.
func (c *Client) ForceClose() {
	if c == nil || c.conn == nil {
		return
	}

	c.conn.Close()
}

func (c *Client) ensureUsable() error {
	if c == nil || c.conn == nil {
		return errors.New("NATS client is not initialized")
	}

	if c.conn.IsClosed() {
		return errors.New("NATS connection is closed")
	}

	if c.conn.IsDraining() {
		return errors.New("NATS connection is draining")
	}

	return nil
}

func validateConfig(cfg config.NatsConfig) error {
	if strings.TrimSpace(cfg.Host) == "" {
		return errors.New("NATS host is empty")
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf(
			"NATS port %d is outside the valid range 1..65535",
			cfg.Port,
		)
	}

	if (cfg.User == "") != (cfg.Password == "") {
		return errors.New(
			"NATS user and password must be specified together",
		)
	}

	return nil
}

func validateSubject(subject string) error {
	if strings.TrimSpace(subject) == "" {
		return errors.New("NATS subject is empty")
	}

	if strings.ContainsAny(subject, " \t\r\n") {
		return fmt.Errorf(
			"NATS subject %q contains whitespace",
			subject,
		)
	}

	return nil
}

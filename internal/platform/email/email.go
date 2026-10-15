package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nrf24l01/actfb/internal/config"
	"github.com/wneessen/go-mail"
)

type Client struct {
	SMTP *mail.Client
	From string
}

func NewEmailClient(ctx context.Context, cfg *config.SMTPConfig) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("email config is nil")
	}

	opts := []mail.Option{
		mail.WithPort(cfg.Port),
		mail.WithTimeout(5 * time.Second),
	}

	// SMTP over TLS (465) или STARTTLS (587).
	if cfg.Port == 465 {
		opts = append(opts, mail.WithSSL())
	} else {
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory))
	}

	if cfg.User != "" || cfg.Password != "" {
		if cfg.User == "" || cfg.Password == "" {
			return nil, errors.New("incomplete SMTP credentials")
		}

		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(cfg.User),
			mail.WithPassword(cfg.Password),
		)
	}

	smtpClient, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("create email client: %w", err)
	}

	// Проверяем подключение при инициализации.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := smtpClient.DialWithContext(pingCtx); err != nil {
		return nil, fmt.Errorf("connect SMTP: %w", err)
	}

	if err := smtpClient.Close(); err != nil {
		return nil, fmt.Errorf("close SMTP check connection: %w", err)
	}

	return &Client{
		SMTP: smtpClient,
		From: cfg.From,
	}, nil
}

func (c *Client) Send(
	ctx context.Context,
	to []string,
	subject string,
	body string,
) error {
	if len(to) == 0 {
		return errors.New("email recipients are empty")
	}

	msg := mail.NewMsg()

	if err := msg.From(c.From); err != nil {
		return fmt.Errorf("set email sender: %w", err)
	}

	if err := msg.To(to...); err != nil {
		return fmt.Errorf("set email recipients: %w", err)
	}

	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextHTML, body)

	if err := c.SMTP.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}

func (c *Client) Close() error {
	return c.SMTP.Close()
}

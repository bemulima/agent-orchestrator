package tarantool

import (
	"context"
	"errors"
	driver "github.com/tarantool/go-tarantool/v2"
	"strings"
	"time"
)

type ConnectionConfig struct {
	Address, User, Password string
	RequestTimeout          time.Duration
}
type Client struct{ conn *driver.Connection }

var errUnavailable = errors.New("verification persistence unavailable")

// Connect requires the narrowly privileged runtime principal and the owner schema.
func Connect(ctx context.Context, cfg ConnectionConfig) (*Client, error) {
	if strings.TrimSpace(cfg.Address) == "" || strings.TrimSpace(cfg.User) == "" || strings.TrimSpace(cfg.Password) == "" {
		return nil, errUnavailable
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 3 * time.Second
	}
	conn, err := driver.Connect(ctx, driver.NetDialer{Address: cfg.Address, User: cfg.User, Password: cfg.Password}, driver.Opts{Timeout: cfg.RequestTimeout, Concurrency: 32})
	if err != nil {
		return nil, errUnavailable
	}
	c := &Client{conn: conn}
	if _, err = conn.Do(driver.NewPingRequest().Context(ctx)).Get(); err != nil {
		_ = c.Close()
		return nil, errUnavailable
	}
	ready, err := conn.Do(driver.NewCallRequest("auth_verification_ready").Args([]interface{}{}).Context(ctx)).Get()
	if err != nil || len(ready) == 0 || ready[0] != "ok" {
		_ = c.Close()
		return nil, errUnavailable
	}
	return c, nil
}
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}
func (c *Client) Execute(ctx context.Context, action string, args []interface{}) ([]interface{}, error) {
	switch action {
	case "signup_start", "signup_resend", "signup_verify", "signup_consume", "email_start", "email_verify", "reset_start", "reset_verify":
	default:
		return nil, errUnavailable
	}
	if c == nil || c.conn == nil {
		return nil, errUnavailable
	}
	data, err := c.conn.Do(driver.NewCallRequest("auth_verification").Args([]interface{}{action, args}).Context(ctx)).Get()
	// Driver errors may contain tuple fields; never expose them to the use case.
	if err != nil {
		return nil, errUnavailable
	}
	return data, nil
}

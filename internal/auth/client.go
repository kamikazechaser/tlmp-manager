// Package auth logs in to TrucklineMP, stores the session, and gets launch tokens.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

type (
	ClientOpts struct {
		BaseURL string
		// PollInterval defaults to 2 seconds.
		PollInterval time.Duration
	}

	Client struct {
		baseURL      string
		httpClient   *http.Client
		pollInterval time.Duration
	}

	LoginSession struct {
		SessionID string `json:"sessionId"`
		LoginURL  string `json:"loginUrl"`
	}

	User struct {
		Status   string `json:"status"`
		Username string `json:"username"`
	}
)

const (
	loginTimeout = 5 * time.Minute
)

var (
	// ErrUnauthorized means that the server rejected the session ID with HTTP 401 or 403.
	ErrUnauthorized = errors.New("session rejected by server")
	errNotFound     = errors.New("not found")
)

func NewClient(o ClientOpts) *Client {
	c := &Client{
		baseURL:      o.BaseURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		pollInterval: o.PollInterval,
	}
	if c.pollInterval <= 0 {
		c.pollInterval = 2 * time.Second
	}
	return c
}

func (c *Client) StartLogin(ctx context.Context) (*LoginSession, error) {
	var s LoginSession
	if err := c.call(ctx, http.MethodPost, "/launcher/session", "", &s); err != nil {
		return nil, fmt.Errorf("starting login: %w", err)
	}
	if s.SessionID == "" || s.LoginURL == "" {
		return nil, errors.New("starting login: incomplete response from server")
	}
	return &s, nil
}

// WaitForLogin returns the long-lived session ID and retries transient errors until loginTimeout.
func (c *Client) WaitForLogin(ctx context.Context, pendingID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	t := time.NewTicker(c.pollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", errors.New("timed out waiting for the browser login")
			}
			return "", ctx.Err()
		case <-t.C:
		}

		var s struct {
			Status    string `json:"status"`
			SessionID string `json:"sessionId"`
		}
		err := c.call(ctx, http.MethodGet, "/launcher/session/"+url.PathEscape(pendingID), "", &s)
		switch {
		case errors.Is(err, errNotFound):
			return "", errors.New("the login page expired before the login finished")
		case err != nil:
			slog.Debug("Polling login session", "err", err)
		case s.Status == "complete" && s.SessionID != "":
			return s.SessionID, nil
		}
	}
}

func (c *Client) Verify(ctx context.Context, sessionID string) (*User, error) {
	var u User
	if err := c.call(ctx, http.MethodPost, "/launcher/verify", sessionID, &u); err != nil {
		return nil, err
	}
	if u.Status != "valid" {
		return nil, fmt.Errorf("unexpected verify status %q", u.Status)
	}
	return &u, nil
}

// Play returns a session token for one game start.
func (c *Client) Play(ctx context.Context, sessionID string) (string, error) {
	var r struct {
		SessionToken string `json:"sessionToken"`
	}
	if err := c.call(ctx, http.MethodPost, "/launcher/play", sessionID, &r); err != nil {
		return "", err
	}
	if r.SessionToken == "" {
		return "", errors.New("no session token in response")
	}
	return r.SessionToken, nil
}

func (c *Client) call(ctx context.Context, method, path, sessionID string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	if sessionID != "" {
		req.Header.Set("Authorization", "Bearer "+sessionID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode/100 != 2:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

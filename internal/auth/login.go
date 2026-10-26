package auth

import (
	"context"
	"fmt"
)

// Login lets prompt open the login page, then stores the new session ID.
func Login(ctx context.Context, c *Client, sessions *Sessions, prompt func(url string) error) (string, *User, error) {
	s, err := c.StartLogin(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := prompt(s.LoginURL); err != nil {
		return "", nil, err
	}

	sessionID, err := c.WaitForLogin(ctx, s.SessionID)
	if err != nil {
		return "", nil, err
	}

	u, err := c.Verify(ctx, sessionID)
	if err != nil {
		return "", nil, fmt.Errorf("verifying new session: %w", err)
	}
	if err := sessions.Save(sessionID); err != nil {
		return "", nil, fmt.Errorf("storing session: %w", err)
	}
	return sessionID, u, nil
}

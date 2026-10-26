package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	testSession = "long-lived-test-session"
	testToken   = "test-launch-token"
)

type (
	// fakeAPI imitates the launcher API: a login completes after `pending` polls.
	fakeAPI struct {
		mu      sync.Mutex
		pending int
		polls   int
		expired bool
		status  int
	}
)

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if f.status != 0 {
		reply(f.status, map[string]string{"error": "test"})
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /launcher/session":
		reply(http.StatusOK, LoginSession{SessionID: "pending", LoginURL: "https://example.invalid/login"})
	case "GET /launcher/session/pending":
		f.polls++
		switch {
		case f.expired:
			reply(http.StatusNotFound, map[string]string{"status": "not_found"})
		case f.polls <= f.pending:
			reply(http.StatusOK, map[string]string{"status": "pending"})
		default:
			reply(http.StatusOK, map[string]string{"status": "complete", "sessionId": testSession})
		}
	case "POST /launcher/verify", "POST /launcher/play":
		if r.Header.Get("Authorization") != "Bearer "+testSession {
			reply(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if r.URL.Path == "/launcher/play" {
			reply(http.StatusOK, map[string]string{"sessionToken": testToken})
			return
		}
		reply(http.StatusOK, User{Status: "valid", Username: "driver"})
	default:
		reply(http.StatusNotFound, nil)
	}
}

func (f *fakeAPI) pollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func setup(t *testing.T, api *fakeAPI) (*Client, *Sessions) {
	t.Helper()
	keyring.MockInit()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	c := NewClient(ClientOpts{BaseURL: srv.URL, PollInterval: time.Millisecond})
	return c, NewSessions(SessionsOpts{Dir: t.TempDir()})
}

func TestLogin(t *testing.T) {
	api := &fakeAPI{pending: 2}
	c, sessions := setup(t, api)

	var opened string
	id, u, err := Login(context.Background(), c, sessions, func(url string) error {
		opened = url
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != testSession || u.Username != "driver" || opened != "https://example.invalid/login" {
		t.Errorf("Login() = %q, %+v, opened %q", id, u, opened)
	}
	if stored, err := sessions.Load(); err != nil || stored != testSession {
		t.Errorf("Load() = %q, %v; want the new session", stored, err)
	}

	token, err := c.Play(context.Background(), id)
	if err != nil || token != testToken {
		t.Errorf("Play() = %q, %v", token, err)
	}
}

func TestLoginFailures(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name      string
		api       *fakeAPI
		ctx       context.Context
		promptErr error
		wantErr   error
		wantPolls int
	}{
		{name: "login page expired", api: &fakeAPI{expired: true}, ctx: context.Background(), wantPolls: 1},
		{name: "prompt fails", api: &fakeAPI{}, ctx: context.Background(), promptErr: errors.New("no browser"), wantPolls: 0},
		{name: "cancelled", api: &fakeAPI{pending: 1000}, ctx: cancelled, wantErr: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, sessions := setup(t, tt.api)
			_, _, err := Login(tt.ctx, c, sessions, func(string) error { return tt.promptErr })
			if err == nil {
				t.Fatal("Login() succeeded")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("Login() error = %v, want %v", err, tt.wantErr)
			}
			if tt.promptErr != nil && !errors.Is(err, tt.promptErr) {
				t.Errorf("Login() error = %v, want the prompt error", err)
			}
			if got := tt.api.pollCount(); tt.ctx == context.Background() && got != tt.wantPolls {
				t.Errorf("polls = %d, want %d", got, tt.wantPolls)
			}
			if _, err := sessions.Load(); !errors.Is(err, ErrNotLoggedIn) {
				t.Errorf("Load() error = %v, want ErrNotLoggedIn", err)
			}
		})
	}
}

func TestVerifyErrors(t *testing.T) {
	c, _ := setup(t, &fakeAPI{})
	if _, err := c.Verify(context.Background(), "revoked"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify(revoked) error = %v, want ErrUnauthorized", err)
	}

	c, _ = setup(t, &fakeAPI{status: http.StatusBadGateway})
	if _, err := c.Verify(context.Background(), testSession); err == nil || errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify() on a 502 error = %v, want a transient error", err)
	}
}

// TestSessionFallbackFile covers a machine without a keyring.
func TestSessionFallbackFile(t *testing.T) {
	keyring.MockInitWithError(errors.New("no secret service"))
	sessions := NewSessions(SessionsOpts{Dir: t.TempDir()})

	if err := sessions.Save(testSession); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(sessions.file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("session file: %v, mode %v; want mode 0600", err, fi.Mode())
	}
	if id, err := sessions.Load(); err != nil || id != testSession {
		t.Errorf("Load() = %q, %v", id, err)
	}
	if err := sessions.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load() after delete: %v, want ErrNotLoggedIn", err)
	}
}

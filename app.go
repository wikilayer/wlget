package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/oauth2"
)

type credentialStore interface {
	load(origin string) (*credentials, error)
	save(origin string, c *credentials) error
	remove(origin string) error
}

type app struct {
	lockDir string
	store   credentialStore
	open    func(ctx context.Context, address string) error
	pause   func(attempt int) time.Duration
	http    *http.Client
	stdout  io.Writer
	stderr  io.Writer
}

func (a *app) get(ctx context.Context, raw string) error {
	t, err := resolve(raw)
	if err != nil {
		return err
	}
	session, err := a.session(ctx, t.origin())
	if err != nil {
		return err
	}
	attempt := 0
	for {
		body, err := session.fetch(ctx, t)
		var unavailable transientError
		if errors.As(err, &unavailable) && attempt < retryAttempts {
			wait := a.pause(attempt)
			attempt++
			fmt.Fprintf(a.stderr, "wlget: %v; trying again in %s\n", err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		if err != nil {
			return err
		}
		attempt = 0
		if t.chat {
			arrived, err := hasMessages(body)
			if err != nil {
				return fmt.Errorf("%s answered something other than chat messages: %w", t.url, err)
			}
			if !arrived {
				continue
			}
		}
		_, err = a.stdout.Write(body)
		return err
	}
}

func (a *app) logout(ctx context.Context, raw string) error {
	t, err := resolve(raw)
	if err != nil {
		return err
	}
	return a.locked(ctx, t.origin(), func() error {
		return a.store.remove(t.origin())
	})
}

func hasMessages(body []byte) (bool, error) {
	var answer struct {
		Messages *[]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return false, err
	}
	if answer.Messages == nil {
		return false, errors.New("it carries no list of messages")
	}
	return len(*answer.Messages) > 0, nil
}

type session struct {
	app    *app
	origin string
	creds  *credentials
}

func (a *app) session(ctx context.Context, origin string) (*session, error) {
	s := &session{app: a, origin: origin}
	creds, err := s.stored()
	if err != nil {
		return nil, err
	}
	s.creds = creds
	if creds == nil {
		err = a.locked(ctx, origin, func() error {
			if s.creds, err = s.stored(); err != nil || s.creds != nil {
				return err
			}
			return s.signIn(ctx)
		})
	}
	return s, err
}

func (s *session) stored() (*credentials, error) {
	creds, err := s.app.store.load(s.origin)
	if err == nil && creds != nil {
		err = creds.complete()
	}
	if err != nil {
		return nil, fmt.Errorf("reading the sign-in to %s: %w; wlget -logout %s forgets it, and the next read signs in afresh", s.origin, err, s.origin)
	}
	return creds, nil
}

func (a *app) locked(ctx context.Context, origin string, change func() error) error {
	if err := os.MkdirAll(a.lockDir, 0o700); err != nil {
		return fmt.Errorf("making a place for the lock that keeps two wlget processes from changing one sign-in: %w", err)
	}
	name := sha256.Sum256([]byte(origin))
	lock := flock.New(filepath.Join(a.lockDir, hex.EncodeToString(name[:8])+".lock"))
	if _, err := lock.TryLockContext(ctx, 50*time.Millisecond); err != nil {
		return fmt.Errorf("waiting for another wlget to finish with the sign-in to %s: %w", origin, err)
	}
	defer func() {
		if err := lock.Unlock(); err != nil {
			fmt.Fprintf(a.stderr, "Releasing the sign-in lock for %s failed: %v\n", origin, err)
		}
	}()
	return change()
}

func (s *session) signIn(ctx context.Context) error {
	meta, err := s.app.discover(ctx, s.origin)
	if err != nil {
		return err
	}
	if meta == nil {
		s.creds = nil
		return nil
	}
	s.creds, err = s.app.signIn(ctx, s.origin, meta)
	return err
}

const retryAttempts = 8

type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }

func (e transientError) Unwrap() error { return e.err }

type notTheDocument struct{ reason string }

func (e notTheDocument) Error() string { return e.reason }

func backoff(attempt int) time.Duration {
	return min(time.Second<<attempt, 30*time.Second)
}

func (s *session) fetch(ctx context.Context, t target) ([]byte, error) {
	resp, err := s.send(ctx, t)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && s.creds != nil {
		s.app.closeBody(resp)
		if err := s.replaceRefused(ctx, s.creds.Token.AccessToken); err != nil {
			return nil, err
		}
		if resp, err = s.send(ctx, t); err != nil {
			return nil, err
		}
	}
	defer s.app.closeBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, transientError{refusal(http.MethodGet, t.url.String(), resp)}
	}
	return nil, refusal(http.MethodGet, t.url.String(), resp)
}

func (s *session) send(ctx context.Context, t target) (*http.Response, error) {
	resp, err := s.request(ctx, t)
	if err != nil && ctx.Err() == nil && worthRetrying(err) {
		return nil, transientError{err}
	}
	return resp, err
}

func worthRetrying(err error) bool {
	var unreachable *url.Error
	var refused notTheDocument
	var unknownHost *net.DNSError
	var untrusted *tls.CertificateVerificationError
	var misnamed x509.HostnameError
	switch {
	case !errors.As(err, &unreachable), errors.As(err, &refused), errors.As(err, &untrusted), errors.As(err, &misnamed):
		return false
	case errors.As(err, &unknownHost):
		return !unknownHost.IsNotFound
	}
	return true
}

func (s *session) request(ctx context.Context, t target) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "wlget/"+buildVersion())
	if s.creds != nil {
		token, err := s.token(ctx)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := *s.app.http
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !t.answeredBy(next.URL) {
			return notTheDocument{fmt.Sprintf("%s sent wlget on to %s, which is not the document asked for", t.url, next.URL)}
		}
		if len(via) >= 10 {
			return notTheDocument{fmt.Sprintf("%s redirected ten times without arriving", t.url)}
		}
		return nil
	}
	return client.Do(req)
}

func (s *session) token(ctx context.Context) (string, error) {
	if s.creds.Token.Valid() {
		return s.creds.Token.AccessToken, nil
	}
	err := s.app.locked(ctx, s.origin, func() error {
		stored, err := s.stored()
		if err != nil {
			return err
		}
		if stored == nil {
			return s.signIn(ctx)
		}
		s.creds = stored
		if stored.Token.Valid() {
			return nil
		}
		return s.renew(ctx)
	})
	if err != nil {
		return "", err
	}
	if s.creds == nil {
		return "", nil
	}
	return s.creds.Token.AccessToken, nil
}

func (s *session) renew(ctx context.Context) error {
	held := s.creds.Token
	fresh, err := s.creds.config().TokenSource(context.WithValue(ctx, oauth2.HTTPClient, s.app.http), held).Token()
	var refused *oauth2.RetrieveError
	if errors.As(err, &refused) && refused.ErrorCode == "invalid_grant" {
		if err := s.app.store.remove(s.origin); err != nil {
			return fmt.Errorf("forgetting the sign-in %s no longer renews: %w", s.origin, err)
		}
		return s.signIn(ctx)
	}
	if err != nil {
		return fmt.Errorf("renewing the sign-in to %s: %w", s.origin, err)
	}
	s.creds.Token = fresh
	if err := s.app.store.save(s.origin, s.creds); err != nil {
		return fmt.Errorf("keeping the renewed sign-in to %s: %w", s.origin, err)
	}
	return nil
}

func (s *session) replaceRefused(ctx context.Context, refused string) error {
	return s.app.locked(ctx, s.origin, func() error {
		stored, err := s.stored()
		if err != nil {
			return err
		}
		if stored != nil && stored.Token.AccessToken != refused {
			s.creds = stored
			return nil
		}
		if err := s.app.store.remove(s.origin); err != nil {
			return fmt.Errorf("forgetting the sign-in %s no longer takes: %w", s.origin, err)
		}
		return s.signIn(ctx)
	})
}

func refusal(method, address string, resp *http.Response) error {
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return fmt.Errorf("%s %s: %s", method, address, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("%s %s: %s, and its explanation broke off: %w", method, address, resp.Status, err)
	}
	return fmt.Errorf("%s %s: %s: %s", method, address, resp.Status, strings.TrimSpace(string(body)))
}

func (a *app) closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		fmt.Fprintf(a.stderr, "Closing the answer from %s failed after it was read: %v\n", resp.Request.URL, err)
	}
}

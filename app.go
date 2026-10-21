package main

import (
	"bytes"
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
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/oauth2"
)

type credentialStore interface {
	describe() string
	load(origin string) (*credentials, error)
	save(origin string, c *credentials) error
	remove(origin string) error
}

type app struct {
	lockDir      string
	store        credentialStore
	signInWith   signInChoice
	onMessage    func(context.Context, []byte) error
	follow       bool
	execInterval time.Duration
	until        time.Time
	lastLook     time.Duration
	open         func(ctx context.Context, address string) error
	pause        func(attempt int) time.Duration
	http         *http.Client
	stdout       io.Writer
	stderr       io.Writer
}

func (a *app) get(ctx context.Context, raw string) error {
	t, err := resolve(raw)
	if err != nil {
		return err
	}
	if a.onMessage != nil && !t.chat {
		return errors.New("-exec runs a command for chat messages, not for a page")
	}
	if a.follow && !t.chat {
		return fmt.Errorf("-follow keeps waiting on a chat address, and %s is not one: a page is printed once", raw)
	}
	if !a.until.IsZero() && !t.chat {
		return fmt.Errorf("-until and -timeout end a wait on a chat address, and %s is not one: a page answers at once", raw)
	}
	session, err := a.session(ctx, t.origin())
	if err != nil {
		return err
	}
	waiting := ctx
	if !a.until.IsZero() {
		end := a.until
		if !end.After(time.Now()) {
			end = time.Now().Add(a.lastLook)
		}
		var stop context.CancelFunc
		waiting, stop = context.WithDeadline(ctx, end)
		defer stop()
	}
	printed := false
	attempt := 0
	for {
		body, err := session.fetch(waiting, t)
		if err != nil && a.deadlinePassed(ctx, waiting) {
			return a.reportDeadline(t, printed)
		}
		var unavailable transientError
		if errors.As(err, &unavailable) && attempt < retryAttempts {
			wait := a.pause(attempt)
			attempt++
			fmt.Fprintf(a.stderr, "wlget: %v; trying again in %s\n", err, wait)
			select {
			case <-time.After(wait):
			case <-waiting.Done():
				if a.deadlinePassed(ctx, waiting) {
					return a.reportDeadline(t, printed)
				}
				return ctx.Err()
			}
			continue
		}
		if err != nil {
			return err
		}
		attempt = 0
		if t.chat {
			arrived, err := t.chatArrived(body)
			if err != nil {
				return fmt.Errorf("%s answered something other than chat messages: %w", t.url, err)
			}
			if !arrived {
				continue
			}
		}
		if _, err := a.stdout.Write(body); err != nil {
			return err
		}
		if a.onMessage != nil {
			if err := a.onMessage(waiting, body); err != nil {
				seq := t.url.Query().Get("after_seq")
				if seq == "" {
					seq = "0"
				}
				fmt.Fprintf(a.stderr, "wlget: command did not deliver this batch; restart with after_seq=%s\n", seq)
				return fmt.Errorf("chat command failed: %w", err)
			}
		}
		if !a.follow {
			return nil
		}
		printed = true
		next, err := t.lastShown(body)
		if err != nil {
			return fmt.Errorf("%s answered chat messages without saying where to wait from next: %w", t.url, err)
		}
		t = t.after(next)
		more, err := t.moreWaiting(body)
		if err != nil {
			return err
		}
		interval := a.execInterval
		if end, ok := waiting.Deadline(); ok {
			interval = min(interval, time.Until(end)-a.lastLook)
		}
		if a.onMessage != nil && interval > 0 && !more {
			select {
			case <-time.After(interval):
			case <-waiting.Done():
				if a.deadlinePassed(ctx, waiting) {
					return a.reportDeadline(t, printed)
				}
				return ctx.Err()
			}
		}
	}
}

func (t target) moreWaiting(body []byte) (bool, error) {
	if path.Ext(t.url.Path) == ".md" {
		return bytes.HasSuffix(body, []byte("\nMore messages are waiting already.\n")), nil
	}
	var answer struct {
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return false, err
	}
	return answer.HasMore, nil
}

type deadlineReached struct{ until time.Time }

func (e deadlineReached) Error() string {
	return "no messages by " + e.until.UTC().Format(time.RFC3339)
}

func (a *app) deadlinePassed(ctx, waiting context.Context) bool {
	return ctx.Err() == nil && errors.Is(waiting.Err(), context.DeadlineExceeded)
}

func (a *app) reportDeadline(t target, printed bool) error {
	reached := deadlineReached{a.until}
	seq := t.url.Query().Get("after_seq")
	if seq == "" {
		seq = "0"
	}
	if path.Ext(t.url.Path) == ".md" {
		none := "no messages"
		if printed {
			none = "no more messages"
		}
		if _, err := fmt.Fprintf(a.stdout, "%s by %s\n\nnext after_seq %s\n", none, a.until.UTC().Format(time.RFC3339), seq); err != nil {
			return err
		}
		return reached
	}
	next, err := strconv.ParseInt(seq, 10, 64)
	if err != nil {
		return fmt.Errorf("%s has an after_seq that is not a number: %w", t.url, err)
	}
	answer := struct {
		Messages     []json.RawMessage `json:"messages"`
		Reason       string            `json:"reason"`
		Deadline     string            `json:"deadline"`
		NextAfterSeq int64             `json:"next_after_seq"`
	}{[]json.RawMessage{}, "deadline", a.until.UTC().Format(time.RFC3339), next}
	if err := json.NewEncoder(a.stdout).Encode(answer); err != nil {
		return err
	}
	return reached
}

var nextAfterLine = regexp.MustCompile(`(?m)^next after_seq ([0-9]+)$`)

func (t target) lastShown(body []byte) (string, error) {
	if path.Ext(t.url.Path) == ".md" {
		found := nextAfterLine.FindAllSubmatch(body, -1)
		if len(found) == 0 {
			return "", errors.New("the text has no line next after_seq N")
		}
		return string(found[len(found)-1][1]), nil
	}
	var answer struct {
		Messages []struct {
			Seq int64 `json:"seq"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", err
	}
	var last int64
	for _, m := range answer.Messages {
		last = max(last, m.Seq)
	}
	return fmt.Sprint(last), nil
}

func (t target) after(seq string) target {
	next := *t.url
	query := next.Query()
	query.Set("after_seq", seq)
	next.RawQuery = query.Encode()
	t.url = &next
	return t
}

func (a *app) logout(ctx context.Context, raw string) error {
	server, err := serverAddress(raw)
	if err != nil {
		return err
	}
	origin := target{url: server}.origin()
	return a.locked(ctx, origin, func() error {
		held, unreadable := a.store.load(origin)
		if unreadable == nil && held == nil {
			fmt.Fprintf(a.stderr, "wlget: %s keeps no sign-in to %s, so there was nothing to forget\n", a.store.describe(), origin)
			return nil
		}
		if err := a.store.remove(origin); err != nil {
			return err
		}
		fmt.Fprintf(a.stderr, "wlget: forgot the sign-in to %s from %s\n", origin, a.store.describe())
		return nil
	})
}

func (t target) chatArrived(body []byte) (bool, error) {
	if path.Ext(t.url.Path) != ".md" {
		return hasMessages(body)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return false, nil
	}
	if !chatTextStart.Match(body) {
		return false, errors.New("the text does not start with a message line such as #12 · session · topic")
	}
	return true, nil
}

var chatTextStart = regexp.MustCompile(`^#[0-9]+ · `)

func (t target) olderServerHint() string {
	if !t.chat || path.Ext(t.url.Path) != ".md" {
		return ""
	}
	return "; Wikilayer 1.2.0 and earlier have no text view of the chat, so ask such a server for chat.json"
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
	free, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("taking the lock on the sign-in to %s: %w", origin, err)
	}
	if !free {
		fmt.Fprintf(a.stderr, "wlget: waiting for another wlget that is signing in to %s or renewing its sign-in; "+
			"if it showed a code, approving that code lets this one go on too\n", origin)
		if _, err := lock.TryLockContext(ctx, 50*time.Millisecond); err != nil {
			return fmt.Errorf("waiting for another wlget to finish with the sign-in to %s: %w", origin, err)
		}
		fmt.Fprintf(a.stderr, "wlget: the other wlget is done with the sign-in to %s, going on\n", origin)
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
	case http.StatusNotFound:
		if hint := t.olderServerHint(); hint != "" {
			return nil, fmt.Errorf("%w%s", refusal(http.MethodGet, t.url.String(), resp), hint)
		}
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
			return notTheDocument{fmt.Sprintf("%s sent wlget on to %s, which is not the document asked for%s", t.url, next.URL, t.olderServerHint())}
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
	if errors.As(err, &refused) && refused.Response != nil {
		switch refused.Response.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return transientError{fmt.Errorf("renewing the sign-in to %s: %s", s.origin, refused.Response.Status)}
		}
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

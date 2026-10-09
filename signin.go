package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const signInWait = 5 * time.Minute

type credentials struct {
	ClientID    string        `json:"client_id"`
	RedirectURL string        `json:"redirect_url"`
	AuthURL     string        `json:"auth_url"`
	TokenURL    string        `json:"token_url"`
	Token       *oauth2.Token `json:"token"`
}

func (c *credentials) encode() (string, error) {
	text, err := json.Marshal(c)
	return string(text), err
}

func decodeCredentials(text string) (*credentials, error) {
	var c credentials
	if err := json.Unmarshal([]byte(text), &c); err != nil {
		return nil, fmt.Errorf("the saved sign-in does not read back: %w", err)
	}
	if err := c.complete(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *credentials) complete() error {
	if c.ClientID == "" || c.RedirectURL == "" || c.AuthURL == "" || c.TokenURL == "" ||
		c.Token == nil || c.Token.AccessToken == "" {
		return errors.New("the saved sign-in is missing its client, its endpoints or its token")
	}
	return nil
}

func (c *credentials) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:    c.ClientID,
		RedirectURL: c.RedirectURL,
		Scopes:      []string{"mcp"},
		Endpoint: oauth2.Endpoint{
			AuthURL:   c.AuthURL,
			TokenURL:  c.TokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

type serverMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

func (a *app) discover(ctx context.Context, origin string) (*serverMetadata, error) {
	address := origin + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking %s how to sign in: %w", origin, err)
	}
	defer a.closeBody(resp)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, refusal(http.MethodGet, address, resp)
	}
	var meta serverMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("%s describes its sign-in in a form wlget cannot read: %w", address, err)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.RegistrationEndpoint == "" {
		return nil, fmt.Errorf("%s names no authorization, token or registration endpoint", address)
	}
	return &meta, nil
}

func (a *app) signIn(ctx context.Context, origin string, meta *serverMetadata) (_ *credentials, err error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening a port on 127.0.0.1 for the browser to come back to: %w", err)
	}
	redirect := fmt.Sprintf("http://%s/callback", listener.Addr())
	clientID, err := a.register(ctx, meta.RegistrationEndpoint, redirect)
	if err != nil {
		return nil, errors.Join(err, listener.Close())
	}
	creds := &credentials{
		ClientID:    clientID,
		RedirectURL: redirect,
		AuthURL:     meta.AuthorizationEndpoint,
		TokenURL:    meta.TokenEndpoint,
	}
	config := creds.config()
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()

	codes := make(chan callback, 1)
	callbackServer := &http.Server{
		Handler:           callbackHandler(state, codes, a.stderr),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := callbackServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			codes <- callback{err: fmt.Errorf("waiting for the browser on %s: %w", redirect, err)}
		}
	}()
	defer func() { err = errors.Join(err, callbackServer.Close()) }()

	address := config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	fmt.Fprintf(a.stderr, "Sign in to %s in the browser:\n  %s\n", origin, address)
	if err := a.open(ctx, address); err != nil {
		fmt.Fprintf(a.stderr, "The browser did not open (%v); open the address above yourself.\n", err)
	}

	var got callback
	select {
	case got = <-codes:
	case <-time.After(signInWait):
		return nil, fmt.Errorf("nobody finished signing in to %s within %s", origin, signInWait)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if got.err != nil {
		return nil, got.err
	}

	token, err := config.Exchange(context.WithValue(ctx, oauth2.HTTPClient, a.http), got.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("trading the sign-in for a token at %s: %w", meta.TokenEndpoint, err)
	}
	creds.Token = token
	if err := a.store.save(origin, creds); err != nil {
		return nil, fmt.Errorf("keeping the sign-in to %s: %w", origin, err)
	}
	fmt.Fprintf(a.stderr, "Signed in to %s.\n", origin)
	return creds, nil
}

func (a *app) register(ctx context.Context, endpoint, redirect string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"client_name":                "wlget",
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("registering wlget at %s: %w", endpoint, err)
	}
	defer a.closeBody(resp)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", refusal(http.MethodPost, endpoint, resp)
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil || registered.ClientID == "" {
		return "", fmt.Errorf("%s registered wlget without saying under which client_id", endpoint)
	}
	return registered.ClientID, nil
}

type callback struct {
	code string
	err  error
}

func callbackHandler(state string, codes chan<- callback, stderr io.Writer) http.Handler {
	var mu sync.Mutex
	answered := false
	tell := func(w http.ResponseWriter, status int, page string) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, page); err != nil {
			fmt.Fprintf(stderr, "The browser left before it was told how the sign-in went: %v\n", err)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != state {
			tell(w, http.StatusBadRequest, "This page does not belong to the sign-in wlget is waiting for.\n")
			return
		}
		mu.Lock()
		first := !answered
		answered = true
		mu.Unlock()
		if !first {
			tell(w, http.StatusConflict, "wlget already has its answer for this sign-in, so this one was not used.\n")
			return
		}

		var got callback
		switch {
		case q.Get("error") != "":
			got.err = fmt.Errorf("the server refused the sign-in: %s %s", q.Get("error"), q.Get("error_description"))
		case q.Get("code") == "":
			got.err = errors.New("the browser came back without a sign-in code")
		default:
			got.code = q.Get("code")
		}
		if got.err != nil {
			tell(w, http.StatusBadRequest, got.err.Error()+"\n")
		} else {
			tell(w, http.StatusOK, "Signed in. You can close this tab and return to the terminal.\n")
		}
		codes <- got
	})
}

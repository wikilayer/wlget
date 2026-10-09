package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type fakeServer struct {
	*httptest.Server
	t                  *testing.T
	signIn             bool
	discoveryRedirects bool
	mu                 sync.Mutex
	clients            map[string]string
	codes              map[string]string
	access             map[string]bool
	refresh            map[string]bool
	issued             int
	keepAccess         bool
	lastAccess         string
	signIns            atomic.Int32
	refreshes          atomic.Int32
	pages              map[string]string
	chatPages          []string
	chatAsked          atomic.Int32
	chatStatus         int
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{
		t:       t,
		signIn:  true,
		clients: map[string]string{},
		codes:   map[string]string{},
		access:  map[string]bool{},
		refresh: map[string]bool{},
		pages:   map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", f.metadata)
	mux.HandleFunc("POST /oauth/register", f.register)
	mux.HandleFunc("GET /oauth/authorize", f.authorize)
	mux.HandleFunc("POST /oauth/token", f.token)
	mux.HandleFunc("GET /chat", f.chat)
	mux.HandleFunc("GET /", f.page)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) metadata(w http.ResponseWriter, r *http.Request) {
	if f.discoveryRedirects {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if !f.signIn {
		http.NotFound(w, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                 f.URL,
		"authorization_endpoint": f.URL + "/oauth/authorize",
		"token_endpoint":         f.URL + "/oauth/token",
		"registration_endpoint":  f.URL + "/oauth/register",
	})
}

func (f *fakeServer) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	require.NoError(f.t, json.NewDecoder(r.Body).Decode(&body))
	require.Len(f.t, body.RedirectURIs, 1)
	f.mu.Lock()
	id := fmt.Sprintf("client-%d", len(f.clients)+1)
	f.clients[id] = body.RedirectURIs[0]
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"client_id": id})
}

func (f *fakeServer) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	redirect, known := f.clients[q.Get("client_id")]
	f.mu.Unlock()
	if !known || redirect != q.Get("redirect_uri") {
		http.Error(w, "redirect_uri is not registered for this client", http.StatusBadRequest)
		return
	}
	assert.Equal(f.t, "mcp", q.Get("scope"))
	assert.Equal(f.t, "S256", q.Get("code_challenge_method"))
	f.signIns.Add(1)
	code := fmt.Sprintf("code-%d", f.signIns.Load())
	f.mu.Lock()
	f.codes[code] = q.Get("client_id")
	f.mu.Unlock()
	back, err := url.Parse(redirect)
	require.NoError(f.t, err)
	back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (f *fakeServer) token(w http.ResponseWriter, r *http.Request) {
	require.NoError(f.t, r.ParseForm())
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		if f.codes[r.PostFormValue("code")] != r.PostFormValue("client_id") || r.PostFormValue("code_verifier") == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(f.codes, r.PostFormValue("code"))
	case "refresh_token":
		if !f.refresh[r.PostFormValue("refresh_token")] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_grant"})
			return
		}
		delete(f.refresh, r.PostFormValue("refresh_token"))
		f.refreshes.Add(1)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type"})
		return
	}
	f.issued++
	access, refresh := fmt.Sprintf("access-%d", f.issued), fmt.Sprintf("refresh-%d", f.issued)
	if f.keepAccess && r.PostFormValue("grant_type") == "refresh_token" {
		access = f.lastAccess
	}
	f.lastAccess = access
	f.access[access] = true
	f.refresh[refresh] = true
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    3600,
		"refresh_token": refresh,
	})
}

func (f *fakeServer) authorized(r *http.Request) bool {
	if !f.signIn {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
}

func (f *fakeServer) page(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_token"})
		return
	}
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>front page</html>"))
		return
	}
	body, ok := f.pages[r.URL.Path]
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if strings.HasPrefix(body, "->") {
		http.Redirect(w, r, strings.TrimPrefix(body, "->"), http.StatusMovedPermanently)
		return
	}
	_, _ = w.Write([]byte(body))
}

func (f *fakeServer) chat(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_token"})
		return
	}
	if f.chatStatus != 0 {
		writeJSON(w, f.chatStatus, map[string]any{"error": "account_chat_disabled"})
		return
	}
	asked := int(f.chatAsked.Add(1))
	_, _ = w.Write([]byte(f.chatPages[min(asked, len(f.chatPages))-1]))
}

func (f *fakeServer) revokeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.access = map[string]bool{}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type memoryStore struct {
	mu    sync.Mutex
	saved map[string]*credentials
}

func (m *memoryStore) load(origin string) (*credentials, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saved[origin], nil
}

func (m *memoryStore) save(origin string, c *credentials) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved[origin] = c
	return nil
}

func (m *memoryStore) remove(origin string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.saved, origin)
	return nil
}

type harness struct {
	app    *app
	store  *memoryStore
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	opened atomic.Int32
}

func newHarness(t *testing.T) *harness {
	h := &harness{
		store:  &memoryStore{saved: map[string]*credentials{}},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	h.app = &app{
		store:  h.store,
		stdout: h.stdout,
		stderr: h.stderr,
		http:   &http.Client{Timeout: 10 * time.Second},
		open: func(_ context.Context, address string) error {
			h.opened.Add(1)
			resp, err := http.Get(address)
			if err != nil {
				return err
			}
			return resp.Body.Close()
		},
	}
	return h
}

func (h *harness) get(t *testing.T, address string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return h.app.get(ctx, address)
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

func TestGet_SignsInOnceThenReadsWithTheSavedToken(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)

	require.NoError(t, h.get(t, server.URL+"/me/notes"))
	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	assert.Equal(t, "# Notes\n# Notes\n", h.stdout.String())
	assert.Equal(t, int32(1), h.opened.Load(), "the second read found its token and asked nobody to sign in")
	assert.NotNil(t, h.store.saved[server.URL], "the token is kept under the server it came from")
	assert.Contains(t, h.stderr.String(), server.URL+"/oauth/authorize",
		"an agent's terminal shows the sign-in address, because the browser may open on a screen nobody is looking at")
}

func TestGet_RefreshesAnExpiredTokenWithoutAskingAgain(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, server.URL+"/me/notes"))
	saved := h.store.saved[server.URL]
	saved.Token.Expiry = time.Now().Add(-time.Minute)

	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	assert.Equal(t, int32(1), server.refreshes.Load())
	assert.Equal(t, int32(1), h.opened.Load())
	assert.NotEqual(t, "access-1", h.store.saved[server.URL].Token.AccessToken,
		"the server rotates the refresh token, so the one just spent is worthless and the new pair must be kept")
}

func TestGet_SignsInAgainWhenTheServerNoLongerTakesTheToken(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, server.URL+"/me/notes"))
	server.revokeAll()

	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	assert.Equal(t, int32(2), h.opened.Load())
	assert.Equal(t, "# Notes\n# Notes\n", h.stdout.String())
}

func TestGet_ReadsAServerThatTakesNoSignIn(t *testing.T) {
	server := newFakeServer(t)
	server.signIn = false
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)

	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	assert.Equal(t, "# Notes\n", h.stdout.String())
	assert.Zero(t, h.opened.Load(), "a server of your own on this computer has nobody to sign in")
}

func TestGet_NamesAServerThatRedirectsTheSignInQuestion(t *testing.T) {
	server := newFakeServer(t)
	server.discoveryRedirects = true
	h := newHarness(t)

	err := h.get(t, server.URL+"/me/notes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect",
		"followed, the redirect lands on the front page and the reader is told the HTML did not parse, which says nothing of what went wrong")
	assert.Zero(t, h.opened.Load())
}

func TestGet_FollowsARenamedPage(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes/12.md"] = "->/me/notes/12-new-title.md"
	server.pages["/me/notes/12-new-title.md"] = "# New title\n"
	h := newHarness(t)

	require.NoError(t, h.get(t, server.URL+"/me/notes/12"))

	assert.Equal(t, "# New title\n", h.stdout.String())
}

func TestGet_RefusesAPageTheServerDoesNotHave(t *testing.T) {
	server := newFakeServer(t)
	h := newHarness(t)

	err := h.get(t, server.URL+"/me/missing")

	require.Error(t, err)
	assert.Empty(t, h.stdout.String(), "the site's front page is not the page that was asked for")
	assert.Contains(t, err.Error(), server.URL+"/me/missing.md")
}

func TestGet_ReportsARefusalWithWhatTheServerSaid(t *testing.T) {
	server := newFakeServer(t)
	server.chatStatus = http.StatusForbidden
	h := newHarness(t)

	err := h.get(t, server.URL+"/chat?after_seq=0")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "account_chat_disabled")
}

func TestGet_ChatWaitsUntilSomethingArrives(t *testing.T) {
	server := newFakeServer(t)
	empty := `{"messages":[],"latest_seq":7,"has_more":false}`
	arrived := `{"messages":[{"seq":8,"body":"Ready"}],"latest_seq":8,"has_more":false}`
	server.chatPages = []string{empty, empty, arrived}
	h := newHarness(t)

	require.NoError(t, h.get(t, server.URL+"/chat?after_seq=7"))

	assert.Equal(t, int32(3), server.chatAsked.Load(),
		"an empty answer means the server's wait ran out, and the reader asked to be told when something comes")
	assert.JSONEq(t, arrived, h.stdout.String())
}

func TestGet_RefusesAChatAnswerWithoutMessages(t *testing.T) {
	for _, body := range []string{`{}`, `{"messages":null}`, `{"error":"x"}`} {
		t.Run(body, func(t *testing.T) {
			server := newFakeServer(t)
			server.chatPages = []string{body}
			h := newHarness(t)

			err := h.get(t, server.URL+"/chat?after_seq=0")

			require.Error(t, err, "taken for an empty wait, it would be asked again for ever and the agent would never hear why")
			assert.Equal(t, int32(1), server.chatAsked.Load())
		})
	}
}

func TestGet_KeepsARefreshTokenThatChangedAlone(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, server.URL+"/me/notes"))
	server.keepAccess = true
	h.store.saved[server.URL].Token.Expiry = time.Now().Add(-time.Minute)

	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	assert.Equal(t, "refresh-2", h.store.saved[server.URL].Token.RefreshToken,
		"the old refresh token is spent, and keeping it means signing in again at the next renewal")
}

func TestGet_ExplainsADamagedSavedSignIn(t *testing.T) {
	server := newFakeServer(t)
	h := newHarness(t)
	h.store.saved[server.URL] = &credentials{ClientID: "c"}

	err := h.get(t, server.URL+"/me/notes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), server.URL)
	assert.Contains(t, err.Error(), "-logout")
}

func TestLogout_ForgetsTheServersToken(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	require.NoError(t, h.app.logout(server.URL+"/me/notes"))

	assert.Nil(t, h.store.saved[server.URL])
}

func TestCredentials_SurviveTheRoundTripThroughText(t *testing.T) {
	saved := &credentials{
		ClientID:    "client-1",
		RedirectURL: "http://127.0.0.1:5000/callback",
		AuthURL:     "https://wikilayer.example/oauth/authorize",
		TokenURL:    "https://wikilayer.example/oauth/token",
		Token:       &oauth2.Token{AccessToken: "a", RefreshToken: "r", Expiry: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)},
	}

	text, err := saved.encode()
	require.NoError(t, err)
	back, err := decodeCredentials(text)
	require.NoError(t, err)

	assert.Equal(t, saved.ClientID, back.ClientID)
	assert.Equal(t, saved.RedirectURL, back.RedirectURL)
	assert.Equal(t, saved.AuthURL, back.AuthURL)
	assert.Equal(t, saved.TokenURL, back.TokenURL)
	assert.Equal(t, saved.Token.AccessToken, back.Token.AccessToken)
	assert.Equal(t, saved.Token.RefreshToken, back.Token.RefreshToken)
	assert.True(t, saved.Token.Expiry.Equal(back.Token.Expiry))
}

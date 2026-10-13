package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func tokenFor(access string) *oauth2.Token {
	return &oauth2.Token{AccessToken: access, TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}
}

func deviceHarness(t *testing.T, server *fakeServer, answer func(h *harness) string) *harness {
	t.Helper()
	server.device = true
	h := newHarness(t)
	h.app.signInWith = signInChoice{methodDevice, "this is an SSH session"}
	server.deviceAnswer = func(string) string { return answer(h) }
	return h
}

func TestSignIn_FromAnotherDeviceShowsTheCodeBeforeItWaits(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := deviceHarness(t, server, func(h *harness) string {
		if strings.Contains(h.stderr.String(), "BCDF-GHJK") {
			return ""
		}
		return "authorization_pending"
	})

	require.NoError(t, h.get(t, server.URL+"/me/notes"),
		"the approval came only once the code was printed, so wlget waited for it without ever showing it")
	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	assert.Equal(t, "# Notes\n# Notes\n", h.stdout.String())
	assert.Zero(t, h.opened.Load(), "wlget tried a browser on a machine it was told has none")
	assert.Equal(t, int32(1), server.signIns.Load(), "the second read found the sign-in and asked for another code")
	shown := h.stderr.String()
	assert.Contains(t, shown, server.URL+"/device?code=BCDF-GHJK")
	assert.Contains(t, shown, "this is an SSH session", "the agent cannot tell the person why no browser opened")
	saved := h.store.saved[server.URL]
	require.NotNil(t, saved)
	assert.NotEmpty(t, saved.Token.RefreshToken, "a device signed in once has to sign in again every hour")
}

func TestSignIn_FromAnotherDeviceSaysWhenItWasDenied(t *testing.T) {
	server := newFakeServer(t)
	h := deviceHarness(t, server, func(*harness) string { return "access_denied" })

	err := h.get(t, server.URL+"/me/notes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied on the approval page")
	assert.Nil(t, h.store.saved[server.URL])
}

func TestSignIn_FromAnotherDeviceSaysWhenTheCodeRanOut(t *testing.T) {
	server := newFakeServer(t)
	h := deviceHarness(t, server, func(*harness) string { return "expired_token" })

	err := h.get(t, server.URL+"/me/notes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run wlget again for a new code")
}

func TestSignIn_FromAnotherDeviceNamesTheWayOutOnAServerThatDoesNotOfferIt(t *testing.T) {
	server := newFakeServer(t)
	h := deviceHarness(t, server, func(*harness) string { return "" })
	server.device = false

	err := h.get(t, server.URL+"/me/notes")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "offers no sign-in from another device")
	assert.Contains(t, err.Error(), "-signin=browser")
}

func TestSignIn_ARenewedDeviceSignInKeepsWorking(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := deviceHarness(t, server, func(*harness) string { return "" })
	require.NoError(t, h.get(t, server.URL+"/me/notes"))

	server.revokeAll()
	saved := h.store.saved[server.URL]
	saved.Token.Expiry = time.Now().Add(-time.Minute)
	require.NoError(t, h.get(t, server.URL+"/me/notes"),
		"a sign-in made without a browser carries no redirect address, and renewing it must not need one")
	assert.Equal(t, int32(1), server.refreshes.Load())
}

func TestChooseSignIn_SaysWhyItChoseAndTakesWhatItIsTold(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	for _, c := range []struct {
		name   string
		asked  string
		vars   map[string]string
		goos   string
		method signInMethod
		reason string
	}{
		{"an SSH session", "", map[string]string{"SSH_CONNECTION": "1 2 3 4"}, "darwin", methodDevice, "SSH session"},
		{"a terminal reached through SSH", "", map[string]string{"SSH_TTY": "/dev/pts/0"}, "linux", methodDevice, "SSH session"},
		{"Linux with no display", "", nil, "linux", methodDevice, "no graphical display"},
		{"Linux with Wayland", "", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "linux", methodBrowser, "browser can open"},
		{"a Mac", "", nil, "darwin", methodBrowser, "browser can open"},
		{"the flag over an SSH session", "browser", map[string]string{"SSH_CONNECTION": "1 2 3 4"}, "linux", methodBrowser, "-signin=browser"},
		{"the variable", "", map[string]string{"WLGET_SIGNIN": "device"}, "darwin", methodDevice, "WLGET_SIGNIN=device"},
		{"the flag over the variable", "browser", map[string]string{"WLGET_SIGNIN": "device"}, "darwin", methodBrowser, "-signin=browser"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := chooseSignIn(c.asked, env(c.vars), c.goos)
			require.NoError(t, err)
			assert.Equal(t, c.method, got.method)
			assert.Contains(t, got.reason, c.reason)
		})
	}

	_, err := chooseSignIn("phone", env(nil), "linux")
	assert.ErrorContains(t, err, "-signin=phone is neither browser nor device")
}

func TestChooseStore_TakesOnlyWhatItKnows(t *testing.T) {
	none := func(string) string { return "" }
	store, err := chooseStore("", none, "linux")
	require.NoError(t, err)
	assert.IsType(t, keyringStore{}, store, "with nothing asked, the sign-in goes to the keychain")

	store, err = chooseStore("", func(name string) string { return map[string]string{"WLGET_STORE": "file"}[name] }, "linux")
	require.NoError(t, err)
	assert.IsType(t, fileStore{}, store)

	_, err = chooseStore("disk", none, "linux")
	assert.ErrorContains(t, err, "-store=disk is neither keychain nor file")

	_, err = chooseStore("file", none, "windows")
	assert.ErrorContains(t, err, "on Windows wlget cannot make a file so",
		"Windows reports every writable file as readable by all, so a file store there is refused on its second read")
}

func TestLogout_SaysWhetherItForgotAnything(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)

	require.NoError(t, h.app.logout(t.Context(), server.URL+"/"))
	assert.Contains(t, h.stderr.String(), "nothing to forget",
		"a logout from a store that never held the sign-in looks the same as one that removed it")

	require.NoError(t, h.get(t, server.URL+"/me/notes"))
	require.NoError(t, h.app.logout(t.Context(), server.URL+"/"))
	assert.Contains(t, h.stderr.String(), "forgot the sign-in to "+server.URL)
}

func TestLogout_ForgetsASignInThatNoLongerReads(t *testing.T) {
	server := newFakeServer(t)
	server.pages["/me/notes.md"] = "# Notes\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, server.URL+"/me/notes"))
	h.store.unreadable = errors.New("the saved sign-in does not read back")

	require.NoError(t, h.app.logout(t.Context(), server.URL+"/"),
		"a read that cannot use its sign-in sends the reader to -logout, and -logout refusing it leaves them nowhere to go")

	assert.Nil(t, h.store.saved[server.URL])
	assert.Contains(t, h.stderr.String(), "forgot the sign-in to "+server.URL)
}

func TestLocked_SaysWhatItWaitsFor(t *testing.T) {
	h := newHarness(t)
	origin := "https://wikilayer.example"
	inside, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = h.app.locked(t.Context(), origin, func() error {
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside

	done := make(chan error, 1)
	go func() { done <- h.app.locked(t.Context(), origin, func() error { return nil }) }()
	require.Eventually(t, func() bool { return strings.Contains(h.stderr.String(), "waiting for another wlget") },
		5*time.Second, 10*time.Millisecond, "a second wlget waits silently while the first one waits for a code to be approved")
	close(release)
	require.NoError(t, <-done)
	assert.Contains(t, h.stderr.String(), "is done with the sign-in",
		"without a word that the wait for the lock is over, a quiet chat wait after it reads as a wlget stuck on the lock")
}

func TestKeychainFailure_NamesTheFileStore(t *testing.T) {
	err := keychainUnavailable(os.ErrNotExist)
	assert.ErrorContains(t, err, "WLGET_STORE=file")
	assert.ErrorIs(t, err, os.ErrNotExist, "the keychain's own reason is lost")
}

func TestFileStore_KeepsTheSignInWhereOnlyItsOwnerReadsIt(t *testing.T) {
	store := fileStore{dir: filepath.Join(t.TempDir(), "wlget", "signins")}
	origin := "https://wikilayer.example"
	creds := &credentials{ClientID: "client-1", TokenURL: origin + "/oauth/token", Token: tokenFor("access-1")}

	none, err := store.load(origin)
	require.NoError(t, err)
	assert.Nil(t, none, "a server never signed in to reads as no sign-in rather than a failure")

	require.NoError(t, store.save(origin, creds))
	dir, err := os.Stat(store.dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
	file, err := os.Stat(store.path(origin))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), file.Mode().Perm(), "another account on the machine can read the sign-in")

	back, err := store.load(origin)
	require.NoError(t, err)
	require.NotNil(t, back)
	assert.Equal(t, "access-1", back.Token.AccessToken)

	leftovers, err := filepath.Glob(filepath.Join(store.dir, ".signin-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "a staged copy of the sign-in was left beside the file")

	require.NoError(t, store.remove(origin))
	gone, err := store.load(origin)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

func TestFileStore_RefusesASignInOthersCanRead(t *testing.T) {
	store := fileStore{dir: t.TempDir()}
	origin := "https://wikilayer.example"
	require.NoError(t, store.save(origin, &credentials{ClientID: "c", TokenURL: "t", Token: tokenFor("a")}))
	require.NoError(t, os.Chmod(store.path(origin), 0o644))

	_, err := store.load(origin)

	assert.ErrorContains(t, err, "chmod 600")
}

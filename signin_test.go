package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callbackRequest(t *testing.T, handler http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?"+query, nil))
	return rec
}

func TestCallback_AStrangersVisitDoesNotSpoilTheSignIn(t *testing.T) {
	codes := make(chan callback, 1)
	handler := callbackHandler("right", codes, &bytes.Buffer{})

	stray := callbackRequest(t, handler, "state=wrong&code=x")
	real := callbackRequest(t, handler, "state=right&code=good")

	assert.Equal(t, http.StatusBadRequest, stray.Code)
	assert.Equal(t, http.StatusOK, real.Code)
	got := <-codes
	require.NoError(t, got.err, "an old tab or a stray request came first, and the sign-in the reader is doing still has to land")
	assert.Equal(t, "good", got.code)
}

func TestCallback_TakesOneAnswerAndSaysSoToTheRest(t *testing.T) {
	codes := make(chan callback, 1)
	handler := callbackHandler("right", codes, &bytes.Buffer{})

	var wg sync.WaitGroup
	answers := make([]*httptest.ResponseRecorder, 2)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = callbackRequest(t, handler, "state=right&code=c")
		}()
	}
	wg.Wait()

	signedIn := 0
	for _, answer := range answers {
		if answer.Code == http.StatusOK {
			signedIn++
		}
	}
	assert.Equal(t, 1, signedIn, "a tab told it signed in whose code is never traded tells its reader something false")
	assert.Len(t, codes, 1)
}

func TestCallback_ARefusalFromTheServerEndsTheSignIn(t *testing.T) {
	codes := make(chan callback, 1)
	handler := callbackHandler("right", codes, &bytes.Buffer{})

	rec := callbackRequest(t, handler, "state=right&error=access_denied")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	got := <-codes
	require.Error(t, got.err)
	assert.Contains(t, got.err.Error(), "access_denied")
}

func TestCredentials_RefuseARecordMissingWhatASignInNeeds(t *testing.T) {
	for _, text := range []string{
		`{}`,
		`{"client_id":"c","auth_url":"a","token_url":"t","redirect_url":"r"}`,
		`{"client_id":"c","auth_url":"a","token_url":"t","redirect_url":"r","token":{}}`,
		`not json`,
	} {
		t.Run(text, func(t *testing.T) {
			_, err := decodeCredentials(text)

			assert.Error(t, err, "a damaged keychain entry would otherwise read as a sign-in and fail later with a nil token")
		})
	}
}

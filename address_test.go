package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_NamesWhatToFetch(t *testing.T) {
	for _, tc := range []struct {
		given, fetch string
		chat         bool
	}{
		{"wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules", "https://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.md", false},
		{"wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.md", "https://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.md", false},
		{"wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.json", "https://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.json", false},
		{"wikilayer://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules.html", "https://wikilayer.org/smee-again/wikilayer-howto/en/55269-wiki-rules", false},
		{"https://wikilayer.org/smee-again/codestyle", "https://wikilayer.org/smee-again/codestyle.md", false},
		{"https://wikilayer.org/smee-again/codestyle/", "https://wikilayer.org/smee-again/codestyle.md", false},
		{"https://wikilayer.org/smee-again/codestyle/1027#block-48710", "https://wikilayer.org/smee-again/codestyle/1027.md", false},
		{"https://wikilayer.org/alpha/test.io", "https://wikilayer.org/alpha/test.io.md", false},
		{"wikilayer://wiki.example.com:8443/team/notes", "https://wiki.example.com:8443/team/notes.md", false},
		{"wikilayer://localhost:38731/me/notes", "http://localhost:38731/me/notes.md", false},
		{"wikilayer://127.0.0.1:38731/me/notes", "http://127.0.0.1:38731/me/notes.md", false},
		{"http://localhost:38731/me/notes", "http://localhost:38731/me/notes.md", false},
		{"wikilayer://wikilayer.org/chat?after_seq=12&exclude_topic=novel/inner-wind", "https://wikilayer.org/chat?after_seq=12&exclude_topic=novel/inner-wind", true},
		{"wikilayer://wikilayer.org/s/images/2982/77d11b31f8b2a085.jpg", "https://wikilayer.org/s/images/2982/77d11b31f8b2a085.jpg", false},
	} {
		t.Run(tc.given, func(t *testing.T) {
			target, err := resolve(tc.given)

			require.NoError(t, err)
			assert.Equal(t, tc.fetch, target.url.String())
			assert.Equal(t, tc.chat, target.chat)
		})
	}
}

func TestResolve_KeepsCredentialsPerServer(t *testing.T) {
	cloud, err := resolve("wikilayer://wikilayer.org/smee-again/codestyle")
	require.NoError(t, err)
	local, err := resolve("wikilayer://localhost:38731/me/notes")
	require.NoError(t, err)

	assert.Equal(t, "https://wikilayer.org", cloud.origin())
	assert.Equal(t, "http://localhost:38731", local.origin())
}

func TestResolve_RefusesWhatNamesNoDocument(t *testing.T) {
	for _, given := range []string{
		"",
		"wikilayer.org/smee-again/codestyle",
		"ftp://wikilayer.org/smee-again/codestyle",
		"wikilayer:///smee-again/codestyle",
		"wikilayer://wikilayer.org",
		"wikilayer://wikilayer.org/",
	} {
		t.Run(given, func(t *testing.T) {
			_, err := resolve(given)

			assert.Error(t, err)
		})
	}
}

func TestAnswersTheSameDocument(t *testing.T) {
	page, err := resolve("wikilayer://wikilayer.org/smee-again/codestyle/1027")
	require.NoError(t, err)
	chat, err := resolve("wikilayer://wikilayer.org/chat?after_seq=1")
	require.NoError(t, err)
	picture, err := resolve("wikilayer://wikilayer.org/s/images/2982/77d11b31f8b2a085.jpg")
	require.NoError(t, err)

	for _, tc := range []struct {
		target   target
		location string
		same     bool
	}{
		{page, "https://wikilayer.org/smee-again/codestyle/1027-makefile.md", true},
		{page, "https://wikilayer.org/", false},
		{page, "https://wikilayer.org/login?next=x", false},
		{page, "https://elsewhere.example/smee-again/codestyle/1027.md", false},
		{page, "https://wikilayer.org/smee-again/codestyle/1027-makefile.json", false},
		{chat, "https://wikilayer.org/chat?after_seq=1", true},
		{chat, "https://wikilayer.org/chat?after_seq=0", false},
		{chat, "https://wikilayer.org/", false},
		{picture, "https://wikilayer.org/s/images/2982/77d11b31f8b2a085.jpg", true},
		{picture, "https://wikilayer.org/", false},
	} {
		t.Run(tc.location, func(t *testing.T) {
			assert.Equal(t, tc.same, tc.target.answeredBy(mustParse(t, tc.location)),
				"a redirect the server makes for a page it does not have lands on its front page, and printing that would pass the site's HTML off as the page asked for")
		})
	}
}

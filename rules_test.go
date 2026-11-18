package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRules_PageReadPassesTheValuesToTheServer(t *testing.T) {
	s := newFakeServer(t)
	s.signIn = false
	s.pages["/me/wiki/page.md"] = "the body\n"
	h := newHarness(t)
	h.app.agreed = []string{"12345678", "87654321"}
	require.NoError(t, h.get(t, s.URL+"/me/wiki/page"))
	assert.Equal(t, "the body\n", h.stdout.String())
	assert.Equal(t, []string{"rules_read_and_agreed=12345678%2C87654321"}, s.pageQueries)
}

func TestRules_PageReadWithoutValuesSendsNone(t *testing.T) {
	s := newFakeServer(t)
	s.signIn = false
	s.pages["/me/wiki/page.md"] = "the body\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, s.URL+"/me/wiki/page"))
	assert.Equal(t, []string{""}, s.pageQueries)
}

func TestRules_ChatAddressCarriesNoValues(t *testing.T) {
	s := newFakeServer(t)
	s.signIn = false
	s.chatPages = []string{"#1 · writer · general · 2026-10-19 20:00 UTC\n\nhello\n\nnext after_seq 1\n"}
	h := newHarness(t)
	h.app.agreed = []string{"12345678"}
	require.NoError(t, h.get(t, s.URL+"/chat?after_seq=0"))
	require.NotEmpty(t, s.chatQueries)
	for _, query := range s.chatQueries {
		assert.NotContains(t, query, "rules_read_and_agreed")
	}
}

func TestRules_ReadingRulesHandsOutNoValue(t *testing.T) {
	s := newFakeServer(t)
	s.signIn = false
	s.pages["/me/wiki/rules.md"] = "<!-- page:rules -->\n# Rules\n\nRead the sources.\n"
	h := newHarness(t)
	require.NoError(t, h.get(t, s.URL+"/me/wiki/rules.md"))
	assert.Contains(t, h.stdout.String(), "Read the sources.")
	assert.NotContains(t, h.stderr.String(), "rules_read_and_agreed")
	assert.NotContains(t, h.stderr.String(), "-agree")
	_, err := os.ReadDir(filepath.Join(h.app.lockDir, "rules"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRules_ServerRefusalReachesTheReader(t *testing.T) {
	s := newFakeServer(t)
	s.signIn = false
	s.pages["/me/wiki/page.md"] = "the body\n"
	s.pageStatus = http.StatusForbidden
	s.pageRefusal = "this read is bound by rules you have not signed. Read them through MCP: get_page_markdown(wiki_id=7, special_role='rules')."
	h := newHarness(t)
	err := h.get(t, s.URL+"/me/wiki/page")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get_page_markdown(wiki_id=7, special_role='rules')")
	assert.Empty(t, h.stdout.String())
}

func TestRun_RefusesMalformedRulesValues(t *testing.T) {
	for _, value := range []string{"yes", "0", "1,,2", "../secret"} {
		h := newHarness(t)
		assert.Equal(t, 2, run([]string{"-rules-read-and-agreed", value, "https://example.com/wiki"}, h.stdout, h.stderr))
		assert.Contains(t, h.stderr.String(), "-rules-read-and-agreed")
		assert.Empty(t, h.stdout.String())
	}
}

func TestRun_AgreeFlagIsGone(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, run([]string{"-agree", "12345678", "https://example.com/wiki"}, h.stdout, h.stderr))
	assert.Empty(t, h.stdout.String())
}

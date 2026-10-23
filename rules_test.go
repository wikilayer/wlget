package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rulesServer(t *testing.T) *fakeServer {
	t.Helper()
	s := newFakeServer(t)
	s.signIn = false
	s.pages["/me/wiki/rules.md"] = "<!-- page:rules -->\n# Rules\n\nRead the sources.\n"
	s.pages["/me/manual/rules.md"] = "<!-- page:rules -->\n# Rules\n\nKeep every citation.\n"
	return s
}

func readRulesCode(t *testing.T, h *harness, address string) string {
	t.Helper()
	require.NoError(t, h.get(t, address))
	assert.Contains(t, h.stdout.String(), "<!-- page:rules -->")
	return issuedRulesCode(t, h)
}

func issuedRulesCode(t *testing.T, h *harness) string {
	t.Helper()
	values := regexp.MustCompile(`-agree ([0-9]+)`).FindAllStringSubmatch(h.stderr.String(), -1)
	require.NotEmpty(t, values, "reading the rules must tell the caller which value signs their agreement")
	return values[len(values)-1][1]
}

func TestRules_ParallelReadsKeepBothIssuedValues(t *testing.T) {
	s := rulesServer(t)
	s.pages["/me/wiki/page.md"] = "the body\n"
	s.pageHeaders["/me/wiki/page.md"] = http.Header{"X-Wikilayer-Rules-Page": {s.URL + "/me/wiki/rules.md"}}
	first, second := newHarness(t), newHarness(t)
	second.app.lockDir = first.app.lockDir
	done := make(chan error, 2)
	for _, h := range []*harness{first, second} {
		go func() { done <- h.get(t, s.URL+"/me/wiki/rules.md") }()
	}
	require.NoError(t, <-done)
	require.NoError(t, <-done)
	firstCode, secondCode := issuedRulesCode(t, first), issuedRulesCode(t, second)
	assert.NotEqual(t, firstCode, secondCode)
	first.app.agreed = []string{firstCode}
	second.app.agreed = []string{secondCode}
	require.NoError(t, first.get(t, s.URL+"/me/wiki/page"))
	require.NoError(t, second.get(t, s.URL+"/me/wiki/page"))
}

func TestRules_RefreshingTheSignInStillRequiresAgreement(t *testing.T) {
	s := rulesServer(t)
	s.signIn = true
	s.pages["/me/wiki/page.md"] = "the body\n"
	h := newHarness(t)
	code := readRulesCode(t, h, s.URL+"/me/wiki/rules.md")
	s.pageHeaders["/me/wiki/page.md"] = http.Header{"X-Wikilayer-Rules-Page": {s.URL + "/me/wiki/rules.md"}}
	h.store.saved[s.URL].Token.Expiry = time.Now().Add(-time.Minute)
	h.stdout.Reset()
	require.Error(t, h.get(t, s.URL+"/me/wiki/page"))
	assert.Equal(t, int32(1), s.refreshes.Load())
	assert.Empty(t, h.stdout.String())
	h.app.agreed = []string{code}
	require.NoError(t, h.get(t, s.URL+"/me/wiki/page"))
}

func TestRules_RefusesTheBodyWithoutAnExplicitAgreement(t *testing.T) {
	s := rulesServer(t)
	for _, suffix := range []string{".md", ".json", ".html"} {
		path := "/me/wiki/page" + strings.TrimSuffix(suffix, ".html")
		s.pages[path] = "the private body"
		s.pageHeaders[path] = http.Header{"X-Wikilayer-Rules-Page": {s.URL + "/me/wiki/rules.md"}}
		h := newHarness(t)
		before := s.pageAsked.Load()
		err := h.get(t, s.URL+"/me/wiki/page"+suffix)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "/me/wiki/rules.md")
		assert.Contains(t, err.Error(), "-agree")
		assert.Empty(t, h.stdout.String())
		assert.Equal(t, before+1, s.pageAsked.Load(), "discovering rules must make no extra HTTP request")
	}
}

func TestRules_AllRequiredPagesMustBeReadAndExplicitlySigned(t *testing.T) {
	s := rulesServer(t)
	s.pages["/me/wiki/page.md"] = "the body\n"
	s.pageHeaders["/me/wiki/page.md"] = http.Header{"X-Wikilayer-Rules-Page": {
		s.URL + "/me/wiki/rules.md", s.URL + "/me/manual/rules.md",
	}}
	h := newHarness(t)
	own := readRulesCode(t, h, s.URL+"/me/wiki/rules.md")
	h.stdout.Reset()
	require.Error(t, h.get(t, s.URL+"/me/wiki/page"), "reading rules is not agreement")
	h.app.agreed = []string{own}
	require.Error(t, h.get(t, s.URL+"/me/wiki/page"), "the manual needs its own agreement")
	manual := readRulesCode(t, h, s.URL+"/me/manual/rules.md")
	h.stdout.Reset()
	h.app.agreed = []string{own, manual}
	require.NoError(t, h.get(t, s.URL+"/me/wiki/page"))
	assert.Equal(t, "the body\n", h.stdout.String())

	next := newHarness(t)
	next.app.lockDir = h.app.lockDir
	require.Error(t, next.get(t, s.URL+"/me/wiki/page"), "another invocation inherits no agreement")
	next.app.agreed = []string{own, manual}
	require.NoError(t, next.get(t, s.URL+"/me/wiki/page"), "a later invocation can explicitly sign the rules it read")
}

func TestRules_AnUnknownValueDoesNotSignUnreadRules(t *testing.T) {
	s := rulesServer(t)
	s.pages["/me/wiki/page.md"] = "the body\n"
	s.pageHeaders["/me/wiki/page.md"] = http.Header{"X-Wikilayer-Rules-Page": {s.URL + "/me/wiki/rules.md"}}
	h := newHarness(t)
	h.app.agreed = []string{"12345678"}
	require.Error(t, h.get(t, s.URL+"/me/wiki/page"))
	assert.Empty(t, h.stdout.String())
}

func TestRules_ValuesExpireAndAnotherReadDoesNotRevokeThem(t *testing.T) {
	s := rulesServer(t)
	s.pages["/me/wiki/page.md"] = "the body\n"
	s.pages["/old/rules.md"] = "->/me/wiki/rules.md"
	s.pageHeaders["/me/wiki/page.md"] = http.Header{"X-Wikilayer-Rules-Page": {
		strings.Replace(s.URL, "http://", "wikilayer://", 1) + "/me/wiki/rules.md",
	}}
	h := newHarness(t)
	first := readRulesCode(t, h, s.URL+"/old/rules.md")
	second := readRulesCode(t, h, s.URL+"/me/wiki/rules.md")
	h.app.agreed = []string{first}
	require.NoError(t, h.get(t, s.URL+"/me/wiki/page"))
	data, err := os.ReadFile(h.app.receiptPath(first))
	require.NoError(t, err)
	var receipt rulesReceipt
	require.NoError(t, json.Unmarshal(data, &receipt))
	receipt.Until = time.Now().Add(-time.Second)
	data, err = json.Marshal(receipt)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(h.app.receiptPath(first), data, 0o600))
	h.stdout.Reset()
	require.Error(t, h.get(t, s.URL+"/me/wiki/page"))
	assert.Empty(t, h.stdout.String())
	h.app.agreed = []string{second}
	require.NoError(t, h.get(t, s.URL+"/me/wiki/page"))
}

type refusedRulesOutput struct{}

func (refusedRulesOutput) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestRules_FailedOutputIssuesNoValue(t *testing.T) {
	s := rulesServer(t)
	h := newHarness(t)
	h.app.stdout = refusedRulesOutput{}
	require.ErrorContains(t, h.get(t, s.URL+"/me/wiki/rules.md"), "output unavailable")
	_, err := os.ReadDir(filepath.Join(h.app.lockDir, "rules"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NotContains(t, h.stderr.String(), "-agree")
}

func TestRun_RefusesMalformedAgreementValues(t *testing.T) {
	for _, value := range []string{"yes", "0", "1,,2", "../secret"} {
		h := newHarness(t)
		assert.Equal(t, 2, run([]string{"-agree", value, "https://example.com/wiki"}, h.stdout, h.stderr))
		assert.Contains(t, h.stderr.String(), "-agree")
		assert.Empty(t, h.stdout.String())
	}
}

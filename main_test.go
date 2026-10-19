package main

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRun_PrintsTheVersion(t *testing.T) {
	version = "1.2.3"
	t.Cleanup(func() { version = "" })
	var stdout, stderr bytes.Buffer

	code := run([]string{"-version"}, &stdout, &stderr)

	assert.Equal(t, 0, code)
	assert.Equal(t, "1.2.3\n", stdout.String(), "the Homebrew formula's test compares this line with the version it built")
}

func TestRun_AsksForExactlyOneAddress(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}} {
		var stdout, stderr bytes.Buffer

		code := run(args, &stdout, &stderr)

		assert.Equal(t, 2, code)
		assert.Contains(t, stderr.String(), "Usage: wlget")
		assert.Empty(t, stdout.String())
	}
}

func TestRun_RefusesUntilAndTimeoutTogether(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"-until", "2026-10-19T20:05Z", "-timeout", "10m", "wikilayer://wikilayer.org/chat?after_seq=1"}, &stdout, &stderr)

	assert.Equal(t, 2, code, "two deadlines would leave it unclear which one ends the wait")
	assert.Contains(t, stderr.String(), "-until")
}

func TestRun_RefusesAnUntilWithoutItsZone(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"-until", "2026-10-19T20:05", "wikilayer://wikilayer.org/chat?after_seq=1"}, &stdout, &stderr)

	assert.Equal(t, 2, code, "a time without a zone means a different moment on each machine that reads it")
	assert.Contains(t, stderr.String(), "zone")
}

func TestParseUntil_TakesTheTimeAsTheChatWritesIt(t *testing.T) {
	for raw, want := range map[string]string{
		"2026-10-19T20:05Z":         "2026-10-19T20:05:00Z",
		"2026-10-19T20:05:30Z":      "2026-10-19T20:05:30Z",
		"2026-10-19T22:05+02:00":    "2026-10-19T20:05:00Z",
		"2026-10-19T22:05:00+02:00": "2026-10-19T20:05:00Z",
	} {
		until, err := parseUntil(raw)

		assert.NoError(t, err, raw)
		assert.Equal(t, want, until.UTC().Format(time.RFC3339), raw)
	}
}

func TestExitCode_TellsAPassedDeadlineFromAFailure(t *testing.T) {
	assert.Equal(t, 0, exitCode(nil))
	assert.Equal(t, 124, exitCode(deadlineReached{}), "the same code timeout(1) gives, so a script tells silence from failure")
	assert.Equal(t, 1, exitCode(errors.New("the server refused")))
}

func TestRun_ExplainsAnAddressItCannotRead(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"ftp://wikilayer.org/x"}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "wikilayer://")
}

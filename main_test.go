package main

import (
	"bytes"
	"testing"

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

func TestRun_ExplainsAnAddressItCannotRead(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"ftp://wikilayer.org/x"}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "wikilayer://")
}

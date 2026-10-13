package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type signInMethod string

const (
	methodBrowser signInMethod = "browser"
	methodDevice  signInMethod = "device"
)

type signInChoice struct {
	method signInMethod
	reason string
}

func chooseSignIn(asked string, getenv func(string) string, goos string) (signInChoice, error) {
	source := "-signin"
	if asked == "" {
		asked, source = getenv("WLGET_SIGNIN"), "WLGET_SIGNIN"
	}
	switch signInMethod(asked) {
	case methodBrowser, methodDevice:
		return signInChoice{signInMethod(asked), fmt.Sprintf("%s=%s asks for it", source, asked)}, nil
	case "":
	default:
		return signInChoice{}, fmt.Errorf("%s=%s is neither %s nor %s", source, asked, methodBrowser, methodDevice)
	}
	if getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != "" {
		return signInChoice{methodDevice, "this is an SSH session, where no browser can come back to wlget"}, nil
	}
	if drawsWindowsOnlyWithADisplay(goos) && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return signInChoice{methodDevice, "this machine has no graphical display for a browser (neither DISPLAY nor WAYLAND_DISPLAY is set)"}, nil
	}
	return signInChoice{methodBrowser, "a browser can open on this machine"}, nil
}

func drawsWindowsOnlyWithADisplay(goos string) bool {
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos":
		return true
	}
	return false
}

type storeKind string

const (
	storeKeychain storeKind = "keychain"
	storeFile     storeKind = "file"
)

func chooseStore(asked string, getenv func(string) string) (credentialStore, error) {
	source := "-store"
	if asked == "" {
		asked, source = getenv("WLGET_STORE"), "WLGET_STORE"
	}
	switch storeKind(asked) {
	case "", storeKeychain:
		return keyringStore{}, nil
	case storeFile:
		config, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("finding a place for the sign-in file: %w", err)
		}
		return fileStore{dir: filepath.Join(config, "wlget", "signins")}, nil
	}
	return nil, fmt.Errorf("%s=%s is neither %s nor %s", source, asked, storeKeychain, storeFile)
}

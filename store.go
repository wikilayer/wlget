package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const keyringService = "wlget"

type keyringStore struct{}

func (keyringStore) describe() string {
	return "the system keychain"
}

func (keyringStore) load(origin string) (*credentials, error) {
	text, err := keyring.Get(keyringService, origin)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, keychainUnavailable(err)
	}
	return decodeCredentials(text)
}

func (keyringStore) save(origin string, c *credentials) error {
	text, err := c.encode()
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, origin, text); err != nil {
		return keychainUnavailable(err)
	}
	return nil
}

func (keyringStore) remove(origin string) error {
	err := keyring.Delete(keyringService, origin)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return keychainUnavailable(err)
	}
	return nil
}

func keychainUnavailable(err error) error {
	return fmt.Errorf("the system keychain did not answer (%w); on a machine without one, such as a server reached over SSH, "+
		"set WLGET_STORE=%s or pass -store=%s to keep the sign-in in a file only your account can read", err, storeFile, storeFile)
}

type fileStore struct {
	dir string
}

func (s fileStore) describe() string {
	return "a file under " + s.dir
}

func (s fileStore) path(origin string) string {
	name := sha256.Sum256([]byte(origin))
	return filepath.Join(s.dir, hex.EncodeToString(name[:16])+".json")
}

func (s fileStore) load(origin string) (*credentials, error) {
	path := s.path(origin)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by accounts other than yours (mode %v), so wlget does not trust it; "+
			"chmod 600 it if it is yours, or delete it and sign in again", path, info.Mode().Perm())
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeCredentials(string(text))
}

func (s fileStore) save(origin string, c *credentials) error {
	text, err := c.encode()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return err
	}
	staged, err := os.CreateTemp(s.dir, ".signin-*")
	if err != nil {
		return err
	}
	_, writeErr := staged.WriteString(text)
	if err := errors.Join(writeErr, staged.Chmod(0o600), staged.Close()); err != nil {
		return errors.Join(err, os.Remove(staged.Name()))
	}
	if err := os.Rename(staged.Name(), s.path(origin)); err != nil {
		return errors.Join(err, os.Remove(staged.Name()))
	}
	return nil
}

func (s fileStore) remove(origin string) error {
	err := os.Remove(s.path(origin))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

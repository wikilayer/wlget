package main

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const keyringService = "wlget"

type keyringStore struct{}

func (keyringStore) load(origin string) (*credentials, error) {
	text, err := keyring.Get(keyringService, origin)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeCredentials(text)
}

func (keyringStore) save(origin string, c *credentials) error {
	text, err := c.encode()
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, origin, text)
}

func (keyringStore) remove(origin string) error {
	err := keyring.Delete(keyringService, origin)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

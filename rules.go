package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const rulesPageHeader = "X-Wikilayer-Rules-Page"

const agreementLifetime = time.Hour

type rulesReceipt struct {
	URL   string    `json:"url"`
	Until time.Time `json:"until"`
}

func parseAgreements(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var values []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("-agree %q is not a list of positive values printed after reading rules", raw)
		}
		values = append(values, strconv.FormatUint(n, 10))
	}
	return values, nil
}

func (a *app) receiptPath(value string) string {
	return filepath.Join(a.lockDir, "rules", value+".json")
}

func (a *app) requireAgreement(headers []string) error {
	var missing []string
	seen := make(map[string]bool)
	for _, header := range headers {
		for _, raw := range strings.Split(header, ",") {
			t, err := resolve(strings.TrimSpace(raw))
			if err != nil || !strings.HasSuffix(t.url.Path, "/rules.md") {
				return fmt.Errorf("the server announced an invalid rules address: %q", raw)
			}
			address := t.url.String()
			if seen[address] {
				continue
			}
			seen[address] = true
			signed, err := a.agreementFor(address)
			if err != nil {
				return err
			}
			if !signed {
				missing = append(missing, raw)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("read and agree to these rules before reading this page:\n  %s\nthen repeat the page request with -agree VALUE[,VALUE...] before the address; each value signs that you read the rules and undertake to follow them",
		strings.Join(missing, "\n  "))
}

func (a *app) agreementFor(address string) (bool, error) {
	for _, value := range a.agreed {
		if _, err := parseAgreements(value); err != nil {
			return false, err
		}
		body, err := os.ReadFile(a.receiptPath(value))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("reading the record of rules read: %w", err)
		}
		var receipt rulesReceipt
		if err := json.Unmarshal(body, &receipt); err != nil {
			return false, fmt.Errorf("reading the record of rules read: %w", err)
		}
		if receipt.URL == address && time.Now().Before(receipt.Until) {
			return true, nil
		}
	}
	return false, nil
}

func (a *app) issueAgreement(address string) error {
	until := time.Now().Add(agreementLifetime)
	body, err := json.Marshal(rulesReceipt{URL: address, Until: until})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(a.lockDir, "rules"), 0o700); err != nil {
		return fmt.Errorf("keeping the record of rules read: %w", err)
	}
	for range 8 {
		n, err := rand.Int(rand.Reader, big.NewInt(99_999_999))
		if err != nil {
			return fmt.Errorf("making a value to sign the rules: %w", err)
		}
		value := strconv.FormatInt(n.Int64()+1, 10)
		file, err := os.OpenFile(a.receiptPath(value), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("keeping the record of rules read: %w", err)
		}
		_, writeErr := file.Write(body)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return fmt.Errorf("keeping the record of rules read: %w", err)
		}
		_, err = fmt.Fprintf(a.stderr, "wlget: -agree %s signs that you read %s and undertake to follow its rules; valid until %s. Pass it explicitly on each page request.\n", value, address, until.UTC().Format(time.RFC3339))
		return err
	}
	return errors.New("could not issue a distinct rules value; read the rules again")
}

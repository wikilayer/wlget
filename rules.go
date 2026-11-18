package main

import (
	"fmt"
	"strconv"
	"strings"
)

const rulesSignatureParam = "rules_read_and_agreed"

func parseAgreements(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var values []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("-rules-read-and-agreed %q is not a list of positive values handed out by MCP", raw)
		}
		values = append(values, strconv.FormatUint(n, 10))
	}
	return values, nil
}

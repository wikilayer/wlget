package main

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

type target struct {
	url   *url.URL
	chat  bool
	exact bool
}

const (
	chatPath     = "/chat"
	storedPrefix = "/s/"
)

func resolve(raw string) (target, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return target{}, fmt.Errorf("%q is not an address: %w", raw, err)
	}
	switch u.Scheme {
	case "wikilayer":
		u.Scheme = "https"
		if isLoopback(u.Hostname()) {
			u.Scheme = "http"
		}
	case "https", "http":
	default:
		return target{}, fmt.Errorf("%q is not an address wlget reads: it starts with wikilayer://, https:// or http://", raw)
	}
	if u.Host == "" {
		return target{}, fmt.Errorf("%q names no server", raw)
	}
	u.Fragment = ""
	u.RawFragment = ""
	if u.Path == chatPath {
		return target{url: u, chat: true, exact: true}, nil
	}
	if strings.HasPrefix(u.Path, storedPrefix) {
		return target{url: u, exact: true}, nil
	}

	page := strings.TrimSuffix(u.Path, "/")
	if page == "" {
		return target{}, fmt.Errorf("%q names a server, not a wiki or a page", raw)
	}
	switch path.Ext(page) {
	case ".md", ".json":
	case ".html":
		page = strings.TrimSuffix(page, ".html")
	default:
		page += ".md"
	}
	u.Path = page
	u.RawPath = ""
	return target{url: u}, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (t target) origin() string {
	return t.url.Scheme + "://" + t.url.Host
}

func (t target) answeredBy(location *url.URL) bool {
	if location.Scheme+"://"+location.Host != t.origin() {
		return false
	}
	if t.exact {
		return location.Path == t.url.Path && location.RawQuery == t.url.RawQuery
	}
	if location.Path == "/" || location.Path == "/login" {
		return false
	}
	return path.Ext(location.Path) == path.Ext(t.url.Path)
}

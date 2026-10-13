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

func serverAddress(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%q is not an address: %w", raw, err)
	}
	switch u.Scheme {
	case "wikilayer":
		u.Scheme = "https"
		if isLoopback(u.Hostname()) {
			u.Scheme = "http"
		}
	case "https", "http":
	default:
		return nil, fmt.Errorf("%q is not an address wlget reads: it starts with wikilayer://, https:// or http://", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%q names no server", raw)
	}
	return u, nil
}

func resolve(raw string) (target, error) {
	u, err := serverAddress(raw)
	if err != nil {
		return target{}, err
	}
	u.Fragment = ""
	u.RawFragment = ""
	switch u.Path {
	case chatPath:
		u.Path = chatPath + ".md"
		u.RawPath = ""
		return target{url: u, chat: true, exact: true}, nil
	case chatPath + ".md", chatPath + ".json":
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

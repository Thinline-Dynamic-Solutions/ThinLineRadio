// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// validatePublicWebhookURL enforces https and destination-type host rules before save.
// Dial-time checks in newSafeWebhookHTTPClient still refuse private/loopback peers
// (including after DNS rebinding or redirects).
func validatePublicWebhookURL(raw, destinationType string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("url required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid url")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("url must use https")
	}
	if u.User != nil {
		return fmt.Errorf("url must not include credentials")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return fmt.Errorf("invalid url host")
	}
	switch strings.ToLower(strings.TrimSpace(destinationType)) {
	case "slack":
		if host != "hooks.slack.com" {
			return fmt.Errorf("slack webhooks must use hooks.slack.com")
		}
	case "discord":
		if host != "discord.com" && host != "discordapp.com" && !strings.HasSuffix(host, ".discord.com") && !strings.HasSuffix(host, ".discordapp.com") {
			return fmt.Errorf("discord webhooks must use discord.com or discordapp.com")
		}
	}
	// Best-effort resolve at save time; dial-time check remains authoritative.
	if ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host); err == nil {
		for _, ip := range ips {
			if isBlockedWebhookIP(ip.IP) {
				return fmt.Errorf("url resolves to a private or local address")
			}
		}
	}
	return nil
}

func isBlockedWebhookIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// CGNAT / shared address space 100.64.0.0/10 (RFC 6598), common on Tailscale.
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return false
}

func newSafeWebhookHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, ip := range ips {
				if isBlockedWebhookIP(ip.IP) {
					lastErr = fmt.Errorf("refusing private or local address for webhook")
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("no safe address for webhook host")
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("redirect must stay on https")
			}
			host := strings.ToLower(req.URL.Hostname())
			if host == "" {
				return fmt.Errorf("redirect missing host")
			}
			ips, err := net.DefaultResolver.LookupIPAddr(req.Context(), host)
			if err != nil {
				return err
			}
			for _, ip := range ips {
				if isBlockedWebhookIP(ip.IP) {
					return fmt.Errorf("redirect resolves to a private or local address")
				}
			}
			return nil
		},
	}
}

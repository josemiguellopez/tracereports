package lab

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ParseHosts splits TRACEREPORTS_LAB_HOSTS (comma separated): "host", "host:port" or "*.domain".
func ParseHosts(s string) []string {
	var out []string
	for _, h := range strings.Split(s, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// HostRule is the hosts a send may reach: the ones the test talked to (the plan's, host:port) plus
// extra (TRACEREPORTS_LAB_HOSTS, or a base URL the person chose). A capsule or a plan comes from
// ingested data: without this rule, whoever sends results could aim a resend (with the secrets of
// TRACEREPORTS_LAB_<NAME>) at any host the machine reaches. Link-local addresses are blocked by the
// transport anyway.
func HostRule(p *Plan, extra []string) func(*url.URL) error {
	allowed := map[string]bool{}
	for _, h := range p.Hosts {
		allowed[strings.ToLower(h)] = true
	}
	if len(p.Hosts) == 0 { // planes viejos sin la lista: el host de la llamada
		if u, err := url.Parse(p.Target.URL); err == nil && u.Host != "" {
			allowed[strings.ToLower(u.Host)] = true
		}
	}
	return func(u *url.URL) error {
		if allowed[strings.ToLower(u.Host)] || Listed(extra, u.Host) {
			return nil
		}
		return fmt.Errorf("host %s is not one the test called nor in TRACEREPORTS_LAB_HOSTS", u.Host)
	}
}

// Listed reports whether hostport ("host" or "host:port") is in a TRACEREPORTS_LAB_HOSTS list:
// the same host:port, the host name, or a "*.domain" it belongs to.
func Listed(list []string, hostport string) bool {
	host := strings.ToLower(hostport)
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	for _, h := range list {
		if h == host || h == name || (strings.HasPrefix(h, "*.") && strings.HasSuffix(name, h[1:])) {
			return true
		}
	}
	return false
}

// HostOf returns the host[:port] of a URL ("" if it has none), to allow a base URL the person chose.
func HostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

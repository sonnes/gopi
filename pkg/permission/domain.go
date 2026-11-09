package permission

import (
	"fmt"
	"net/url"
	"strings"
)

// domainPrefix starts the content of a rule for web_fetch, as in
// "WebFetch(domain:go.dev)".
const domainPrefix = "domain:"

// domainRule is the content of a rule for web_fetch: one host. The
// format comes from tools/WebFetchTool/WebFetchTool.ts of Claude Code.
// The rule matches the host exactly, so a rule on go.dev does not match
// pkg.go.dev. Claude Code compares the letter case of the rule, and
// gopi does not, because a host name has no letter case.
type domainRule struct {
	// host is the host of the rule, in lowercase.
	host string
}

// parseDomainRule parses the content of a rule for web_fetch.
func parseDomainRule(content string) (domainRule, error) {
	host, ok := strings.CutPrefix(content, domainPrefix)
	if !ok || host == "" {
		return domainRule{}, fmt.Errorf("the content %q is not %s<host>", content, domainPrefix)
	}
	return domainRule{host: strings.ToLower(host)}, nil
}

// hostOf returns the host of the URL raw, in lowercase. ok is false if
// raw does not parse or has no host.
func hostOf(raw string) (host string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	return strings.ToLower(u.Hostname()), true
}

package notify

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Mask is the placeholder returned in place of a stored secret. Sending it
// back on update means "unchanged": the stored value is kept.
const Mask = "********"

// SecretHeader reports whether a header name is likely to carry a credential.
func SecretHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
		return true
	}
	return strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "key") ||
		strings.Contains(n, "password") || strings.Contains(n, "auth")
}

var secretQuery = map[string]bool{"token": true, "key": true, "secret": true, "password": true, "sig": true, "signature": true, "auth": true}

func secretQueryName(k string) bool {
	k = strings.ToLower(k)
	return secretQuery[k] || strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "apikey") || strings.Contains(k, "api_key")
}

// pathSecretTypes are channel types whose URL path is itself the credential.
var pathSecretTypes = map[string]bool{"discord": true, "slack": true, "teams": true}

// MaskURL hides the secret parts of a URL: the userinfo password, query
// values with credential-like names and, for webhook providers whose path is
// the credential, the whole path.
func MaskURL(typ, raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Mask
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "MASKPW")
		}
	}
	if u.RawQuery != "" {
		parts := strings.Split(u.RawQuery, "&")
		for i, p := range parts {
			k, _, _ := strings.Cut(p, "=")
			if dk, err := url.QueryUnescape(k); err == nil {
				k = dk
			}
			if secretQueryName(k) && strings.Contains(p, "=") {
				parts[i] = p[:strings.Index(p, "=")] + "=" + Mask
			}
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	if pathSecretTypes[typ] && strings.Trim(u.Path, "/") != "" {
		u.Path, u.RawPath = "/"+Mask, ""
	}
	s := strings.Replace(u.String(), "MASKPW", Mask, 1)
	return strings.ReplaceAll(s, "%2A", "*")
}

// RestoreURL fills masked parts of an incoming URL from the stored one. A URL
// without a mask is taken as given.
func RestoreURL(typ, incoming, stored string) string {
	if !strings.Contains(incoming, Mask) {
		return incoming
	}
	if incoming == MaskURL(typ, stored) {
		return stored
	}
	nu, err := url.Parse(incoming)
	if err != nil {
		return stored
	}
	su, err := url.Parse(stored)
	if err != nil {
		return incoming
	}
	if nu.User != nil && su.User != nil {
		if pw, ok := nu.User.Password(); ok && pw == Mask {
			spw, _ := su.User.Password()
			nu.User = url.UserPassword(nu.User.Username(), spw)
		}
	}
	if nu.Path == "/"+Mask {
		nu.Path, nu.RawPath = su.Path, su.RawPath
	}
	if nu.RawQuery != "" {
		sq := map[string]string{}
		for _, p := range strings.Split(su.RawQuery, "&") {
			k, v, _ := strings.Cut(p, "=")
			sq[k] = v
		}
		parts := strings.Split(nu.RawQuery, "&")
		for i, p := range parts {
			k, v, ok := strings.Cut(p, "=")
			if ok && v == Mask {
				if old, found := sq[k]; found {
					parts[i] = k + "=" + old
				}
			}
		}
		nu.RawQuery = strings.Join(parts, "&")
	}
	return nu.String()
}

// MaskHeaders returns headers with credential-bearing values masked.
func MaskHeaders(h map[string]any) map[string]any {
	out := make(map[string]any, len(h))
	for k, v := range h {
		if s, ok := v.(string); ok && s != "" && SecretHeader(k) {
			out[k] = Mask
		} else {
			out[k] = v
		}
	}
	return out
}

// RestoreHeaders keeps stored credential headers the update left masked or
// blank.
func RestoreHeaders(incoming, stored map[string]any) map[string]any {
	out := make(map[string]any, len(incoming))
	for k, v := range incoming {
		if s, _ := v.(string); SecretHeader(k) && (s == Mask || s == "") {
			for sk, sv := range stored {
				if strings.EqualFold(sk, k) {
					out[k] = sv
				}
			}
			if _, ok := out[k]; ok {
				continue
			}
			if s == Mask {
				continue // nothing stored to keep: drop the placeholder
			}
		}
		out[k] = v
	}
	return out
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// RedactConfig prepares a channel config for a response: secret fields are
// blanked (with a *_set marker), credential headers and URL secrets masked.
func RedactConfig(typ string, raw []byte) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	for _, k := range SecretFields {
		if v, ok := m[k]; ok && v != "" && v != nil {
			m[k] = ""
			m[k+"_set"] = true
		}
	}
	if s, ok := m["url"].(string); ok {
		m["url"] = MaskURL(typ, s)
	}
	if s, ok := m["server"].(string); ok {
		m["server"] = MaskURL("", s)
	}
	if h := asMap(m["headers"]); h != nil {
		m["headers"] = MaskHeaders(h)
	}
	return m
}

// MergeConfig applies an update to the stored channel config: omitted, blank
// or masked secrets keep their stored value.
func MergeConfig(typ string, newRaw, oldRaw []byte) []byte {
	nm := map[string]any{}
	om := map[string]any{}
	_ = json.Unmarshal(newRaw, &nm)
	_ = json.Unmarshal(oldRaw, &om)
	for _, k := range SecretFields {
		if v, ok := nm[k]; !ok || v == "" || v == nil || v == Mask {
			if ov, ok := om[k]; ok {
				nm[k] = ov
			}
		}
		delete(nm, k+"_set")
	}
	for _, f := range []string{"url", "server"} {
		t := typ
		if f == "server" {
			t = ""
		}
		if s, ok := nm[f].(string); ok {
			if os, _ := om[f].(string); os != "" {
				nm[f] = RestoreURL(t, s, os)
			}
		}
	}
	if h := asMap(nm["headers"]); h != nil {
		nm["headers"] = RestoreHeaders(h, asMap(om["headers"]))
	}
	b, _ := json.Marshal(nm)
	return b
}

// RedactCheckConfig prepares a check config for a response.
func RedactCheckConfig(raw []byte) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	if v, ok := m["basic_password"]; ok && v != "" && v != nil {
		m["basic_password"] = ""
		m["basic_password_set"] = true
	}
	if h := asMap(m["headers"]); h != nil {
		m["headers"] = MaskHeaders(h)
	}
	return m
}

// MergeCheckConfig applies an update to a stored check config.
func MergeCheckConfig(newRaw, oldRaw []byte) []byte {
	nm := map[string]any{}
	om := map[string]any{}
	_ = json.Unmarshal(newRaw, &nm)
	_ = json.Unmarshal(oldRaw, &om)
	if v, ok := nm["basic_password"]; !ok || v == "" || v == nil || v == Mask {
		if ov, ok := om["basic_password"]; ok {
			nm["basic_password"] = ov
		}
	}
	delete(nm, "basic_password_set")
	if h := asMap(nm["headers"]); h != nil {
		nm["headers"] = RestoreHeaders(h, asMap(om["headers"]))
	}
	b, _ := json.Marshal(nm)
	return b
}

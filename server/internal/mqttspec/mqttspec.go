// Package mqttspec parses and validates MQTT connection, rule and topic
// settings. It is pure (no I/O besides resolving secret references) and
// shared by the service layer (validation on write) and the gateway's mqtt
// transport (use at runtime).
package mqttspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// --- topic filters ---

// ValidateFilter checks an MQTT topic filter (+ and # wildcards, MQTT 5 §4.7).
func ValidateFilter(f string) error {
	if f == "" || len(f) > 1024 {
		return errors.New("must be 1-1024 characters")
	}
	if strings.ContainsRune(f, 0) {
		return errors.New("must not contain NUL")
	}
	if strings.HasPrefix(f, "$share/") {
		return errors.New("don't write $share/ yourself: set replicas > 1 on the connection")
	}
	levels := strings.Split(f, "/")
	for i, l := range levels {
		if strings.Contains(l, "#") && (l != "#" || i != len(levels)-1) {
			return errors.New("# must be a whole level, and the last one")
		}
		if strings.Contains(l, "+") && l != "+" {
			return errors.New("+ must be a whole level")
		}
	}
	return nil
}

// Match reports whether a topic matches a filter. Topics starting with $
// don't match filters starting with a wildcard (MQTT 5 §4.7.2).
func Match(filter, topic string) bool {
	if strings.HasPrefix(topic, "$") && (strings.HasPrefix(filter, "+") || strings.HasPrefix(filter, "#")) {
		return false
	}
	fl := strings.Split(filter, "/")
	tl := strings.Split(topic, "/")
	for i, f := range fl {
		if f == "#" {
			return true
		}
		if i >= len(tl) {
			return false
		}
		if f != "+" && f != tl[i] {
			return false
		}
	}
	return len(fl) == len(tl)
}

// --- device resolution ---

// DeviceSpec says where an uplink finds the device's external id.
type DeviceSpec struct {
	Segment *int   `json:"segment,omitempty"` // topic level, 0-based
	Field   string `json:"field,omitempty"`   // path in the decoded payload
	Fixed   string `json:"fixed,omitempty"`   // always this external id
}

func ParseDevice(raw json.RawMessage) (DeviceSpec, error) {
	var d DeviceSpec
	if len(raw) == 0 {
		return d, errors.New("required: {segment: n}, {field: path} or {fixed: id}")
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, fmt.Errorf("invalid: %v", err)
	}
	n := 0
	if d.Segment != nil {
		n++
		if *d.Segment < 0 || *d.Segment > 64 {
			return d, errors.New("segment must be 0-64")
		}
	}
	if d.Field != "" {
		n++
		if _, err := ParsePath(d.Field); err != nil {
			return d, fmt.Errorf("field: %v", err)
		}
	}
	if d.Fixed != "" {
		n++
	}
	if n != 1 {
		return d, errors.New("set exactly one of segment, field or fixed")
	}
	return d, nil
}

// --- field maps ---

type Mapping struct {
	Element string `json:"element"`
	Value   string `json:"value"`          // path; "" = the whole decoded object
	Wrap    string `json:"wrap,omitempty"` // "value" (default) → {"value": v}; "raw" → as is
	When    string `json:"when,omitempty"` // "exists" → skip silently when missing
	path    Path
}

func (m Mapping) Path() Path { return m.path }

func ParseFieldMap(raw json.RawMessage) ([]Mapping, error) {
	var ms []Mapping
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("required: at least one {element, value} mapping")
	}
	if err := json.Unmarshal(raw, &ms); err != nil {
		return nil, fmt.Errorf("must be an array of {element, value, wrap, when}: %v", err)
	}
	if len(ms) == 0 || len(ms) > 100 {
		return nil, errors.New("1-100 mappings")
	}
	for i := range ms {
		m := &ms[i]
		if m.Element = strings.TrimSpace(m.Element); m.Element == "" {
			return nil, fmt.Errorf("mapping %d: element is required", i)
		}
		p, err := ParsePath(m.Value)
		if err != nil {
			return nil, fmt.Errorf("mapping %d: %v", i, err)
		}
		m.path = p
		switch m.Wrap {
		case "", "value", "raw":
		default:
			return nil, fmt.Errorf("mapping %d: wrap must be value or raw", i)
		}
		switch m.When {
		case "", "exists":
		default:
			return nil, fmt.Errorf("mapping %d: when must be exists", i)
		}
	}
	return ms, nil
}

// --- paths: a.b[0]["c d"] ---

type pathStep struct {
	key   string
	index int
	isIdx bool
}

type Path []pathStep

// ParsePath parses a safe JSON path subset: dots, [index], ["quoted key"].
// An empty path means the whole value.
func ParsePath(s string) (Path, error) {
	var p Path
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); {
		switch s[i] {
		case '.':
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return nil, errors.New("unclosed [")
			}
			inner := s[i+1 : i+end]
			if len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\'') && inner[len(inner)-1] == inner[0] {
				p = append(p, pathStep{key: inner[1 : len(inner)-1]})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("bad index [%s]", inner)
				}
				p = append(p, pathStep{index: n, isIdx: true})
			}
			i += end + 1
		default:
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			p = append(p, pathStep{key: s[i:j]})
			i = j
		}
	}
	if len(p) > 16 {
		return nil, errors.New("at most 16 levels")
	}
	return p, nil
}

// Get walks a decoded JSON value.
func (p Path) Get(v any) (any, bool) {
	cur := v
	for _, st := range p {
		if st.isIdx {
			a, ok := cur.([]any)
			if !ok || st.index >= len(a) {
				return nil, false
			}
			cur = a[st.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[st.key]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// --- downlink topics ---

var placeholders = []string{"{device}", "{element}", "{user}"}

// ValidateTopicTemplate checks a downlink topic template: no wildcards, and
// only known placeholders.
func ValidateTopicTemplate(t string) error {
	if t == "" || len(t) > 1024 {
		return errors.New("must be 1-1024 characters")
	}
	if strings.ContainsAny(t, "+#\x00") {
		return errors.New("must not contain + # or NUL")
	}
	if strings.HasPrefix(t, "$") {
		return errors.New("must not start with $")
	}
	rest := t
	for _, ph := range placeholders {
		rest = strings.ReplaceAll(rest, ph, "")
	}
	if strings.ContainsAny(rest, "{}") {
		return errors.New("unknown placeholder (use {device}, {element}, {user})")
	}
	return nil
}

// RenderTopic fills a template. Every value must be one topic level: no
// + # / or NUL, not empty, not starting with $ (topic injection guard).
func RenderTopic(t string, vals map[string]string) (string, error) {
	out := t
	for _, ph := range placeholders {
		if !strings.Contains(out, ph) {
			continue
		}
		v := vals[strings.Trim(ph, "{}")]
		if v == "" || strings.ContainsAny(v, "+#/\x00") || strings.HasPrefix(v, "$") {
			return "", fmt.Errorf("%s value %q can't be used in a topic", ph, v)
		}
		out = strings.ReplaceAll(out, ph, v)
	}
	return out, nil
}

// --- encoders ---

type Encoder struct {
	Template  json.RawMessage `json:"template,omitempty"`   // JSON with "{{value}}" replaced by the message's value
	DecoderID string          `json:"decoder_id,omitempty"` // a decoder with encodeDownlink
}

func ParseEncoder(raw json.RawMessage) (Encoder, error) {
	var e Encoder
	if len(raw) == 0 || string(raw) == "null" {
		return e, nil // the message as is
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, fmt.Errorf("must be {template} or {decoder_id} or {}: %v", err)
	}
	if len(e.Template) > 0 && e.DecoderID != "" {
		return e, errors.New("set template or decoder_id, not both")
	}
	if len(e.Template) > 0 && !json.Valid(e.Template) {
		return e, errors.New("template must be JSON")
	}
	return e, nil
}

// --- auth, TLS, secrets ---

const (
	AuthNone     = "none"
	AuthPassword = "password"
	AuthMTLS     = "mtls"
)

type Auth struct {
	Method   string `json:"method"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // env:NAME or file:/path
}

func ParseAuth(raw json.RawMessage) (Auth, error) {
	var a Auth
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &a); err != nil {
			return a, fmt.Errorf("invalid: %v", err)
		}
	}
	if a.Method == "" {
		a.Method = AuthNone
		if a.Username != "" {
			a.Method = AuthPassword
		}
	}
	switch a.Method {
	case AuthNone, AuthMTLS:
	case AuthPassword:
		if a.Username == "" {
			return a, errors.New("username is required")
		}
		if a.Password != "" && !IsRef(a.Password) {
			return a, errors.New("password must be a reference (env:NAME or file:/path), never the value")
		}
	default:
		return a, errors.New("method must be none, password or mtls")
	}
	return a, nil
}

type TLS struct {
	CA                 string `json:"ca,omitempty"`   // reference to a PEM bundle
	Cert               string `json:"cert,omitempty"` // reference (mTLS)
	Key                string `json:"key,omitempty"`  // reference (mTLS)
	ServerName         string `json:"server_name,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

func ParseTLS(raw json.RawMessage) (TLS, error) {
	var t TLS
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &t); err != nil {
			return t, fmt.Errorf("invalid: %v", err)
		}
	}
	for name, v := range map[string]string{"ca": t.CA, "cert": t.Cert, "key": t.Key} {
		if v != "" && !IsRef(v) {
			return t, fmt.Errorf("%s must be a reference (env:NAME or file:/path)", name)
		}
	}
	if (t.Cert == "") != (t.Key == "") {
		return t, errors.New("cert and key go together")
	}
	return t, nil
}

// IsRef reports whether s is a secret reference.
func IsRef(s string) bool {
	return (strings.HasPrefix(s, "env:") && len(s) > 4) || (strings.HasPrefix(s, "file:/") && len(s) > 6)
}

// Resolve reads a secret reference on this machine. Values are never logged.
func Resolve(ref string) ([]byte, error) {
	switch {
	case ref == "":
		return nil, nil
	case strings.HasPrefix(ref, "env:"):
		v, ok := os.LookupEnv(ref[4:])
		if !ok {
			return nil, fmt.Errorf("secret %s: environment variable not set", ref)
		}
		return []byte(v), nil
	case strings.HasPrefix(ref, "file:"):
		b, err := os.ReadFile(ref[5:])
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", ref, errors.Unwrap(err))
		}
		return b, nil
	}
	return nil, fmt.Errorf("secret: %q is not a reference", "***")
}

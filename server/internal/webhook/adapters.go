package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"text/template"
	"text/template/parse"
	"time"
)

// Formats an endpoint can use.
var Formats = []string{"standard", "slack", "discord", "teams", "telegram", "custom"}

// MaxTemplateOutput bounds what a custom template may produce.
const MaxTemplateOutput = 64 << 10

// FormatPayload renders an event for a target, as sent at at (the
// webhook-timestamp of this attempt). When the target has a secret, every
// format is signed (Standard Webhooks headers).
func FormatPayload(t Target, evt NotificationEvent, at time.Time) ([]byte, http.Header, error) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "QuackQuack-Webhook/1.0")
	for k, v := range t.Headers {
		headers.Set(k, v)
	}
	var body []byte
	var err error
	switch t.Format {
	case "slack":
		body, err = formatSlack(evt)
	case "discord":
		body, err = formatDiscord(evt)
	case "teams":
		body, err = formatTeams(evt)
	case "telegram":
		body, err = formatTelegram(evt, t.ChatID)
	case "custom":
		body, err = RenderTemplate(t.Template, evt)
	default:
		body, err = formatStandard(evt)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("format %s payload: %w", t.Format, err)
	}
	if t.Secret != "" {
		ts, sig, err := SignStandardWebhook(t.Secret, evt.ID.String(), at, body)
		if err != nil {
			return nil, nil, err
		}
		headers.Set("webhook-id", evt.ID.String())
		headers.Set("webhook-timestamp", ts)
		headers.Set("webhook-signature", sig)
	}
	return body, headers, nil
}

func formatStandard(evt NotificationEvent) ([]byte, error) {
	cloudevent := map[string]any{
		"specversion":     "1.0",
		"id":              evt.ID.String(),
		"source":          "/quack/notifications",
		"type":            evt.Type,
		"time":            evt.Timestamp.UTC().Format(time.RFC3339Nano),
		"datacontenttype": "application/json",
		"data":            evt,
	}
	return json.Marshal(cloudevent)
}

func titleOf(evt NotificationEvent) string {
	switch evt.State {
	case StateResolved:
		return "✅ " + evt.Title
	case StateFiring:
		switch evt.Severity {
		case SeverityCritical:
			return "🚨 " + evt.Title
		case SeverityWarning:
			return "⚠️ " + evt.Title
		}
	}
	return "ℹ️ " + evt.Title
}

// slackEscape escapes the three characters Slack's mrkdwn treats as control
// (<!channel>, links), see https://api.slack.com/reference/surfaces/formatting.
var slackEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

func formatSlack(evt NotificationEvent) ([]byte, error) {
	color := "#0284C7"
	switch {
	case evt.State == StateResolved:
		color = "#16A34A"
	case evt.Severity == SeverityWarning:
		color = "#F59E0B"
	case evt.Severity == SeverityCritical:
		color = "#EF4444"
	}
	fields := []string{fmt.Sprintf("*Severity:* `%s`", strings.ToUpper(string(evt.Severity)))}
	if evt.DeviceName != "" {
		fields = append(fields, "*Device:* "+slackEscape(evt.DeviceName))
	}
	if evt.ElementName != "" {
		fields = append(fields, "*Element:* "+slackEscape(evt.ElementName))
	}
	if evt.Value != nil {
		fields = append(fields, fmt.Sprintf("*Value:* `%s`", slackEscape(fmt.Sprint(evt.Value))))
	}
	payload := map[string]any{
		"text": slackEscape(fmt.Sprintf("[%s] %s: %s", strings.ToUpper(string(evt.Severity)), evt.Title, evt.Message)),
		"attachments": []map[string]any{{
			"color": color,
			"blocks": []map[string]any{
				{"type": "header", "text": map[string]any{"type": "plain_text", "text": titleOf(evt), "emoji": true}},
				{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": slackEscape(evt.Message)}},
				{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": strings.Join(fields, "  •  ")}},
				{"type": "context", "elements": []map[string]any{{"type": "mrkdwn",
					"text": "Quack Quack IoT  •  " + evt.Timestamp.UTC().Format(time.RFC1123)}}},
			},
		}},
	}
	return json.Marshal(payload)
}

func formatDiscord(evt NotificationEvent) ([]byte, error) {
	color := 0x0284C7
	switch {
	case evt.State == StateResolved:
		color = 0x16A34A
	case evt.Severity == SeverityWarning:
		color = 0xF59E0B
	case evt.Severity == SeverityCritical:
		color = 0xEF4444
	}
	fields := []map[string]any{{"name": "Severity", "value": strings.ToUpper(string(evt.Severity)), "inline": true}}
	if evt.DeviceName != "" {
		fields = append(fields, map[string]any{"name": "Device", "value": evt.DeviceName, "inline": true})
	}
	if evt.ElementName != "" {
		fields = append(fields, map[string]any{"name": "Element", "value": evt.ElementName, "inline": true})
	}
	if evt.Value != nil {
		fields = append(fields, map[string]any{"name": "Value", "value": fmt.Sprint(evt.Value), "inline": true})
	}
	payload := map[string]any{
		"allowed_mentions": map[string]any{"parse": []string{}}, // names can't @everyone
		"embeds": []map[string]any{{
			"title":       titleOf(evt),
			"description": evt.Message,
			"color":       color,
			"fields":      fields,
			"timestamp":   evt.Timestamp.UTC().Format(time.RFC3339),
			"footer":      map[string]any{"text": "Quack Quack IoT"},
		}},
	}
	return json.Marshal(payload)
}

func formatTeams(evt NotificationEvent) ([]byte, error) {
	facts := []map[string]string{{"title": "Severity", "value": strings.ToUpper(string(evt.Severity))}}
	if evt.DeviceName != "" {
		facts = append(facts, map[string]string{"title": "Device", "value": evt.DeviceName})
	}
	if evt.ElementName != "" {
		facts = append(facts, map[string]string{"title": "Element", "value": evt.ElementName})
	}
	if evt.Value != nil {
		facts = append(facts, map[string]string{"title": "Value", "value": fmt.Sprint(evt.Value)})
	}
	payload := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.5",
				"body": []map[string]any{
					{"type": "TextBlock", "size": "Medium", "weight": "Bolder", "text": titleOf(evt)},
					{"type": "TextBlock", "text": evt.Message, "wrap": true},
					{"type": "FactSet", "facts": facts},
					{"type": "TextBlock", "size": "Small", "isSubtle": true,
						"text": "Quack Quack IoT • " + evt.Timestamp.UTC().Format(time.RFC1123)},
				},
			},
		}},
	}
	return json.Marshal(payload)
}

// formatTelegram builds a Bot API sendMessage body. Every interpolated value
// is HTML-escaped (parse_mode is HTML: a "<" in a name would otherwise make
// Telegram refuse the message, or inject a link).
func formatTelegram(evt NotificationEvent, chatID string) ([]byte, error) {
	if chatID == "" {
		return nil, errors.New("a telegram endpoint needs options.chat_id")
	}
	esc := html.EscapeString
	var sb strings.Builder
	fmt.Fprintf(&sb, "<b>[%s] %s</b>\n\n", strings.ToUpper(string(evt.Severity)), esc(titleOf(evt)))
	fmt.Fprintf(&sb, "%s\n\n", esc(evt.Message))
	if evt.DeviceName != "" {
		fmt.Fprintf(&sb, "<b>Device:</b> %s\n", esc(evt.DeviceName))
	}
	if evt.ElementName != "" {
		fmt.Fprintf(&sb, "<b>Element:</b> %s\n", esc(evt.ElementName))
	}
	if evt.Value != nil {
		fmt.Fprintf(&sb, "<b>Value:</b> <code>%s</code>\n", esc(fmt.Sprint(evt.Value)))
	}
	fmt.Fprintf(&sb, "<i>%s</i>", evt.Timestamp.UTC().Format(time.RFC1123))
	return json.Marshal(map[string]any{"chat_id": chatID, "text": sb.String(), "parse_mode": "HTML"})
}

// --- custom templates ---

var templateFuncs = template.FuncMap{
	// json renders any value as JSON: {"title": {{json .Title}}} stays valid
	// whatever the title contains.
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
}

// ParseTemplate checks a custom template: Go text/template syntax over the
// notification event, without range (a loop could run for a long time
// writing nothing) or nested templates.
func ParseTemplate(src string) (*template.Template, error) {
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("the custom format needs a template")
	}
	if len(src) > 16<<10 {
		return nil, errors.New("the template is over 16 KiB")
	}
	t, err := template.New("webhook").Funcs(templateFuncs).Option("missingkey=error").Parse(src)
	if err != nil {
		return nil, err
	}
	for _, tt := range t.Templates() {
		if tt.Tree == nil {
			continue
		}
		if tt.Name() != "webhook" {
			return nil, errors.New("define/block isn't allowed")
		}
		if err := checkTemplateNode(tt.Root); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func checkTemplateNode(n parse.Node) error {
	switch x := n.(type) {
	case *parse.ListNode:
		if x == nil {
			return nil
		}
		for _, c := range x.Nodes {
			if err := checkTemplateNode(c); err != nil {
				return err
			}
		}
	case *parse.IfNode:
		return errors.Join(checkTemplateNode(x.List), checkTemplateNode(x.ElseList))
	case *parse.WithNode:
		return errors.Join(checkTemplateNode(x.List), checkTemplateNode(x.ElseList))
	case *parse.RangeNode:
		return errors.New("range isn't allowed in webhook templates")
	case *parse.TemplateNode:
		return errors.New("calling templates isn't allowed in webhook templates")
	}
	return nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxTemplateOutput {
		return 0, fmt.Errorf("the template produced more than %d KiB", MaxTemplateOutput>>10)
	}
	return b.Buffer.Write(p)
}

// RenderTemplate runs a custom template on an event; the result must be JSON.
func RenderTemplate(src string, evt NotificationEvent) ([]byte, error) {
	t, err := ParseTemplate(src)
	if err != nil {
		return nil, err
	}
	var buf limitedBuffer
	if err := t.Execute(&buf, evt); err != nil {
		return nil, err
	}
	if !json.Valid(buf.Bytes()) {
		return nil, errors.New("the template's output isn't valid JSON (use {{json .Field}} for values)")
	}
	return buf.Bytes(), nil
}

// SampleEvent is the event test sends and template checks use.
func SampleEvent(severity Severity) NotificationEvent {
	if severity == "" {
		severity = SeverityInfo
	}
	return NotificationEvent{
		Type:        "io.quack.test.notification",
		Severity:    severity,
		Title:       "Test Webhook Delivery",
		Message:     "This is a test notification from Quack Quack.",
		DeviceName:  "Demo Greenhouse Gateway",
		ElementName: "Soil Moisture",
		Value:       42.5,
		Timestamp:   time.Now().UTC(),
	}
}

package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// FormatPayload converts an internal NotificationEvent into the wire format expected by the target webhook.
func FormatPayload(
	format string,
	secret string,
	evt NotificationEvent,
	customTemplate *string,
	customHeaders map[string]string,
) ([]byte, http.Header, error) {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "QuackQuack-Webhook/1.0")

	// Apply custom headers first
	for k, v := range customHeaders {
		headers.Set(k, v)
	}

	var body []byte
	var err error

	switch format {
	case "slack":
		body, err = formatSlack(evt)
	case "discord":
		body, err = formatDiscord(evt)
	case "teams":
		body, err = formatTeams(evt)
	case "telegram":
		body, err = formatTelegram(evt)
	case "custom":
		body, err = formatCustom(evt, customTemplate)
	case "standard", "":
		fallthrough
	default:
		body, err = formatStandard(evt)
		if err == nil && secret != "" {
			msgID, ts, sig := SignStandardWebhook(secret, evt.ID.String(), evt.Timestamp, body)
			headers.Set("webhook-id", msgID)
			headers.Set("webhook-timestamp", ts)
			headers.Set("webhook-signature", sig)
		}
	}

	if err != nil {
		return nil, nil, fmt.Errorf("format %s payload: %w", format, err)
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

func formatSlack(evt NotificationEvent) ([]byte, error) {
	color := "#0284C7" // blue (info)
	icon := "ℹ️"
	switch evt.Severity {
	case SeverityWarning:
		color = "#F59E0B" // amber
		icon = "⚠️"
	case SeverityCritical:
		color = "#EF4444" // red
		icon = "🚨"
	}

	fields := []string{
		fmt.Sprintf("*Severity:* `%s`", strings.ToUpper(string(evt.Severity))),
	}
	if evt.DeviceName != "" {
		fields = append(fields, fmt.Sprintf("*Device:* %s", evt.DeviceName))
	}
	if evt.ElementName != "" {
		fields = append(fields, fmt.Sprintf("*Element:* %s", evt.ElementName))
	}
	if evt.Value != nil {
		fields = append(fields, fmt.Sprintf("*Value:* `%v`", evt.Value))
	}

	payload := map[string]any{
		"text": fmt.Sprintf("[%s] %s: %s", strings.ToUpper(string(evt.Severity)), evt.Title, evt.Message),
		"attachments": []map[string]any{
			{
				"color": color,
				"blocks": []map[string]any{
					{
						"type": "header",
						"text": map[string]any{
							"type":  "plain_text",
							"text":  fmt.Sprintf("%s %s", icon, evt.Title),
							"emoji": true,
						},
					},
					{
						"type": "section",
						"text": map[string]any{
							"type": "mrkdwn",
							"text": evt.Message,
						},
					},
					{
						"type": "section",
						"text": map[string]any{
							"type": "mrkdwn",
							"text": strings.Join(fields, "  •  "),
						},
					},
					{
						"type": "context",
						"elements": []map[string]any{
							{
								"type": "mrkdwn",
								"text": fmt.Sprintf("Quack Quack IoT  •  %s", evt.Timestamp.UTC().Format(time.RFC1123)),
							},
						},
					},
				},
			},
		},
	}
	return json.Marshal(payload)
}

func formatDiscord(evt NotificationEvent) ([]byte, error) {
	color := 0x0284C7 // info (blue)
	switch evt.Severity {
	case SeverityWarning:
		color = 0xF59E0B // amber
	case SeverityCritical:
		color = 0xEF4444 // red
	}

	fields := []map[string]any{
		{"name": "Severity", "value": strings.ToUpper(string(evt.Severity)), "inline": true},
	}
	if evt.DeviceName != "" {
		fields = append(fields, map[string]any{"name": "Device", "value": evt.DeviceName, "inline": true})
	}
	if evt.ElementName != "" {
		fields = append(fields, map[string]any{"name": "Element", "value": evt.ElementName, "inline": true})
	}
	if evt.Value != nil {
		fields = append(fields, map[string]any{"name": "Value", "value": fmt.Sprintf("%v", evt.Value), "inline": true})
	}

	payload := map[string]any{
		"embeds": []map[string]any{
			{
				"title":       evt.Title,
				"description": evt.Message,
				"color":       color,
				"fields":      fields,
				"timestamp":   evt.Timestamp.UTC().Format(time.RFC3339),
				"footer": map[string]any{
					"text": "Quack Quack IoT Telemetry",
				},
			},
		},
	}
	return json.Marshal(payload)
}

func formatTeams(evt NotificationEvent) ([]byte, error) {
	facts := []map[string]string{
		{"title": "Severity", "value": strings.ToUpper(string(evt.Severity))},
	}
	if evt.DeviceName != "" {
		facts = append(facts, map[string]string{"title": "Device", "value": evt.DeviceName})
	}
	if evt.ElementName != "" {
		facts = append(facts, map[string]string{"title": "Element", "value": evt.ElementName})
	}
	if evt.Value != nil {
		facts = append(facts, map[string]string{"title": "Value", "value": fmt.Sprintf("%v", evt.Value)})
	}

	payload := map[string]any{
		"type": "message",
		"attachments": []map[string]any{
			{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.5",
					"body": []map[string]any{
						{
							"type":   "TextBlock",
							"size":   "Medium",
							"weight": "Bolder",
							"text":   evt.Title,
						},
						{
							"type": "TextBlock",
							"text": evt.Message,
							"wrap": true,
						},
						{
							"type":  "FactSet",
							"facts": facts,
						},
						{
							"type":     "TextBlock",
							"size":     "Small",
							"isSubtle": true,
							"text":     fmt.Sprintf("Quack Quack IoT • %s", evt.Timestamp.UTC().Format(time.RFC1123)),
						},
					},
				},
			},
		},
	}
	return json.Marshal(payload)
}

func formatTelegram(evt NotificationEvent) ([]byte, error) {
	icon := "ℹ️"
	switch evt.Severity {
	case SeverityWarning:
		icon = "⚠️"
	case SeverityCritical:
		icon = "🚨"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s <b>[%s] %s</b>\n\n", icon, strings.ToUpper(string(evt.Severity)), evt.Title)
	fmt.Fprintf(&sb, "%s\n\n", evt.Message)
	if evt.DeviceName != "" {
		fmt.Fprintf(&sb, "<b>Device:</b> %s\n", evt.DeviceName)
	}
	if evt.ElementName != "" {
		fmt.Fprintf(&sb, "<b>Element:</b> %s\n", evt.ElementName)
	}
	if evt.Value != nil {
		fmt.Fprintf(&sb, "<b>Value:</b> <code>%v</code>\n", evt.Value)
	}
	fmt.Fprintf(&sb, "<i>%s</i>", evt.Timestamp.UTC().Format(time.RFC1123))

	payload := map[string]any{
		"text":       sb.String(),
		"parse_mode": "HTML",
	}
	return json.Marshal(payload)
}

func formatCustom(evt NotificationEvent, customTemplate *string) ([]byte, error) {
	if customTemplate == nil || strings.TrimSpace(*customTemplate) == "" {
		return formatStandard(evt)
	}

	tmpl, err := template.New("webhook").Parse(*customTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse custom template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, evt); err != nil {
		return nil, fmt.Errorf("execute custom template: %w", err)
	}

	return buf.Bytes(), nil
}

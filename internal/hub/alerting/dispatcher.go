package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Dispatcher transmits alert notifications to configured webhook destinations.
type Dispatcher struct {
	client *http.Client
}

// NewDispatcher returns an initialized Dispatcher with a reasonable HTTP timeout.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// discordEmbedField represents a key-value detail field in a Discord embed.
type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// discordEmbed mirrors Discord's embed JSON object schema.
type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Color       int                 `json:"color"`
	Fields      []discordEmbedField `json:"fields"`
	Timestamp   string              `json:"timestamp"`
	Footer      struct {
		Text string `json:"text"`
	} `json:"footer"`
}

// slackAttachment mirrors Slack-compatible attachment schema.
type slackAttachment struct {
	Color  string `json:"color"`
	Title  string `json:"title"`
	Text   string `json:"text"`
	Ts     int64  `json:"ts"`
}

// multiWebhookPayload formats the notification so it renders rich embeds in Discord
// while remaining fully compatible with Slack and generic JSON webhook receivers.
type multiWebhookPayload struct {
	Content     string            `json:"content"`               // Discord plaintext
	Text        string            `json:"text"`                  // Slack plaintext
	Username    string            `json:"username"`              // Bot name
	Embeds      []discordEmbed    `json:"embeds,omitempty"`      // Discord embeds
	Attachments []slackAttachment `json:"attachments,omitempty"` // Slack attachments
}

// Send formats and delivers an AlertEvent to the specified webhook URL.
func (d *Dispatcher) Send(ctx context.Context, webhookURL string, event AlertEvent) error {
	if webhookURL == "" {
		return fmt.Errorf("dispatcher: webhook url is empty")
	}

	statusWord := "FIRING"
	icon := "🚨"
	colorDec := 15680324 // #EF4444 (red)
	colorHex := "#EF4444"

	if event.IsResolved {
		statusWord = "RESOLVED"
		icon = "✅"
		colorDec = 2278750 // #22C55E (green)
		colorHex = "#22C55E"
	}

	title := fmt.Sprintf("%s [%s] %s Alert: %s", icon, statusWord, event.Metric, event.Hostname)
	desc := fmt.Sprintf("Node **%s** (%s) has triggered an alert state.", event.Hostname, event.NodeID)
	if event.IsResolved {
		desc = fmt.Sprintf("Node **%s** (%s) alert condition has cleared.", event.Hostname, event.NodeID)
	}

	payload := multiWebhookPayload{
		Content:  fmt.Sprintf("%s **[%s] %s Alert on %s** (%s)", icon, statusWord, event.Metric, event.Hostname, event.CurrentValue),
		Text:     fmt.Sprintf("%s [%s] %s Alert on %s (%s)", icon, statusWord, event.Metric, event.Hostname, event.CurrentValue),
		Username: "Telemetry AlertBot",
		Embeds: []discordEmbed{
			{
				Title:       title,
				Description: desc,
				Color:       colorDec,
				Fields: []discordEmbedField{
					{Name: "Node", Value: event.Hostname, Inline: true},
					{Name: "Metric", Value: event.Metric, Inline: true},
					{Name: "Status", Value: statusWord, Inline: true},
					{Name: "Current Value", Value: event.CurrentValue, Inline: true},
					{Name: "Threshold", Value: event.Threshold, Inline: true},
					{Name: "Node ID", Value: event.NodeID, Inline: true},
				},
				Timestamp: event.Timestamp.UTC().Format(time.RFC3339),
				Footer: struct {
					Text string `json:"text"`
				}{Text: "DevOps Telemetry Alert Engine"},
			},
		},
		Attachments: []slackAttachment{
			{
				Color: colorHex,
				Title: title,
				Text:  fmt.Sprintf("Metric: %s | Value: %s | Threshold: %s", event.Metric, event.CurrentValue, event.Threshold),
				Ts:    event.Timestamp.Unix(),
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dispatcher: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("dispatcher: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TelemetryHub-AlertEngine/1.0")

	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("dispatcher: send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dispatcher: webhook returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// TestWebhook triggers a test notification to verify delivery to the given webhook URL.
func (d *Dispatcher) TestWebhook(ctx context.Context, webhookURL string) error {
	if webhookURL == "" {
		return fmt.Errorf("dispatcher: webhook url is empty")
	}

	event := AlertEvent{
		NodeID:       "test-node",
		Hostname:     "telemetry-hub-test",
		Metric:       "Test Notification",
		CurrentValue: "Operational",
		Threshold:    "N/A",
		IsResolved:   true,
		Timestamp:    time.Now().UTC(),
	}

	return d.Send(ctx, webhookURL, event)
}

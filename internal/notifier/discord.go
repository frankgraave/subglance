package notifier

import (
	"context"
	"net/http"
	"strings"
)

// DiscordSender posts an embed to a Discord webhook.
//
// Discord accepts a bare content string, but an embed is what makes the alert
// scannable in a busy channel: the colour bar is visible before any text is
// read, which is the difference between noticing an outage and scrolling past
// it.
type DiscordSender struct{ client *http.Client }

// NewDiscordSender builds a Discord channel.
func NewDiscordSender() *DiscordSender {
	return &DiscordSender{client: newHTTPClient()}
}

// Discord embed colours, as the decimal integers its API expects.
const (
	discordRed   = 0xE5484D
	discordGreen = 0x30A46C
)

// Validate checks the webhook URL.
func (s *DiscordSender) Validate(cfg map[string]string) error {
	if err := validateHTTPSURL(cfg["url"], "url"); err != nil {
		return err
	}
	// Catching the wrong-product paste here saves a confusing 404 later:
	// Slack and Discord webhook URLs look similar enough that they get
	// mixed up, and Discord's rejection does not say so.
	if !strings.Contains(cfg["url"], "discord") {
		return errNotDiscordURL
	}
	return nil
}

var errNotDiscordURL = &configError{"url does not look like a Discord webhook (expected discord.com or discordapp.com)"}

// Send posts the embed.
func (s *DiscordSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	colour := discordGreen
	if a.Down() {
		colour = discordRed
	}

	payload := map[string]any{
		"username": "SubGlance",
		"embeds": []map[string]any{{
			"title":       a.Title(),
			"description": a.Body(),
			"color":       colour,
			"timestamp":   a.At.UTC().Format("2006-01-02T15:04:05Z"),
		}},
	}

	req, err := jsonRequest(ctx, cfg["url"], payload)
	if err != nil {
		return err
	}
	return httpSend(ctx, s.client, req)
}

// configError is a validation failure that is not worth a retry and not worth
// a stack of wrapping either.
type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }

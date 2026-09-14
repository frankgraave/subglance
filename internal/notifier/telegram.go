package notifier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// TelegramSender sends a message through the Bot API.
//
// Unlike the webhook channels this one needs two settings, a bot token and a
// chat id, and the token is a credential rather than a URL. That is why it is
// built from parts here instead of being stored as a ready-made URL: a token
// in a column named "url" would end up in logs and error messages that
// reasonably assume a URL is not a secret.
type TelegramSender struct {
	client *http.Client

	// apiBase is the Bot API root, overridden in tests. Nothing else sets
	// it: pointing a production install at a different Telegram would be a
	// way to leak the token, not a feature.
	apiBase string
}

// NewTelegramSender builds a Telegram channel.
func NewTelegramSender() *TelegramSender {
	return &TelegramSender{client: newHTTPClient(), apiBase: "https://api.telegram.org"}
}

// Validate checks that both settings are present.
func (s *TelegramSender) Validate(cfg map[string]string) error {
	if strings.TrimSpace(cfg["bot_token"]) == "" {
		return &configError{"bot_token is required"}
	}
	if strings.TrimSpace(cfg["chat_id"]) == "" {
		return &configError{"chat_id is required"}
	}
	// A bot token is "<digits>:<secret>". Checking the shape catches the
	// common paste error — the bot's @name instead of its token — with a
	// message that says what is wrong, rather than a 401 from Telegram
	// that says only "Unauthorized".
	if !strings.Contains(cfg["bot_token"], ":") {
		return &configError{"bot_token does not look like a bot token (expected 123456:ABC-DEF…)"}
	}
	return nil
}

// Send posts the message.
func (s *TelegramSender) Send(ctx context.Context, cfg map[string]string, a Alert) error {
	base := s.apiBase
	if base == "" {
		base = "https://api.telegram.org"
	}

	marker := "🟢"
	if a.Down() {
		marker = "🔴"
	}

	payload := map[string]any{
		"chat_id": cfg["chat_id"],
		"text":    fmt.Sprintf("%s %s\n\n%s", marker, a.Title(), a.Body()),
		// Plain text, not Markdown or HTML: a monitor name containing an
		// underscore or an angle bracket would otherwise either break the
		// parse or, worse, be silently reformatted. The name has to
		// arrive exactly as the operator typed it.
		"disable_web_page_preview": true,
	}

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(base, "/"), cfg["bot_token"])
	req, err := jsonRequest(ctx, endpoint, payload)
	if err != nil {
		return err
	}

	if err := httpSend(ctx, s.client, req); err != nil {
		// The token is in the URL, so any error mentioning it would put
		// the credential in the outbox's last_error column and from
		// there into the interface. Strip it before the message escapes.
		return scrubToken(err, cfg["bot_token"])
	}
	return nil
}

// scrubToken removes a secret from an error message.
func scrubToken(err error, token string) error {
	if token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "bot_token")
	var r *Retryable
	if errors.As(err, &r) {
		return &Retryable{Err: &configError{msg}}
	}
	return &configError{msg}
}

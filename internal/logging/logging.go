// Package logging builds the application's structured logger.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// New returns a slog.Logger for the given level and format.
//
// level is one of debug, info, warn, error; format is "text" or "json".
// Unrecognised values fall back to info/text rather than failing — config
// validation is where bad input is rejected, not here.
func New(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	if strings.EqualFold(format, "json") {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

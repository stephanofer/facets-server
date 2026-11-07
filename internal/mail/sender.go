package mail

import (
	"context"
	"errors"
	"log/slog"
)

type Sender interface {
	Send(context.Context, Message) (string, error)
}

// LoadSender selects local delivery only for development without an external
// sender. SES configuration or delivery errors never trigger a log fallback.
func LoadSender(ctx context.Context, environment string, opts Options, log *slog.Logger) (Sender, error) {
	if environment == "development" && opts.From == "" {
		if log == nil {
			return nil, errors.New("development mail requires a logger")
		}
		log.Warn("development mail logs contain recipients and confirmation codes; do not use real user data")
		return &logSender{log: log}, nil
	}
	return LoadSES(ctx, opts)
}

// logSender is deliberately unexported: construction must pass the environment
// gate above. It shares the normal handler's validation and secret cleanup path.
type logSender struct{ log *slog.Logger }

func (s *logSender) Send(ctx context.Context, m Message) (string, error) {
	if err := validateMessage(m); err != nil {
		return "", &DeliveryError{code: "invalid_message", cause: err, permanent: true}
	}
	if err := ctx.Err(); err != nil {
		return "", &DeliveryError{code: "cancelled_before_send", cause: err}
	}
	s.log.LogAttrs(ctx, slog.LevelWarn, "development mail delivery",
		slog.Any("message", m))
	return "development-log", nil
}

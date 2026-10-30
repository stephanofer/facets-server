// Package mail provides bounded transactional email delivery through Amazon SES.
package mail

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stephanofer/facets-server/internal/execution"
)

// Message contains rendered content, never executable templates or AWS details.
// Body is plain text; HTML is optional. At least one body must be nonempty.
type Message struct {
	To      string   `json:"to"`
	Cc      []string `json:"cc,omitempty"`
	Bcc     []string `json:"bcc,omitempty"`
	ReplyTo []string `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	Body    string   `json:"body,omitempty"`
	HTML    string   `json:"html,omitempty"`
}

type Options struct {
	From    string
	Timeout time.Duration
}

// SESAPI is the narrow SDK boundary used for deterministic tests.
type SESAPI interface {
	SendEmail(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}

type SES struct {
	client SESAPI
	opts   Options
}

// LoadSES uses the SDK's region, credentials, HTTPS transport and request signing.
// Call once when composing a module that actually needs email, not on every send.
func LoadSES(ctx context.Context, opts Options) (*SES, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, &DeliveryError{code: "aws_configuration", cause: err, permanent: true}
	}
	if cfg.Region == "" {
		return nil, errors.New("mail requires an AWS region")
	}
	return NewSES(sesv2.NewFromConfig(cfg), opts)
}

func NewSES(client SESAPI, opts Options) (*SES, error) {
	if client == nil {
		return nil, errors.New("mail client required")
	}
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	return &SES{client: client, opts: opts}, nil
}

func validateOptions(opts Options) error {
	if !validAddress(opts.From) || opts.Timeout < time.Millisecond || opts.Timeout > 5*time.Minute {
		return errors.New("invalid mail configuration")
	}
	return nil
}

// Deliver adapts the durable worker contract. The job ID is NOT an SES idempotency
// token. Producers must use outbox deduplication and ExternalEffect: true.
func (s *SES) Deliver(ctx context.Context, _ string, payload []byte) (string, error) {
	var m Message
	if len(payload) > 65536 || json.Unmarshal(payload, &m) != nil {
		return "", &DeliveryError{code: "invalid_payload", permanent: true}
	}
	return s.Send(ctx, m)
}

// Send returns SES's acceptance ID, not proof of delivery to the mailbox.
func (s *SES) Send(ctx context.Context, m Message) (string, error) {
	if err := validateMessage(m); err != nil {
		return "", &DeliveryError{code: "invalid_message", cause: err, permanent: true}
	}
	if err := ctx.Err(); err != nil {
		return "", &DeliveryError{code: "cancelled_before_send", cause: err}
	}
	ctx, cancel := context.WithTimeout(ctx, s.opts.Timeout)
	defer cancel()
	body := &types.Body{}
	if m.Body != "" {
		body.Text = &types.Content{Data: new(m.Body), Charset: new("UTF-8")}
	}
	if m.HTML != "" {
		body.Html = &types.Content{Data: new(m.HTML), Charset: new("UTF-8")}
	}
	out, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: new(s.opts.From),
		Destination:      &types.Destination{ToAddresses: []string{m.To}, CcAddresses: m.Cc, BccAddresses: m.Bcc},
		ReplyToAddresses: m.ReplyTo,
		Content: &types.EmailContent{Simple: &types.Message{
			Subject: &types.Content{Data: new(m.Subject), Charset: new("UTF-8")}, Body: body,
		}},
	}, func(o *sesv2.Options) {
		// SendEmail has no idempotency token. Default network/5xx retries could
		// duplicate mail after a lost acknowledgement. Retain SDK jitter, backoff
		// and retry budgets, but retry only explicit throttling rejections.
		o.Retryer = retry.NewStandard(func(o *retry.StandardOptions) {
			o.MaxAttempts = 3
			o.Retryables = []retry.IsErrorRetryable{retry.IsErrorRetryableFunc(func(err error) aws.Ternary {
				return aws.BoolTernary(isThrottled(err))
			})}
		})
	})
	if err != nil {
		return "", classifyError(err)
	}
	if out == nil || out.MessageId == nil || *out.MessageId == "" || len(*out.MessageId) > 256 {
		return "", &DeliveryError{code: "missing_acceptance_id", uncertain: true}
	}
	return *out.MessageId, nil
}

func validateMessage(m Message) error {
	if !validAddress(m.To) || m.Subject == "" || len(m.Subject) > 200 || strings.ContainsAny(m.Subject, "\r\n\x00") || !utf8.ValidString(m.Subject) {
		return errors.New("invalid mail headers")
	}
	if len(m.Cc)+len(m.Bcc)+1 > 50 || len(m.ReplyTo) > 10 {
		return errors.New("too many mail addresses")
	}
	for _, list := range [][]string{m.Cc, m.Bcc, m.ReplyTo} {
		for _, address := range list {
			if !validAddress(address) {
				return errors.New("invalid mail address")
			}
		}
	}
	if (m.Body == "" && m.HTML == "") || len(m.Body)+len(m.HTML) > 60000 || !utf8.ValidString(m.Body) || !utf8.ValidString(m.HTML) || strings.ContainsRune(m.Body, '\x00') || strings.ContainsRune(m.HTML, '\x00') {
		return errors.New("invalid mail content")
	}
	return nil
}

func validAddress(s string) bool {
	if len(s) > 254 || strings.ContainsAny(s, "\r\n\x00") {
		return false
	}
	// SES does not support internationalized mailbox local parts. Callers must
	// provide ASCII addresses (punycode for internationalized domains).
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// DeliveryError exposes safe diagnostics; the underlying SDK error is preserved
// for errors.Is/As only and may contain private data. Never log the cause.
type DeliveryError struct {
	code, requestID      string
	cause                error
	permanent, uncertain bool
}

func (e *DeliveryError) Error() string     { return "mail send failed: " + e.code }
func (e *DeliveryError) Unwrap() error     { return e.cause }
func (e *DeliveryError) ErrorCode() string { return e.code }
func (e *DeliveryError) RequestID() string { return e.requestID }
func (e *DeliveryError) Is(target error) bool {
	return (e.uncertain && target == execution.ErrUncertain) || (e.permanent && target == execution.ErrPermanent)
}

func isThrottled(err error) bool {
	api, ok := errors.AsType[smithy.APIError](err)
	return ok && api.ErrorCode() == "TooManyRequestsException"
}

func classifyError(err error) error {
	e := &DeliveryError{code: "unknown_result", cause: err, uncertain: true}
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok {
		status := response.HTTPStatusCode()
		if status >= 400 && status < 500 && status != 408 {
			e.uncertain, e.permanent = false, status != 429
		}
	}
	if response, ok := errors.AsType[interface {
		error
		ServiceRequestID() string
	}](err); ok {
		e.requestID = safeToken(response.ServiceRequestID())
	}
	if api, ok := errors.AsType[smithy.APIError](err); ok {
		e.code = safeToken(api.ErrorCode())
		if api.ErrorFault() == smithy.FaultClient {
			e.uncertain, e.permanent = false, true
		}
		if isThrottled(err) || api.ErrorCode() == "LimitExceededException" {
			e.uncertain, e.permanent = false, false
		}
	}
	return e
}

func safeToken(s string) string {
	if s == "" || len(s) > 128 {
		return "unknown"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return "unknown"
		}
	}
	return s
}

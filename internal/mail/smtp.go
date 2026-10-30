// Package mail delivers bounded plaintext messages over SMTP without logging content.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json/v2"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/stephanofer/facets-server/internal/execution"
)

type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}
type SMTP struct {
	Address, From, Username, Password string
	RequireTLS                        bool
	Timeout                           time.Duration
}

func (s SMTP) Deliver(ctx context.Context, id string, payload []byte) (result string, err error) {
	var m Message
	if len(payload) > 65536 || json.Unmarshal(payload, &m) != nil {
		return "", errors.New("invalid mail payload")
	}
	if !validAddress(s.From) || !validAddress(m.To) || strings.ContainsAny(m.Subject, "\r\n") || len(m.Subject) > 200 || len(m.Body) > 60000 || strings.ContainsAny(id, "\r\n<>") {
		return "", errors.New("invalid mail message")
	}
	if s.Timeout <= 0 {
		return "", errors.New("mail deadline required")
	}
	host, _, err := net.SplitHostPort(s.Address)
	if err != nil {
		return "", errors.New("invalid SMTP address")
	}
	if !s.RequireTLS {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("plaintext SMTP is restricted to loopback tests")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.Address)
	if err != nil {
		return "", errors.New("SMTP connection failed")
	}
	// Connection cleanup cannot invalidate a known DATA acceptance and trigger a
	// resend. Delivery failures are determined by protocol operations below.
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", errors.New("SMTP deadline failed")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return "", errors.New("SMTP greeting failed")
	}
	if s.RequireTLS {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return "", errors.New("SMTP TLS failed")
		}
	}
	if s.Username != "" {
		if !s.RequireTLS {
			return "", errors.New("SMTP authentication requires TLS")
		}
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return "", errors.New("SMTP authentication failed")
		}
	}
	if err := client.Mail(s.From); err != nil {
		return "", errors.New("SMTP sender rejected")
	}
	if err := client.Rcpt(m.To); err != nil {
		return "", errors.New("SMTP recipient rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return "", errors.New("SMTP data rejected")
	}
	// Once DATA begins, a failed write or final acknowledgement can have an unknown
	// result. Message-ID helps investigation but is NOT provider idempotency.
	var body bytes.Buffer
	encoder := quotedprintable.NewWriter(&body)
	if _, err := encoder.Write([]byte(m.Body)); err != nil {
		return "", execution.ErrUncertain
	}
	if err := encoder.Close(); err != nil {
		return "", execution.ErrUncertain
	}
	_, domain, _ := strings.CutLast(s.From, "@")
	content := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s>\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n",
		s.From, m.To, mime.QEncoding.Encode("UTF-8", m.Subject), id+"@"+domain,
		body.String(),
	)
	if _, err := writer.Write([]byte(content)); err != nil {
		return "", execution.ErrUncertain
	}
	if err := writer.Close(); err != nil {
		return "", execution.ErrUncertain
	}
	// Acceptance is known after DATA's 250 response. QUIT failure must not resend.
	return id, nil
}
func validAddress(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && !strings.ContainsAny(s, "\r\n")
}

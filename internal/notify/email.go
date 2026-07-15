package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
	"strings"
	"time"

	"github.com/ebnsina/sabab-api/internal/config"
)

// EmailSender delivers over SMTP.
//
// When SMTP is not configured (the common case for a fresh self-host), it logs
// the alert instead of failing. That is deliberate: a self-hoster who has not
// set up mail yet should still be able to create an email rule and SEE that it
// would have fired, rather than hitting an error that looks like a bug. The log
// line says plainly that mail is not configured.
type EmailSender struct {
	cfg config.SMTP
	log *slog.Logger
}

// NewEmailSender builds the sender. A nil-ish (host-less) config puts it in
// log-only mode.
func NewEmailSender(cfg config.SMTP, log *slog.Logger) *EmailSender {
	return &EmailSender{cfg: cfg, log: log}
}

func (*EmailSender) Kind() string { return "email" }

func (s *EmailSender) Send(ctx context.Context, cfg ChannelConfig, n Notification) error {
	to := cfg.Settings["to"]
	if to == "" {
		return fmt.Errorf("email channel is missing a to-address")
	}

	if s.cfg.Host == "" {
		// Not configured: record what would have gone out, so a rule can be
		// tested end to end before mail is wired up.
		s.log.Warn("email not configured; alert not sent",
			slog.String("to", to),
			slog.String("subject", subject(n)))
		return nil
	}

	msg := buildMessage(s.cfg.From, to, n)

	// smtp.SendMail does not take a context, so we run it in a goroutine and let
	// ctx cancel the wait. The dial itself is bounded by the SMTP server, and a
	// leaked goroutine on a hung server is preferable to blocking the alert loop.
	done := make(chan error, 1)
	go func() {
		done <- s.send(to, msg)
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("smtp send timed out: %w", ctx.Err())
	case err := <-done:
		return err
	}
}

func (s *EmailSender) send(to, msg string) error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)

	var auth smtp.Auth
	if s.cfg.Username != "" {
		// PlainAuth refuses to send credentials over an unencrypted link unless
		// the host is localhost, which is the correct default — it stops a
		// misconfiguration from leaking the SMTP password in cleartext.
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}

	if err := smtp.SendMail(addr, auth, s.cfg.From, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

func subject(n Notification) string {
	return fmt.Sprintf("[%s] %s: %s", n.ProjectName, n.Reason, truncate(n.Title, 120))
}

// buildMessage assembles a minimal, correct RFC 5322 message. Plain text only —
// an alert email needs to be legible in any client and forwardable to a pager,
// not pretty.
func buildMessage(from, to string, n Notification) string {
	var b strings.Builder

	fmt.Fprintf(&b, "From: Sabab <%s>\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject(n))
	fmt.Fprintf(&b, "Date: %s\r\n", n.FiredAt.UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")

	fmt.Fprintf(&b, "%s in %s\r\n\r\n", n.Reason, n.ProjectName)
	fmt.Fprintf(&b, "%s\r\n", n.Title)
	if n.Culprit != "" {
		fmt.Fprintf(&b, "%s\r\n", n.Culprit)
	}
	b.WriteString("\r\n")

	if line := metaLine(n); line != "" {
		fmt.Fprintf(&b, "%s\r\n\r\n", strings.ReplaceAll(line, "·", "|"))
	}
	if n.URL != "" {
		fmt.Fprintf(&b, "View issue: %s\r\n", n.URL)
	}

	return b.String()
}

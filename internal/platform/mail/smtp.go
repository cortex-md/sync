package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"time"

	"github.com/cortexnotes/cortex-sync/internal/domain"
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string
	Timeout  time.Duration
}

type SMTPMailer struct {
	cfg      SMTPConfig
	from     mail.Address
	renderer *Renderer
}

func NewSMTPMailer(cfg SMTPConfig, renderer *Renderer) (*SMTPMailer, error) {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("invalid mail from address: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &SMTPMailer{cfg: cfg, from: *from, renderer: renderer}, nil
}

func (m *SMTPMailer) Send(ctx context.Context, email domain.OutboxEmail) error {
	rendered, err := m.renderer.Render(email.Template, email.Locale, email.Data)
	if err != nil {
		return err
	}
	to, err := mail.ParseAddress(email.To)
	if err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}

	message, err := buildMessage(m.from, *to, email.Template, rendered)
	if err != nil {
		return err
	}

	client, err := m.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if m.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(m.from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(to.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (m *SMTPMailer) dial(ctx context.Context) (*smtp.Client, error) {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	dialer := &net.Dialer{Timeout: m.cfg.Timeout}
	deadline := time.Now().Add(m.cfg.Timeout)
	tlsConfig := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if m.cfg.TLSMode == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("smtp dial: %w", err)
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if m.cfg.TLSMode == "starttls" {
		if err := client.StartTLS(tlsConfig); err != nil {
			client.Close()
			return nil, fmt.Errorf("smtp starttls: %w", err)
		}
	}
	return client, nil
}

func buildMessage(from, to mail.Address, template string, rendered *Rendered) ([]byte, error) {
	boundary := randomBoundary()
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from.String())
	fmt.Fprintf(&buf, "To: %s\r\n", to.String())
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", rendered.Subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%s@%s>\r\n", randomBoundary(), domainOf(from.Address))
	fmt.Fprintf(&buf, "X-Cortex-Template: %s\r\n", template)
	buf.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	for _, part := range []struct{ contentType, body string }{
		{"text/plain; charset=utf-8", rendered.Text},
		{"text/html; charset=utf-8", rendered.HTML},
	} {
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", part.contentType)
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp := quotedprintable.NewWriter(&buf)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
		buf.WriteString("\r\n")
	}
	fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	return buf.Bytes(), nil
}

func randomBoundary() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func domainOf(address string) string {
	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == '@' {
			return address[i+1:]
		}
	}
	return "localhost"
}

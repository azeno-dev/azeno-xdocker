// Package notify sends job summaries by email (SMTP) and webhook.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// SMTP holds mail settings.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	To       []string
	StartTLS bool
}

// Message is a notification.
type Message struct {
	Title   string         `json:"title"`
	Text    string         `json:"text"`
	Status  string         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
}

// Email sends m by SMTP.
func Email(ctx context.Context, s SMTP, m Message) error {
	if s.Host == "" || s.From == "" || len(s.To) == 0 {
		return fmt.Errorf("email is not configured")
	}
	if s.Port == 0 {
		s.Port = 587
	}
	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	var body bytes.Buffer
	fmt.Fprintf(&body, "From: %s\r\nTo: %s\r\nSubject: [xDocker] %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		s.From, strings.Join(s.To, ", "), m.Title, time.Now().Format(time.RFC1123Z), m.Text)

	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if s.Port == 465 {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: s.Host})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
				return err
			}
		} else if s.StartTLS {
			return fmt.Errorf("server does not offer STARTTLS")
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	for _, to := range s.To {
		if err := c.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body.Bytes()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Webhook POSTs m as JSON to url.
func Webhook(ctx context.Context, url string, m Message) error {
	if url == "" {
		return fmt.Errorf("webhook is not configured")
	}
	b, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook answered HTTP %d", resp.StatusCode)
	}
	return nil
}

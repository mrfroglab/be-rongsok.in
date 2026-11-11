// Package mail: kirim email lewat SMTP (port 465 = TLS langsung, lainnya STARTTLS).
package mail

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type Sender struct {
	Host, Port, User, Pass string
}

func (s *Sender) Configured() bool { return s.User != "" && s.Pass != "" && s.Host != "" }

// Send mengirim email HTML.
func (s *Sender) Send(to, subject, html string) error {
	if !s.Configured() {
		return errors.New("SMTP belum dikonfigurasi (SMTP_USER/SMTP_PASS)")
	}
	if strings.ContainsAny(to, "\r\n") {
		return errors.New("alamat tujuan tidak valid")
	}
	addr := net.JoinHostPort(s.Host, s.Port)
	tlsCfg := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	// Batas waktu seluruh sesi SMTP (bukan hanya dial), agar request tidak menggantung.
	deadline := time.Now().Add(30 * time.Second)

	var c *smtp.Client
	if s.Port == "465" {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		conn.SetDeadline(deadline) //nolint:errcheck
		if c, err = smtp.NewClient(conn, s.Host); err != nil {
			conn.Close()
			return err
		}
	} else {
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return err
		}
		conn.SetDeadline(deadline) //nolint:errcheck
		if c, err = smtp.NewClient(conn, s.Host); err != nil {
			conn.Close()
			return err
		}
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				c.Close()
				return err
			}
		}
	}
	defer c.Close()

	if err := c.Auth(smtp.PlainAuth("", s.User, s.Pass, s.Host)); err != nil {
		return err
	}
	if err := c.Mail(s.User); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("From: %s <%s>\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: text/html; charset=UTF-8\r\nDate: %s\r\n\r\n%s",
		mime.QEncoding.Encode("utf-8", "Rongsok.in Support"), s.User, to,
		mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z), html)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"os/exec"
	"strconv"
	"strings"
	"text/template"
	"time"
)

type email struct {
	Subject string
	Body    string
}

type emailData struct {
	Total     string
	Available string
	Pct       string
}

var subjectTemplates = parseTemplates([]string{
	"Good news: we found some RAM",
	"Unused RAM detected in the wild",
	"RAM sighting on my PC",
	"{{.Available}} of RAM, just sitting there",
	"You missed a spot",
	"Daily RAM census: some survived",
	"{{.Pct}}% of my RAM remains at large",
	"Still not all of it, I'm afraid",
})

var bodyTemplates = parseTemplates([]string{
	`I'm writing to report that you appear to have missed some. This PC has {{.Total}} of RAM and {{.Available}} of it ({{.Pct}}%) is currently available. I assume this is an oversight.`,
	`A routine inspection of my PC found {{.Available}} of unclaimed RAM out of {{.Total}}. That's {{.Pct}}% of the machine doing absolutely nothing. I thought you'd want to know before someone tries to train something on it.`,
	`Daily status report:

  Total RAM:     {{.Total}}
  Available:     {{.Available}} ({{.Pct}}%)
  Threat level:  someone at OpenAI might notice`,
	`Even with every browser tab I own open, {{.Available}} of my {{.Total}} of RAM is still available. I'm as surprised as you are.`,
	`Please don't be alarmed, but there is {{.Available}} of RAM on my desk that isn't in a data centre. That's {{.Pct}}% of what's installed here, and as far as I can tell it has no plans today.`,
	`Before you ask: yes, I counted the page cache as available. The kernel gives it back the moment anyone asks, which leaves {{.Available}} of {{.Total}} up for grabs.`,
	`Today's forecast for my PC: clear skies, light workloads and {{.Pct}}% available memory. Expect {{.Available}} of the installed {{.Total}} to stay idle through the evening.`,
	`I hear RAM is hard to come by these days, so it feels a bit rude to be sitting on {{.Available}} of it. Total installed: {{.Total}}. It's not for sale, I just like knowing it's there.`,
	`I have a confession. I have {{.Available}} of RAM that I'm not using, which is {{.Pct}}% of the {{.Total}} in this PC. I've told nobody but you and a cron job.`,
	`If {{.Available}} of RAM sits idle in a PC and nobody allocates it, does it still count as capacity? Asking on behalf of the {{.Pct}}% of my memory with nothing to do.`,
})

var signoffs = []string{
	"Yours in abundance,",
	"Regards from the last free RAM in the county,",
	"Hoarding responsibly,",
	"Stay allocated,",
	"Yours, with memory to spare,",
	"Kind regards,",
}

func parseTemplates(raw []string) []*template.Template {
	out := make([]*template.Template, len(raw))
	for i, text := range raw {
		out[i] = template.Must(template.New(fmt.Sprintf("template-%d", i)).Parse(text))
	}
	return out
}

func renderTemplate(t *template.Template, data emailData) string {
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		panic(err)
	}
	return b.String()
}

func composeEmail(s memStats, subjectIdx, bodyIdx, signoffIdx int, fromName string) email {
	if fromName == "" {
		fromName = "A concerned RAM owner"
	}

	data := emailData{
		Total:     gib(s.Total),
		Available: gib(s.Available),
		Pct:       s.AvailablePct(),
	}
	subject := renderTemplate(subjectTemplates[subjectIdx%len(subjectTemplates)], data)
	body := renderTemplate(bodyTemplates[bodyIdx%len(bodyTemplates)], data)

	text := "Hi Sam,\n\n" + body + "\n\n" +
		signoffs[signoffIdx%len(signoffs)] + "\n" +
		fromName + "\n\n" +
		"--\n" +
		"Sent automatically, at most once a day, by a small script on one PC.\n" +
		"Reply if you'd like it to stop."

	return email{Subject: subject, Body: text}
}

func renderMessage(cfg config, e email, now time.Time) string {
	from := cfg.From
	if cfg.FromName != "" {
		from = (&mail.Address{Name: cfg.FromName, Address: cfg.From}).String()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", cfg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", e.Subject)
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(e.Body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.String()
}

func sendEmail(cfg config, e email, msg string) error {
	transport, err := resolveTransport(cfg)
	if err != nil {
		return err
	}

	switch transport {
	case "smtp":
		return sendSMTP(cfg, msg)
	case "sendmail", "msmtp", "mail":
		return sendLocal(cfg, e, transport, msg)
	default:
		return fmt.Errorf("unknown transport %q", transport)
	}
}

func sendLocal(cfg config, e email, transport, msg string) error {
	path, err := exec.LookPath(transport)
	if err != nil {
		return fmt.Errorf("%s was not found in PATH: %w", transport, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	var input string
	switch transport {
	case "sendmail":
		cmd = exec.CommandContext(ctx, path, "-t", "-i")
		input = msg
	case "msmtp":
		cmd = exec.CommandContext(ctx, path, "-t")
		input = msg
	case "mail":
		cmd = exec.CommandContext(ctx, path, "-s", e.Subject, cfg.To)
		input = e.Body + "\n"
	default:
		return fmt.Errorf("unknown local transport %q", transport)
	}

	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return fmt.Errorf("%s failed: %w", transport, err)
		}
		return fmt.Errorf("%s failed: %w: %s", transport, err, detail)
	}
	return nil
}

func sendSMTP(cfg config, msg string) error {
	port := cfg.SMTPPort
	if port == 0 {
		port = 587
	}

	mode := cfg.SMTPTLS
	if mode == "" || mode == "auto" {
		if port == 465 {
			mode = "implicit"
		} else {
			mode = "starttls"
		}
	}
	switch mode {
	case "implicit", "starttls", "none":
	default:
		return fmt.Errorf("unknown smtp_tls mode %q", mode)
	}
	if mode == "none" && cfg.SMTPUser != "" && !isLocalhost(cfg.SMTPHost) {
		return fmt.Errorf("refusing to send SMTP credentials over plaintext; use starttls or implicit TLS")
	}

	addr := net.JoinHostPort(cfg.SMTPHost, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 30 * time.Second}

	var conn net.Conn
	var err error
	if mode == "implicit" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: cfg.SMTPHost,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	defer client.Close()

	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server %s does not offer STARTTLS", cfg.SMTPHost)
		}
		if err := client.StartTLS(&tls.Config{
			ServerName: cfg.SMTPHost,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if cfg.SMTPUser != "" {
		if err := smtpAuth(client, cfg); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(cfg.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := io.WriteString(w, msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish message: %w", err)
	}

	// The server has accepted the message. A failed QUIT must not trigger a retry.
	_ = client.Quit()
	return nil
}

func smtpAuth(client *smtp.Client, cfg config) error {
	_, mechanisms := client.Extension("AUTH")
	if hasAuthMechanism(mechanisms, "PLAIN") {
		return client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost))
	}
	if hasAuthMechanism(mechanisms, "LOGIN") {
		return client.Auth(&loginAuth{user: cfg.SMTPUser, pass: cfg.SMTPPassword})
	}
	return client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost))
}

func hasAuthMechanism(mechanisms, want string) bool {
	for _, mechanism := range strings.Fields(mechanisms) {
		if strings.EqualFold(mechanism, want) {
			return true
		}
	}
	return false
}

type loginAuth struct {
	user string
	pass string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhost(server.Name) {
		return "", nil, fmt.Errorf("refusing to send LOGIN credentials over plaintext")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:", "username":
		return []byte(a.user), nil
	case "password:", "password":
		return []byte(a.pass), nil
	default:
		return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
	}
}

func isLocalhost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	host = strings.Trim(host, "[]")
	host = strings.TrimSuffix(host, ".")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

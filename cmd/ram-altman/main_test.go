package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseMeminfo(t *testing.T) {
	input := `MemTotal:       16000000 kB
MemFree:         300000 kB
MemAvailable:    9000000 kB
Buffers:         200000 kB
Cached:          8000000 kB
SwapCached:      999999 kB
SReclaimable:    500000 kB
Shmem:           100000 kB
`

	stats, err := parseMeminfo(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 16000000*1024 {
		t.Errorf("Total = %d, want %d", stats.Total, uint64(16000000)*1024)
	}
	if stats.Available != 9000000*1024 {
		t.Errorf("Available = %d, want %d", stats.Available, uint64(9000000)*1024)
	}
	if stats.Used != 7000000*1024 {
		t.Errorf("Used = %d, want %d", stats.Used, uint64(7000000)*1024)
	}
	if stats.Free != 300000*1024 {
		t.Errorf("Free = %d, want %d", stats.Free, uint64(300000)*1024)
	}
	wantCache := uint64(200000+8000000+500000-100000) * 1024
	if stats.Cache != wantCache {
		t.Errorf("Cache = %d, want %d", stats.Cache, wantCache)
	}
}

func TestParseMeminfoFallback(t *testing.T) {
	input := `MemTotal:       16000000 kB
MemFree:        1000000 kB
Buffers:        200000 kB
Cached:         3000000 kB
SReclaimable:   500000 kB
Shmem:          700000 kB
`

	stats, err := parseMeminfo(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	wantAvailable := uint64(1000000+200000+3000000+500000-700000) * 1024
	if stats.Available != wantAvailable {
		t.Errorf("Available = %d, want %d", stats.Available, wantAvailable)
	}
	if got := gib(stats.Available); got != "3.8 GiB" {
		t.Errorf("formatted available = %q, want %q", got, "3.8 GiB")
	}
}

func TestThreshold(t *testing.T) {
	tests := []struct {
		name      string
		total     uint64
		available uint64
		pct       int
		want      bool
	}{
		{"exactly ten percent", 16000000, 1600000, 10, false},
		{"just over ten percent", 16000000, 1600001, 10, true},
		{"below ten percent", 16000000, 1000000, 10, false},
		{"zero threshold and zero available", 16000000, 0, 0, false},
		{"zero threshold and some available", 16000000, 1, 0, true},
		{"hundred percent", 16000000, 16000000, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := memStats{Total: tt.total, Available: tt.available}
			if got := stats.overThreshold(tt.pct); got != tt.want {
				t.Errorf("overThreshold(%d) = %v, want %v", tt.pct, got, tt.want)
			}
		})
	}
}

func TestComposeEmailTemplates(t *testing.T) {
	if len(subjectTemplates) != 8 {
		t.Errorf("subject templates = %d, want 8", len(subjectTemplates))
	}
	if len(bodyTemplates) != 10 {
		t.Errorf("body templates = %d, want 10", len(bodyTemplates))
	}
	if len(signoffs) != 6 {
		t.Errorf("signoffs = %d, want 6", len(signoffs))
	}

	stats := memStats{
		Total:     16000000 * 1024,
		Available: 9000000 * 1024,
		Used:      7000000 * 1024,
	}
	for i := range bodyTemplates {
		e := composeEmail(stats, i%len(subjectTemplates), i, i%len(signoffs), "")
		if strings.Contains(e.Subject, "{{") || strings.Contains(e.Body, "{{") {
			t.Fatalf("template %d left an unresolved placeholder", i)
		}
		if !strings.HasPrefix(e.Body, "Hi Sam,\n\n") {
			t.Fatalf("template %d has the wrong opening", i)
		}
		if !strings.Contains(e.Body, "A concerned RAM owner") {
			t.Fatalf("template %d has no default sender name", i)
		}
		if !strings.Contains(e.Body, "Reply if you'd like it to stop.") {
			t.Fatalf("template %d has no footer", i)
		}
	}

	e := composeEmail(stats, 0, 0, 0, "Test Name")
	if !strings.Contains(e.Body, "15.3 GiB") {
		t.Errorf("body does not contain total: %q", e.Body)
	}
	if !strings.Contains(e.Body, "8.6 GiB") {
		t.Errorf("body does not contain available: %q", e.Body)
	}
	if !strings.Contains(e.Body, stats.AvailablePct()+"%") {
		t.Errorf("body does not contain percentage: %q", e.Body)
	}
}

func TestAppendSample(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 9, 12, 34, 56, 0, time.UTC)
	stats := memStats{
		Total:     16000000 * 1024,
		Available: 9000000 * 1024,
		Used:      7000000 * 1024,
	}

	if err := appendSample(dir, stats, now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "usage.csv"))
	if err != nil {
		t.Fatal(err)
	}
	want := "timestamp,total_kb,used_kb,available_kb,available_pct\n" +
		"2026-11-09T12:34:56+0000,16000000,7000000,9000000,56.2\n"
	if string(data) != want {
		t.Errorf("usage.csv mismatch\n got: %q\nwant: %q", data, want)
	}
}

func TestPick(t *testing.T) {
	dir := t.TempDir()
	seen := make(map[int]bool)
	previous := -1

	for i := 0; i < 40; i++ {
		index, err := pick(dir, "body", 10)
		if err != nil {
			t.Fatal(err)
		}
		if index < 0 || index >= 10 {
			t.Fatalf("pick returned %d", index)
		}
		if index == previous {
			t.Fatalf("back-to-back repeat at pick %d", i)
		}
		previous = index
		if i < 10 {
			seen[index] = true
		}
	}

	if len(seen) != 10 {
		t.Errorf("first ten picks used %d distinct indices, want 10", len(seen))
	}
}

func TestMaybeSend(t *testing.T) {
	cfg := defaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.FromName = "Tester"

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	stats := memStats{Total: 16 << 30, Available: 9 << 30, Used: 7 << 30}
	sent := 0
	send := func(email) error {
		sent++
		return nil
	}

	ok, err := maybeSend(cfg, stats, now, send)
	if err != nil || !ok || sent != 1 {
		t.Fatalf("first send: ok=%v sent=%d err=%v", ok, sent, err)
	}
	if last, _ := readLastSent(cfg.StateDir); last != "2026-11-09" {
		t.Errorf("last_sent = %q, want 2026-11-09", last)
	}

	ok, err = maybeSend(cfg, stats, now, send)
	if err != nil || ok || sent != 1 {
		t.Fatalf("same-day send: ok=%v sent=%d err=%v", ok, sent, err)
	}

	ok, err = maybeSend(cfg, stats, now.Add(24*time.Hour), send)
	if err != nil || !ok || sent != 2 {
		t.Fatalf("next-day send: ok=%v sent=%d err=%v", ok, sent, err)
	}

	failing := func(email) error { return fmt.Errorf("smtp is down") }
	ok, err = maybeSend(cfg, stats, now.Add(48*time.Hour), failing)
	if err == nil || ok {
		t.Fatalf("failed send returned ok=%v err=%v", ok, err)
	}
	if last, _ := readLastSent(cfg.StateDir); last != "2026-11-10" {
		t.Errorf("last_sent changed after a failed send: %q", last)
	}

	low := memStats{Total: 16 << 30, Available: 1 << 30, Used: 15 << 30}
	ok, err = maybeSend(cfg, low, now.Add(72*time.Hour), func(email) error {
		t.Fatal("sent an email below the threshold")
		return nil
	})
	if err != nil || ok {
		t.Fatalf("low-memory send: ok=%v err=%v", ok, err)
	}
}

func TestConfig(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	path := filepath.Join(dir, "ram-altman.conf")
	configText := fmt.Sprintf(`to=config@example.com
from=From Name <from@example.com>
from_name="Configured Name"
threshold=25
state_dir=%s
transport=auto
smtp_host=smtp.example.com
smtp_port=465
smtp_tls=implicit
smtp_user=user@example.com
smtp_password_env=TEST_SMTP_PASSWORD
`, stateDir)
	if err := os.WriteFile(path, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TEST_SMTP_PASSWORD", "secret")
	t.Setenv("RAM_ALTMAN_TO", "env@example.com")

	cfg, err := loadConfig(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.To != "env@example.com" {
		t.Errorf("To = %q, want env@example.com", cfg.To)
	}
	if cfg.From != "from@example.com" {
		t.Errorf("From = %q, want from@example.com", cfg.From)
	}
	if cfg.FromName != "Configured Name" {
		t.Errorf("FromName = %q, want Configured Name", cfg.FromName)
	}
	if cfg.Threshold != 25 {
		t.Errorf("Threshold = %d, want 25", cfg.Threshold)
	}
	if cfg.StateDir != stateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, stateDir)
	}
	if cfg.SMTPHost != "smtp.example.com" || cfg.SMTPPort != 465 || cfg.SMTPTLS != "implicit" {
		t.Errorf("SMTP = %s:%d/%s", cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPTLS)
	}
	if cfg.SMTPPassword != "secret" {
		t.Errorf("SMTPPassword = %q, want secret", cfg.SMTPPassword)
	}

	t.Setenv("RAM_ALTMAN_SMTP_PASSWORD", "direct")
	cfg, err = loadConfig(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SMTPPassword != "direct" {
		t.Errorf("SMTPPassword = %q, want direct env override", cfg.SMTPPassword)
	}

	bad := filepath.Join(dir, "bad.conf")
	if err := os.WriteFile(bad, []byte("bogus=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(bad, true); err == nil {
		t.Error("unknown config key was accepted")
	}
}

func TestRenderMessage(t *testing.T) {
	cfg := config{
		From:     "from@example.com",
		FromName: "Test Name",
		To:       "to@example.com",
	}
	e := email{Subject: "Test subject", Body: "Line one\nLine two"}
	now := time.Date(2026, 11, 9, 12, 34, 56, 0, time.UTC)

	got := renderMessage(cfg, e, now)
	want := "From: \"Test Name\" <from@example.com>\r\n" +
		"To: to@example.com\r\n" +
		"Subject: Test subject\r\n" +
		"Date: Mon, 09 Nov 2026 12:34:56 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Auto-Submitted: auto-generated\r\n" +
		"\r\n" +
		"Line one\r\nLine two\r\n"
	if got != want {
		t.Errorf("message mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestSendLocalSendmail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is Unix-only")
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "outbox")
	script := filepath.Join(dir, "sendmail")
	body := "#!/bin/sh\ncat > \"$OUTFILE\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OUTFILE", out)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := defaultConfig()
	cfg.To = "to@example.com"
	cfg.From = "from@example.com"
	cfg.Transport = "sendmail"

	e := email{Subject: "Local test", Body: "Hello from sendmail"}
	msg := renderMessage(cfg, e, time.Unix(0, 0).UTC())
	if err := sendEmail(cfg, e, msg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Subject: Local test") {
		t.Errorf("sendmail did not receive the headers: %q", data)
	}
	if !strings.Contains(string(data), "Hello from sendmail") {
		t.Errorf("sendmail did not receive the body: %q", data)
	}
}

func TestSendSMTP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	got := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		reply := func(text string) error {
			if _, err := writer.WriteString(text); err != nil {
				return err
			}
			return writer.Flush()
		}

		if err := reply("220 test ESMTP\r\n"); err != nil {
			errCh <- err
			return
		}

		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")

			if inData {
				if line == "." {
					inData = false
					if err := reply("250 2.0.0 OK\r\n"); err != nil {
						errCh <- err
						return
					}
					continue
				}
				data.WriteString(line + "\n")
				continue
			}

			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				if err := reply("250-test\r\n250 8BITMIME\r\n"); err != nil {
					errCh <- err
					return
				}
			case strings.HasPrefix(upper, "HELO"):
				if err := reply("250 test\r\n"); err != nil {
					errCh <- err
					return
				}
			case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
				if err := reply("250 2.1.0 OK\r\n"); err != nil {
					errCh <- err
					return
				}
			case upper == "DATA":
				inData = true
				if err := reply("354 End data with <CR><LF>.<CR><LF>\r\n"); err != nil {
					errCh <- err
					return
				}
			case upper == "QUIT":
				_ = reply("221 2.0.0 Bye\r\n")
				got <- data.String()
				errCh <- nil
				return
			default:
				if err := reply("250 OK\r\n"); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	cfg := defaultConfig()
	cfg.SMTPHost = "127.0.0.1"
	cfg.SMTPPort = port
	cfg.SMTPTLS = "none"
	cfg.To = "to@example.com"
	cfg.From = "from@example.com"

	e := email{Subject: "Test subject", Body: "Hello from SMTP"}
	msg := renderMessage(cfg, e, time.Unix(0, 0).UTC())
	if err := sendSMTP(cfg, msg); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the fake SMTP server")
	}

	select {
	case body := <-got:
		if !strings.Contains(body, "Subject: Test subject") {
			t.Errorf("SMTP message has no subject: %q", body)
		}
		if !strings.Contains(body, "Hello from SMTP") {
			t.Errorf("SMTP message has no body: %q", body)
		}
	default:
		t.Fatal("fake SMTP server received no message")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"RAM_ALTMAN_TO",
		"RAM_ALTMAN_FROM",
		"RAM_ALTMAN_FROM_NAME",
		"RAM_ALTMAN_THRESHOLD",
		"RAM_ALTMAN_STATE_DIR",
		"RAM_ALTMAN_TRANSPORT",
		"RAM_ALTMAN_SMTP_HOST",
		"RAM_ALTMAN_SMTP_PORT",
		"RAM_ALTMAN_SMTP_TLS",
		"RAM_ALTMAN_SMTP_USER",
		"RAM_ALTMAN_SMTP_PASSWORD",
		"RAM_ALTMAN_SMTP_PASSWORD_ENV",
		"RAM_ALTMAN_CONFIG",
	} {
		t.Setenv(name, "")
	}
}

package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

const (
	maxLogBytes    = 10 * 1024 * 1024
	logLinesToKeep = 50000
	lockStaleAfter = 10 * time.Minute
)

const usageText = `ram-altman - track RAM usage and, at most once a day, tell Sam Altman
when this PC has RAM to spare.

Usage:
  ram-altman                 Log a sample and send the email if it is due
  ram-altman --status        Print current memory figures and exit
  ram-altman --dry-run       Print the email that would be sent and exit
  ram-altman --test-email    Send a test email now
  ram-altman --version       Print the version and exit
  ram-altman --help          Show this help

Options:
  --config PATH              Read settings from PATH

Settings can also come from RAM_ALTMAN_* environment variables. See README.md
for the full list.
`

type config struct {
	To              string
	From            string
	FromName        string
	Threshold       int
	StateDir        string
	Transport       string
	SMTPHost        string
	SMTPPort        int
	SMTPTLS         string
	SMTPUser        string
	SMTPPassword    string
	SMTPPasswordEnv string
}

func defaultConfig() config {
	return config{
		Threshold: 10,
		Transport: "auto",
		SMTPPort:  587,
		SMTPTLS:   "auto",
	}
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	fs := flag.NewFlagSet("ram-altman", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	status := fs.Bool("status", false, "print current memory figures and exit")
	dryRun := fs.Bool("dry-run", false, "print the email that would be sent and exit")
	testEmail := fs.Bool("test-email", false, "send a test email now")
	configFlag := fs.String("config", "", "path to config file")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "ram-altman: unexpected argument: %s\n", fs.Arg(0))
		return 1
	}
	if *showVersion {
		fmt.Printf("ram-altman %s\n", version)
		return 0
	}

	modes := 0
	for _, on := range []bool{*status, *dryRun, *testEmail} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		fmt.Fprintln(os.Stderr, "ram-altman: choose only one of --status, --dry-run or --test-email")
		return 1
	}

	if *status {
		stats, err := readMemory()
		if err != nil {
			return fail(err)
		}
		printStatus(stats)
		return 0
	}

	path, explicit := resolveConfigPath(*configFlag)
	cfg, err := loadConfig(path, explicit)
	if err != nil {
		return fail(err)
	}

	now := time.Now()

	if *dryRun {
		stats, err := readMemory()
		if err != nil {
			return fail(err)
		}
		printStatus(stats)
		if !stats.overThreshold(cfg.Threshold) {
			fmt.Printf("No email: available RAM is not above %d%%.\n", cfg.Threshold)
			return 0
		}
		if cfg.To == "" {
			cfg.To = "recipient-not-set@example.com"
		}
		if cfg.From == "" {
			cfg.From = "sender-not-set@example.com"
		}
		e := composeEmail(
			stats,
			randomIndex(len(subjectTemplates)),
			randomIndex(len(bodyTemplates)),
			randomIndex(len(signoffs)),
			cfg.FromName,
		)
		fmt.Println()
		fmt.Print(renderMessage(cfg, e, now))
		return 0
	}

	// cron and launchd often leave out the directories sendmail and msmtp live in.
	if runtime.GOOS != "windows" {
		extra := "/usr/sbin:/sbin"
		if runtime.GOOS == "darwin" {
			extra += ":/opt/homebrew/bin:/usr/local/bin"
		}
		os.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+extra)
	}

	if err := prepareSend(&cfg); err != nil {
		return fail(err)
	}

	if *testEmail {
		stats, err := readMemory()
		if err != nil {
			return fail(err)
		}
		e := composeEmail(
			stats,
			randomIndex(len(subjectTemplates)),
			randomIndex(len(bodyTemplates)),
			randomIndex(len(signoffs)),
			cfg.FromName,
		)
		if err := sendEmail(cfg, e, renderMessage(cfg, e, now)); err != nil {
			return fail(err)
		}
		fmt.Printf("Test email sent to %s.\n", cfg.To)
		return 0
	}

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return fail(err)
	}

	release, ok, err := acquireLock(cfg.StateDir)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return 0
	}
	defer release()

	stats, err := readMemory()
	if err != nil {
		return fail(err)
	}
	if err := appendSample(cfg.StateDir, stats, now); err != nil {
		fmt.Fprintf(os.Stderr, "ram-altman: warning: %v\n", err)
	}

	_, err = maybeSend(cfg, stats, now, func(e email) error {
		return sendEmail(cfg, e, renderMessage(cfg, e, now))
	})
	if err != nil {
		return fail(err)
	}
	return 0
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "ram-altman: %v\n", err)
	return 1
}

func randomIndex(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.IntN(n)
}

func resolveConfigPath(flagPath string) (string, bool) {
	if flagPath != "" {
		return flagPath, true
	}
	if p := os.Getenv("RAM_ALTMAN_CONFIG"); p != "" {
		return p, true
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, "ram-altman.conf"), false
}

func loadConfig(path string, explicit bool) (config, error) {
	cfg := defaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := applyConfigData(&cfg, path, data); err != nil {
				return cfg, err
			}
		case os.IsNotExist(err) && !explicit:
			// The default config file is optional.
		default:
			return cfg, err
		}
	}

	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	if cfg.SMTPPasswordEnv != "" {
		if v := os.Getenv(cfg.SMTPPasswordEnv); v != "" {
			cfg.SMTPPassword = v
		}
	}
	if v := os.Getenv("RAM_ALTMAN_SMTP_PASSWORD"); v != "" {
		cfg.SMTPPassword = v
	}

	cfg.StateDir = strings.TrimSpace(cfg.StateDir)
	if cfg.StateDir == "" {
		dir, err := defaultStateDir()
		if err != nil {
			return cfg, err
		}
		cfg.StateDir = dir
	}

	if err := validateConfig(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyConfigData(cfg *config, path string, data []byte) error {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})

	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected key=value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if err := applyConfigValue(cfg, key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func applyConfigValue(cfg *config, key, value string) error {
	switch key {
	case "to":
		cfg.To = value
	case "from":
		cfg.From = value
	case "from_name":
		cfg.FromName = value
	case "threshold":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid threshold %q", value)
		}
		cfg.Threshold = n
	case "state_dir":
		cfg.StateDir = value
	case "transport":
		cfg.Transport = value
	case "smtp_host":
		cfg.SMTPHost = value
	case "smtp_port":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid smtp_port %q", value)
		}
		cfg.SMTPPort = n
	case "smtp_tls":
		cfg.SMTPTLS = value
	case "smtp_user":
		cfg.SMTPUser = value
	case "smtp_password":
		cfg.SMTPPassword = value
	case "smtp_password_env":
		cfg.SMTPPasswordEnv = value
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

func applyEnv(cfg *config) error {
	setStringEnv("RAM_ALTMAN_TO", &cfg.To)
	setStringEnv("RAM_ALTMAN_FROM", &cfg.From)
	setStringEnv("RAM_ALTMAN_FROM_NAME", &cfg.FromName)
	setStringEnv("RAM_ALTMAN_STATE_DIR", &cfg.StateDir)
	setStringEnv("RAM_ALTMAN_TRANSPORT", &cfg.Transport)
	setStringEnv("RAM_ALTMAN_SMTP_HOST", &cfg.SMTPHost)
	setStringEnv("RAM_ALTMAN_SMTP_TLS", &cfg.SMTPTLS)
	setStringEnv("RAM_ALTMAN_SMTP_USER", &cfg.SMTPUser)
	setStringEnv("RAM_ALTMAN_SMTP_PASSWORD_ENV", &cfg.SMTPPasswordEnv)

	if v := os.Getenv("RAM_ALTMAN_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("RAM_ALTMAN_THRESHOLD: %w", err)
		}
		cfg.Threshold = n
	}
	if v := os.Getenv("RAM_ALTMAN_SMTP_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("RAM_ALTMAN_SMTP_PORT: %w", err)
		}
		cfg.SMTPPort = n
	}
	return nil
}

func setStringEnv(name string, dst *string) {
	if v := os.Getenv(name); v != "" {
		*dst = v
	}
}

func validateConfig(cfg *config) error {
	cfg.To = strings.TrimSpace(cfg.To)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.FromName = strings.TrimSpace(cfg.FromName)
	cfg.Transport = strings.ToLower(strings.TrimSpace(cfg.Transport))
	cfg.SMTPHost = strings.TrimSpace(cfg.SMTPHost)
	cfg.SMTPTLS = strings.ToLower(strings.TrimSpace(cfg.SMTPTLS))
	cfg.SMTPUser = strings.TrimSpace(cfg.SMTPUser)

	if cfg.Threshold < 0 || cfg.Threshold > 100 {
		return fmt.Errorf("threshold must be between 0 and 100")
	}
	if cfg.SMTPPort < 1 || cfg.SMTPPort > 65535 {
		return fmt.Errorf("smtp_port must be between 1 and 65535")
	}
	switch cfg.Transport {
	case "auto", "smtp", "sendmail", "msmtp", "mail":
	default:
		return fmt.Errorf("unknown transport %q", cfg.Transport)
	}
	switch cfg.SMTPTLS {
	case "auto", "starttls", "implicit", "none":
	default:
		return fmt.Errorf("unknown smtp_tls mode %q", cfg.SMTPTLS)
	}
	if strings.ContainsAny(cfg.FromName, "\r\n") {
		return fmt.Errorf("from_name must not contain newlines")
	}
	if strings.ContainsAny(cfg.SMTPHost+cfg.SMTPUser, "\r\n") {
		return fmt.Errorf("smtp_host and smtp_user must not contain newlines")
	}

	if cfg.To != "" {
		addr, err := mail.ParseAddress(cfg.To)
		if err != nil {
			return fmt.Errorf("invalid to address: %w", err)
		}
		cfg.To = addr.Address
	}
	if cfg.From != "" {
		addr, err := mail.ParseAddress(cfg.From)
		if err != nil {
			return fmt.Errorf("invalid from address: %w", err)
		}
		cfg.From = addr.Address
		if cfg.FromName == "" && addr.Name != "" {
			cfg.FromName = addr.Name
		}
	}
	return nil
}

func defaultStateDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "ram-altman"), nil
		}
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "ram-altman"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "ram-altman"), nil
	default:
		if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
			return filepath.Join(dir, "ram-altman"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "ram-altman"), nil
	}
}

func prepareSend(cfg *config) error {
	if cfg.To == "" {
		return fmt.Errorf("to is not set (RAM_ALTMAN_TO or to= in the config file)")
	}
	if cfg.From == "" {
		return fmt.Errorf("from is not set (RAM_ALTMAN_FROM or from= in the config file)")
	}

	transport, err := resolveTransport(*cfg)
	if err != nil {
		return err
	}
	cfg.Transport = transport

	if transport == "smtp" && cfg.SMTPUser != "" && cfg.SMTPPassword == "" {
		return fmt.Errorf("smtp_user is set but no SMTP password is configured (RAM_ALTMAN_SMTP_PASSWORD, smtp_password_env, or smtp_password)")
	}
	return nil
}

func resolveTransport(cfg config) (string, error) {
	transport := cfg.Transport
	if transport == "" {
		transport = "auto"
	}
	if transport == "auto" {
		if cfg.SMTPHost != "" {
			return "smtp", nil
		}
		transport = autoLocalTransport()
		if transport == "" {
			return "", noTransportError()
		}
		return transport, nil
	}

	switch transport {
	case "smtp":
		if cfg.SMTPHost == "" {
			return "", fmt.Errorf("transport=smtp but smtp_host is empty")
		}
	case "sendmail", "msmtp", "mail":
		if _, err := exec.LookPath(transport); err != nil {
			return "", fmt.Errorf("transport=%s but %s was not found in PATH", transport, transport)
		}
	default:
		return "", fmt.Errorf("unknown transport %q", transport)
	}
	return transport, nil
}

func autoLocalTransport() string {
	switch runtime.GOOS {
	case "windows":
		return ""
	case "darwin":
		if _, err := exec.LookPath("msmtp"); err == nil {
			return "msmtp"
		}
		return ""
	default:
		for _, name := range []string{"sendmail", "msmtp", "mail"} {
			if _, err := exec.LookPath(name); err == nil {
				return name
			}
		}
		return ""
	}
}

func noTransportError() error {
	switch runtime.GOOS {
	case "windows":
		return fmt.Errorf("no SMTP host configured; Windows has no built-in sendmail/msmtp/mail, so set smtp_host")
	case "darwin":
		return fmt.Errorf("no SMTP host configured and msmtp was not found; macOS sendmail/mail are not used automatically, so set smtp_host or install msmtp")
	default:
		return fmt.Errorf("no SMTP host configured and no sendmail/msmtp/mail command was found; set smtp_host or install one of them")
	}
}

func acquireLock(stateDir string) (func(), bool, error) {
	path := filepath.Join(stateDir, "lock")

	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, werr := fmt.Fprintf(f, "%d\n", os.Getpid()); werr != nil {
				f.Close()
				os.Remove(path)
				return nil, false, werr
			}
			if cerr := f.Close(); cerr != nil {
				os.Remove(path)
				return nil, false, cerr
			}
			return func() { _ = os.Remove(path) }, true, nil
		}
		if !os.IsExist(err) {
			return nil, false, err
		}

		fi, statErr := os.Stat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, false, statErr
		}
		if time.Since(fi.ModTime()) < lockStaleAfter {
			return nil, false, nil
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return nil, false, rmErr
		}
	}
	return nil, false, nil
}

func appendSample(stateDir string, s memStats, now time.Time) error {
	path := filepath.Join(stateDir, "usage.csv")

	needHeader := false
	if fi, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		needHeader = true
	} else if fi.Size() == 0 {
		needHeader = true
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if needHeader {
		if _, err := fmt.Fprintln(f, "timestamp,total_kb,used_kb,available_kb,available_pct"); err != nil {
			f.Close()
			return err
		}
	}
	if _, err := fmt.Fprintf(f, "%s,%d,%d,%d,%s\n",
		now.Format("2006-01-02T15:04:05-0700"),
		s.Total/1024,
		s.Used/1024,
		s.Available/1024,
		s.AvailablePct(),
	); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return rotateLog(path)
}

func rotateLog(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() <= maxLogBytes {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= logLinesToKeep+1 {
		return nil
	}

	keep := make([]string, 0, logLinesToKeep+1)
	keep = append(keep, lines[0])
	keep = append(keep, lines[len(lines)-logLinesToKeep:]...)
	out := strings.Join(keep, "\n") + "\n"

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows can refuse to replace an existing file.
		os.Remove(path)
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}
	return nil
}

func readLastSent(stateDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "last_sent"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func markSent(stateDir, day string) error {
	return os.WriteFile(filepath.Join(stateDir, "last_sent"), []byte(day+"\n"), 0o600)
}

func maybeSend(cfg config, s memStats, now time.Time, send func(email) error) (bool, error) {
	if !s.overThreshold(cfg.Threshold) {
		return false, nil
	}

	today := now.Format("2006-01-02")
	last, err := readLastSent(cfg.StateDir)
	if err != nil {
		return false, err
	}
	if last == today {
		return false, nil
	}

	subjectIdx, err := pick(cfg.StateDir, "subject", len(subjectTemplates))
	if err != nil {
		return false, err
	}
	bodyIdx, err := pick(cfg.StateDir, "body", len(bodyTemplates))
	if err != nil {
		return false, err
	}
	signoffIdx, err := pick(cfg.StateDir, "signoff", len(signoffs))
	if err != nil {
		return false, err
	}

	e := composeEmail(s, subjectIdx, bodyIdx, signoffIdx, cfg.FromName)
	if err := send(e); err != nil {
		return false, err
	}
	if err := markSent(cfg.StateDir, today); err != nil {
		return true, err
	}
	return true, nil
}

func pick(stateDir, name string, count int) (int, error) {
	if count <= 0 {
		return 0, nil
	}

	bagPath := filepath.Join(stateDir, "bag_"+name)
	lastPath := bagPath + ".last"

	indices, err := readBag(bagPath)
	if err != nil {
		return 0, err
	}
	if len(indices) == 0 {
		indices = rand.Perm(count)
		last, err := readLastPick(lastPath)
		if err != nil {
			return 0, err
		}
		// Stop the last pick of the old round opening the new one.
		if len(indices) > 1 && indices[0] == last {
			indices = append(indices[1:], indices[0])
		}
		if err := writeBag(bagPath, indices); err != nil {
			return 0, err
		}
	}

	next := indices[0]
	if next < 0 || next >= count {
		next = rand.IntN(count)
	}
	if err := writeBag(bagPath, indices[1:]); err != nil {
		return 0, err
	}
	if err := writeLastPick(lastPath, next); err != nil {
		return 0, err
	}
	return next, nil
}

func readBag(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return nil, nil
	}
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return nil, nil
		}
		out = append(out, n)
	}
	return out, nil
}

func writeBag(path string, indices []int) error {
	lines := make([]string, len(indices))
	for i, n := range indices {
		lines[i] = strconv.Itoa(n)
	}
	data := ""
	if len(lines) > 0 {
		data = strings.Join(lines, "\n") + "\n"
	}
	return os.WriteFile(path, []byte(data), 0o600)
}

func readLastPick(path string) (int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return -1, nil
	}
	if err != nil {
		return -1, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return -1, nil
	}
	return n, nil
}

func writeLastPick(path string, index int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(index)+"\n"), 0o600)
}

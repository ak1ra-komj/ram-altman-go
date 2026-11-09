# Ram-Altman

**A small cross-platform program that writes to Sam Altman once a day to report the RAM he missed.**

This is the Go fork maintained at **[ak1ra-komj/ram-altman-go](https://github.com/ak1ra-komj/ram-altman-go)**, based on the original [IainKay/ram-altman](https://github.com/IainKay/ram-altman).

Read the sample emails at **[ram-altman.lol](https://ram-altman.lol)**, no install needed.

[![Website: ram-altman.lol](https://img.shields.io/badge/website-ram--altman.lol-0f5a43)](https://ram-altman.lol)
![Global DRAM allocation: quite terrible](https://img.shields.io/badge/global%20DRAM%20allocation-quite%20terrible-red)
![Utilization: maybe 5%](https://img.shields.io/badge/utilization-maybe%205%25-orange)
![Emails: 1 per day](https://img.shields.io/badge/emails-1%20per%20day-blue)
![Runs on Linux, macOS and Windows](https://img.shields.io/badge/runs%20on-Linux%20%7C%20macOS%20%7C%20Windows-informational)
![Written in Go, still not Electron](https://img.shields.io/badge/written%20in-Go%2C%20still%20not%20Electron-brightgreen)

There is RAM in your computer. Right now. Just sitting there, in a *personal* computer, being personal.

You may have thought of this as "a computer working normally". On a recent episode of [Cheeky Pint](https://gizmodo.com/sam-altman-youre-completely-right-about-ram-im-sorry-for-the-confusion-heres-what-happened-2000835208), the CEO of OpenAI offered another view:

> "From a global allocation perspective, actually, it's quite terrible to have everybody have a bunch of the world's limited supply of DRAM sitting in their own computer"
>
> — Sam Altman

That RAM, he added, is ["being used maybe 5% of the time"](https://www.xda-developers.com/sam-altman-says-its-quite-terrible-that-people-have-ram-in-their-pcs-during-a-shortage/).

Which raised an obvious question for a certain kind of person: *surely someone should be monitoring this?*

So we wrote a program. If your machine has more than 10% of its RAM available, it sends one email a day to let Sam know. It seemed only fair. He can't fix the problem if nobody tells him where the RAM is.

## Features

- Tracks total, used and available RAM on Linux, macOS and Windows
- Handles Linux page cache correctly, which already puts it ahead of most arguments about RAM
- Sends at most one email per day, because we're hoarders, not animals
- Rotates 8 subject lines, 10 messages and 6 sign-offs, so the joke gets old at a carefully managed pace
- Logs every sample to CSV for when the auditors arrive
- Ships with tests, more than the DRAM market has had sensible outcomes
- Can send through SMTP directly, or through a local `sendmail` / `msmtp` / `mail` command where available
- Is written in Go, as an Electron version would have solved the problem by using all the RAM

## A sample of today's correspondence

```text
Subject: You missed a spot

Hi Sam,

Please don't be alarmed, but there is 8.6 GiB of RAM on my desk that isn't in
a data centre. That's 56.2% of what's installed here, and as far as I can tell
it has no plans today.

Hoarding responsibly,
A concerned RAM owner

--
Sent automatically, at most once a day, by a small script on one PC.
Reply if you'd like it to stop.
```

The figures are real. They come from your machine. That's the worrying part.

## What counts as "used"?

```text
used = total - available
```

- **Linux**: `available` is `MemAvailable`, the kernel's own estimate of how much memory programs could claim without swapping. On kernels older than 3.14, it is estimated as free memory plus reclaimable cache.
- **macOS**: there is no exact `MemAvailable` equivalent. The program uses free pages plus inactive pages, which is deliberately conservative.
- **Windows**: `available` is `GlobalMemoryStatusEx().ullAvailPhys`, the physical memory Windows says can be reused immediately.

On Linux, free RAM counts as available. So does cache the kernel can reclaim. tmpfs and shared memory don't, because they can't simply be handed back.

Every sysadmin has spent years explaining that [Linux didn't eat your RAM](https://www.linuxatemyram.com/) and that free RAM is wasted RAM. It took decades for someone important to agree with the second part, and he'd like it shipped to him.

Strictly speaking, the page cache is doing useful work, so counting it as available is a generous concession to the other side. Think of it as a plea bargain.

## Installation

You'll need Go 1.25 or newer if you build from source. The supported targets are 64-bit Linux, macOS and Windows (amd64/arm64). Your RAM is assumed.

```bash
git clone https://github.com/ak1ra-komj/ram-altman-go.git
cd ram-altman-go
go build ./cmd/ram-altman

./ram-altman --status    # see the evidence against you
./ram-altman --dry-run   # preview an email without sending it
```

On Windows, the same command produces `ram-altman.exe`:

```powershell
go build ./cmd/ram-altman
```

You can also install it with:

```bash
go install github.com/ak1ra-komj/ram-altman-go/cmd/ram-altman@latest
```

Cross-compiling is just as boring:

```bash
GOOS=windows GOARCH=amd64 go build -o ram-altman.exe ./cmd/ram-altman
GOOS=darwin  GOARCH=arm64 go build -o ram-altman-macos ./cmd/ram-altman
```

Example `--status` output, from a simulated machine that has confessed to everything:

```text
Total:      15.3 GiB
Used:       6.7 GiB
Available:  8.6 GiB (56.2%)
  Free:     0.3 GiB
  Cache:    8.2 GiB
```

## Configuration

The program reads a flat `key=value` file. The default location is:

| Platform | Config file |
| --- | --- |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/ram-altman.conf` |
| macOS | `~/Library/Application Support/ram-altman.conf` |
| Windows | `%AppData%\ram-altman.conf` |

Use `--config PATH` or `RAM_ALTMAN_CONFIG` to point somewhere else. Environment variables override the config file.

```ini
to=sam@example.com
from=you@example.com
from_name=Your Name
threshold=10

# SMTP submission. Leave smtp_host empty to use a local MTA.
smtp_host=smtp.example.com
smtp_port=587
smtp_tls=starttls
smtp_user=you@example.com
smtp_password_env=RAM_ALTMAN_SMTP_PASSWORD
```

Put the password in the named environment variable:

```bash
export RAM_ALTMAN_SMTP_PASSWORD='your app password'
```

You can also use `RAM_ALTMAN_SMTP_PASSWORD` directly, or `smtp_password=` in the config file. The config file is per-user, so protect it if it contains a password.

`--test-email` sends one test message and exits. It does not write the daily state, so it cannot make the real email skip a day.

```bash
./ram-altman --test-email
```

### SMTP

`net/smtp` is enough for the common providers:

- `smtp_tls=starttls` for port 587
- `smtp_tls=implicit` for port 465
- `smtp_tls=none` only for a trusted local relay; credentials over plaintext are refused unless the host is localhost
- `smtp_tls=auto` picks implicit for port 465 and STARTTLS otherwise

Authentication supports PLAIN and LOGIN. Gmail, iCloud and Fastmail work with an app password. Providers that require OAuth2 for SMTP, such as some Microsoft 365 tenants, are not supported by the built-in client; use `msmtp` with an existing OAuth2 setup and set `transport=msmtp`.

### Local mail commands

If `smtp_host` is empty, the program falls back to a local mail command:

| Platform | Auto-detected |
| --- | --- |
| Linux | `sendmail`, then `msmtp`, then `mail` |
| macOS | `msmtp` only |
| Windows | none; configure SMTP |

macOS ships with `sendmail` and `mail`, but they route through Postfix, which is not enabled as a service by default. Messages can sit in a queue forever, so the program does not select them automatically. Use SMTP, install `msmtp`, or set `transport=sendmail` explicitly if you know your Postfix setup works.

### Settings

| Config key | Environment variable | Default |
| --- | --- | --- |
| `to` | `RAM_ALTMAN_TO` | Required |
| `from` | `RAM_ALTMAN_FROM` | Required |
| `from_name` | `RAM_ALTMAN_FROM_NAME` | `A concerned RAM owner` |
| `threshold` | `RAM_ALTMAN_THRESHOLD` | `10` |
| `state_dir` | `RAM_ALTMAN_STATE_DIR` | Platform state directory |
| `transport` | `RAM_ALTMAN_TRANSPORT` | `auto` |
| `smtp_host` | `RAM_ALTMAN_SMTP_HOST` | Empty (use local MTA) |
| `smtp_port` | `RAM_ALTMAN_SMTP_PORT` | `587` |
| `smtp_tls` | `RAM_ALTMAN_SMTP_TLS` | `auto` |
| `smtp_user` | `RAM_ALTMAN_SMTP_USER` | Empty (no auth) |
| `smtp_password` | `RAM_ALTMAN_SMTP_PASSWORD` | Empty |
| `smtp_password_env` | `RAM_ALTMAN_SMTP_PASSWORD_ENV` | Empty |

`transport` can be `auto`, `smtp`, `sendmail`, `msmtp` or `mail`.

On Linux, `RAM_ALTMAN_MEMINFO` can point at a different `meminfo` file. It is mainly there for tests.

## Scheduling

The program samples on every run and sends at most one email per day. Run it every five minutes.

### Linux

```cron
RAM_ALTMAN_TO=recipient@example.com
RAM_ALTMAN_FROM=you@example.com
RAM_ALTMAN_FROM_NAME=Your Name

*/5 * * * * /path/to/ram-altman
```

If you use the config file, the crontab only needs the command. If a send fails, it tries again on the next run, because this message matters.

### macOS

Save this as `~/Library/LaunchAgents/lol.ram-altman.plist`, replacing the paths:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>lol.ram-altman</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/ram-altman</string>
    </array>
    <key>StartInterval</key>
    <integer>300</integer>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
```

Load it with:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/lol.ram-altman.plist
```

### Windows

Run this in PowerShell, adjusting the path:

```powershell
$action = New-ScheduledTaskAction -Execute 'C:\Tools\ram-altman.exe'
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date) `
    -RepetitionInterval (New-TimeSpan -Minutes 5) `
    -RepetitionDuration (New-TimeSpan -Days 3650)
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -StartWhenAvailable
Register-ScheduledTask -TaskName 'Ram Altman' -Action $action -Trigger $trigger -Settings $settings
```

Run the task as yourself so it can read your config file. If the machine was asleep, the next run catches up; the daily marker stops it sending twice.

### State and logs

| Platform | Default state directory |
| --- | --- |
| Linux | `${XDG_STATE_HOME:-~/.local/state}/ram-altman` |
| macOS | `~/Library/Application Support/ram-altman` |
| Windows | `%LocalAppData%\ram-altman` |

The directory contains `usage.csv`, `last_sent` and the rotation bags, plus a lock file while the program is running. The CSV columns are unchanged from the Bash version.

## Audit yourself

Let it run for a while, then find out how your machine compares:

```bash
awk -F, 'NR>1 { used += $3; total += $2 }
  END { if (total) printf "Your RAM in use: %.1f%% (the claim: maybe 5%%)\n", used * 100 / total }' \
  ~/.local/state/ram-altman/usage.csv
```

On macOS, point the command at the state directory from the table above. On Windows, open the CSV in a spreadsheet or run the command in WSL.

Yes, share of memory in use isn't the same thing as share of time spent in use. If you came here to say that, welcome to Hacker News. We're glad you're here.

## Migrating from the Bash version

The environment variables, CSV columns, `last_sent` and rotation bags are unchanged. On Linux, the default state directory is also unchanged, so existing history keeps working.

The old `ram-altman.sh` is still in git history. Update your scheduler to run the new binary instead:

```cron
*/5 * * * * /path/to/ram-altman
```

## Please be a good neighbour

One email a day from one person is a joke. Ten thousand of them is a denial-of-service attack with better material.

- **`--dry-run` is the best way to enjoy this.** It costs nothing and all the jokes are there.
- **This repo doesn't ship with anyone's email address,** and please don't add one.
- **Use your own real address.** No spoofing, and no running it across your server fleet.
- **If anyone asks you to stop, stop.** The footer of every email promises this, so keep the promise.

You can also point it at someone who will enjoy it, like the friend who has been waiting for DDR5 prices to come down.

## FAQ

**Does this help with the DRAM shortage?**
No.

**Is this a serious tool?**
The memory accounting is. The correspondence isn't.

**Does it work on macOS?**
Yes. There is no exact `MemAvailable` equivalent, so it counts free plus inactive pages. Your RAM may still be soldered down, which makes you the hardest target in the global allocation programme.

**Does it work on Windows?**
Yes. Use SMTP; Windows has no `sendmail` to fall back on.

**Why is the threshold 10%?**
Below that, your machine has suffered enough.

**Does running this use RAM?**
Briefly. It's then returned to the global pool, which is more than can be said for the rest of us.

**Can I run the tests without emailing anyone?**
Yes:

```bash
go test ./...
```

The tests use simulated memory and a fake SMTP server. No inboxes are harmed.

**Can I see it without installing anything?**
Yes. [ram-altman.lol](https://ram-altman.lol) cycles through the real emails, with sample figures.

**Why Go?**
Because unlike an Electron app, it only uses RAM while it's running.

**What should I do with all this RAM?**
Open another browser tab. You've still got time.

## In fairness to Sam

Altman has since [said the remarks were taken out of context](https://gizmodo.com/sam-altman-youre-completely-right-about-ram-im-sorry-for-the-confusion-heres-what-happened-2000835208), writing:

> "i actually do want the world to have tons of memory and people to put as much as they want in their computers"

He [followed up](https://www.gadgetreview.com/sam-altman-says-he-wants-you-to-have-even-more-ram-not-less-changes-name-to-ram-altman) with "i want you to have so much ram" and "lets build more", and briefly changed his display name on X to Ram Altman, which is objectively excellent and is where this project gets its name.

The original remarks [came up in a discussion of running AI models locally](https://www.engadget.com/2303266/sam-altman-says-its-terrible-dram-computer/), and the utilization argument is a real one: servers really are busier than your desktop. We just think it's funnier to reply with cron.

Besides, if he wants you to have so much RAM, then this program isn't a complaint at all. It's a daily progress report.

## License

Free as in unallocated.

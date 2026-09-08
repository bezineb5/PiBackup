# PiBackup Go

A simple, lightweight photo backup tool for Raspberry Pi written in Go.

It is designed for a compact travel backup box: plug in a camera SD card and
the contents are copied to the Pi's backup disk. The top priority is **never
corrupting the source card** — it is mounted read-only and never written to.

## Features

- **Source-safety first**: source cards are mounted read-only (`ro`) with
  `noatime`; the application never writes to the card (not even for naming).
- **Single binary**: no Python/venv/pip, just copy and run.
- **Event-driven detection**: kernel uevents via netlink (no polling), with an
  fsnotify fallback; a short retry loop waits for freshly-appeared devices to
  settle instead of a fixed sleep.
- **Fast**: efficient file copying with rsync.
- **Robust logging**: structured `slog` JSON with automatic rotation.
- **Stable backup names**: a Pi-side registry maps each device (by its blkid
  UUID/serial) to a consistent name across insertions, without touching the card.
- **Permission preservation**: rsync normalises on-disk permissions of the copies.
- **Data safety**: disk buffers are flushed after each backup; all long-running
  operations honour a context, so shutdown interrupts an in-flight backup.
- **Parallel operation prevention**: a mutex prevents multiple backups at once;
  a manual-scan debounce collapses repeated button presses.
- **Clean user feedback**: abstract event-based feedback system.
- **WebDAV server**: remote read-only access to backup content (iOS Files app).

## What it does

1. **Detects** USB / removable storage via kernel uevents (primary) or fsnotify
   on `/dev` (fallback).
2. **Waits for the device to settle** by polling its sysfs attributes (USB
   subsystem link, removable flag, medium size) with backoff — no fixed sleep.
3. **Mounts read-only** (`ro,noatime`) — the card is never written to.
4. **Runs rsync** to copy the card's contents to `/share/<name>`.
5. **Flushes** the destination disk (`sync`) and unmounts the card.
6. **Processes** any devices already connected at startup.
7. **Logs everything** with automatic rotation.

## Build

```bash
make build
```

This creates a `pibackup-go` binary for ARM (Raspberry Pi, ARMv6).

To build for a 64-bit Pi:

```bash
GOOS=linux GOARCH=arm64 go build -o pibackup-go .
```

## Install

1. **Copy binary to Pi:**
   ```bash
   scp pibackup-go pi@your-pi:/home/pi/
   ```

2. **Make executable:**
   ```bash
   chmod +x pibackup-go
   ```

3. **Install service:**
   ```bash
   sudo cp pibackup-go.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable pibackup-go.service
   sudo systemctl start pibackup-go.service
   ```

## Usage

### Basic Usage

Just plug in a memory card or USB drive. The service will:

- Detect the device (no polling delay)
- Wait for it to settle, then mount it **read-only**
- Copy all files to `/share/<backup-name>`
- Unmount when done
- Log all activities with timestamps

### Command-Line Options

The application supports command-line flags for configuration overrides:

```bash
# Show help
./pibackup-go --help

# Use custom config file
./pibackup-go --config /path/to/config.yaml

# Override specific settings
./pibackup-go --backup-path /custom/backup/path --log-level debug

# Combine config file with overrides
./pibackup-go --config config.yaml --webdav-port 8080 --feedback console
```

#### Available Flags

- `-c, --config string` - Config file path (default: config.yaml)
- `-b, --backup-path string` - Backup directory path
- `-l, --log-level string` - Logging level (debug, info, warn, error)
- `-p, --webdav-port string` - WebDAV server port
- `-f, --feedback string` - Feedback type (touchphat, console, log, none)

#### Configuration Priority

1. **Command-line flags** (highest priority)
2. **Environment variables** (PIBACKUP_*)
3. **Config file** (config.yaml)
4. **Default values** (lowest priority)

### Skipping a backup

To skip backup of a specific device, create a `.backupignore` file at the root
of the device:

```bash
# On the device root directory
touch .backupignore
```

The application will detect this file and skip the backup, logging the reason.
This is a read of the source only; the card is never written.

### Backup naming

Names are resolved **without writing to the card**, in this order:

1. If a `unique.id` file already exists on the card, its value is used
   (read-only; kept for backward compatibility with cards stamped by older
   builds).
2. Otherwise the card's stable identifier (its blkid UUID or serial) is looked
   up in a **Pi-side registry** stored at `<backup-path>/.device-names.json`.
   On first sight, a fresh 6-character ID is minted and stored there.
3. Fallback: a timestamp, `backup_20240729_143022` (used when no stable
   identifier can be determined — e.g. a device with no blkid UUID/serial).

The registry lives on the destination disk, never on the source card.

## Logging

The application uses structured logging with automatic rotation:

### Log Location
- **Log files**: `/share/logs/pibackup.log`
- **Rotation**: 10MB per file, 20 backup files, 120 days retention
- **Compression**: Old log files are automatically compressed
- **Format**: JSON to the log file; human-readable text to the console

### Log Format (file)
```json
{"time":"2024-07-29T14:30:22.123Z","level":"INFO","msg":"application started","version":"1.0.0","component":"app"}
{"time":"2024-07-29T14:30:22.124Z","level":"INFO","msg":"uevent monitor started","component":"uevent"}
{"time":"2024-07-29T14:30:25.456Z","level":"INFO","msg":"uevent: device added","device":"/dev/sda","component":"app"}
{"time":"2024-07-29T14:30:27.789Z","level":"INFO","msg":"device mounted successfully","device":"sda","mount_point":"/media/sda1","read_only":true,"component":"mount"}
{"time":"2024-07-29T14:30:45.456Z","level":"INFO","msg":"backup completed successfully","device":"/dev/sda1","destination":"/share/ABC123","duration_seconds":18.5,"component":"backup"}
```

Each component (`app`, `mount`, `backup`, `device`, `uevent`) tags its own log
lines with a structured `component` field instead of a raw source file path.
Package-level callers (WebDAV, touchphat) use the base logger.

### Key Events
- `application started` - Application startup with version
- `uevent: device added` - Kernel uevent for a new block device
- `device did not become ready in time` - Readiness polling exhausted
- `device mounted successfully` - Device mounted (read-only, noatime)
- `backup completed successfully` - Backup completed with duration
- `registered new device name` - A device was seen for the first time and named in the Pi-side registry
- `shutting down` - Application shutdown
- `backup already in progress` - Parallel operation prevented
- `scan already in progress` - A manual backup press was collapsed into an in-flight scan

### Benefits of slog
- **Structured JSON** - Easy to parse and analyze
- **Source location** - Shows exact file and line number
- **Level-based filtering** - INFO, WARN, ERROR levels
- **Key-value pairs** - Structured data for each event
- **Performance** - Optimized for high-throughput logging

## User Feedback

The application uses an abstract feedback system based on events. A feedback
sink is anything that implements the `Feedback` interface; the core logic never
touches hardware directly.

### Core Concept

```go
type Feedback interface {
    Notify(event Event)
    Halt() error
}

type Event struct {
    Type      EventType
    Message   string
    Device    string
    Progress  float64
    Error     error
    Timestamp time.Time
}
```

### Event Types

- **EventStatus** - General status updates
- **EventProgress** - Backup progress (0-100%)
- **EventSuccess** - Successful operations
- **EventError** - Error conditions
- **EventWarning** - Warning messages

### Built-in Implementations

- **`touchphat`** (default) - Pimoroni Touch pHAT / CAP1166 capacitive touch
  sensor via periph.io. Falls back to no feedback if the hardware is absent
  (e.g. running on a Pi without the HAT). Also exposes touch events that can
  trigger a manual backup or a graceful shutdown (see Touch input).
- **`console`** - Human-readable lines to stdout.
- **`log`** - Structured events into the `slog` logger.
- **`none`** - No feedback.

### Touch input

When the `touchphat` feedback is active, two touch buttons are wired:

- **Manual backup** triggers a scan of all connected devices. Repeated presses
  while a scan is running are debounced (collapsed into the in-flight scan).
- **Shutdown** cancels the application context (interrupting any in-flight
  backup — rsync, mount, and sync all abort), then runs `shutdown now`. This
  makes the power button responsive even during a backup.

## Configuration

The application uses Viper for flexible configuration management with multiple
sources.

### Configuration Sources (in order of priority)

1. **Command-line flags** (highest priority)
2. **Environment variables** (PIBACKUP_*)
3. **Config file** (config.yaml)
4. **Default values** (lowest priority)

### Config File

Create a `config.yaml` file in one of these locations:
- `/etc/pibackup/config.yaml` (system-wide)
- `$HOME/.pibackup/config.yaml` (user-specific)
- `./config.yaml` (current directory)

Example configuration with defaults shown:

```yaml
backup:
  path: "/share"
  usb_mount_path: "/media"
  skip_patterns: [".backupignore"]

logging:
  level: "info"
  path: "/share/logs"
  max_size: 10        # MB per file
  max_backups: 20
  max_age: 120        # days
  compress: true

webdav:
  enabled: true
  port: "80"
  username: ""
  password: ""

mount:
  readonly: true      # mount source cards read-only (default; do not disable)

feedback:
  type: "touchphat"
  led_brightness: 50
```

### Environment Variables

All configuration values can be overridden with environment variables:

```bash
# Feedback
export PIBACKUP_FEEDBACK="touchphat"   # touchphat | console | log | none

# Mount
export PIBACKUP_MOUNT_READONLY="true"  # keep source cards read-only

# WebDAV
export PIBACKUP_WEBDAV_ENABLED="true"
export PIBACKUP_WEBDAV_PORT="80"

# Backup
export PIBACKUP_BACKUP_PATH="/share"
```

### Default Behavior

- **`mount.readonly` defaults to `true`** — source cards are mounted read-only.
  This is the core source-safety guarantee. Only disable it if you have a
  specific reason to write to a source.
- **`feedback.type` defaults to `touchphat`** — if the hardware is missing,
  the app falls back to no feedback and continues running.
- **`webdav.enabled` defaults to `true`** — see the WebDAV section for
  security considerations on untrusted networks.

## WebDAV Remote Access

The application includes a built-in WebDAV server for remote access to backup
content from iOS devices.

> **Security note**: the default is unauthenticated, read-only access on port
> 80, exposed to whatever network the Pi is joined to. On hotel/cafe Wi-Fi this
> means anyone on the LAN can browse your backups. Set `webdav.username` /
> `webdav.password`, bind to a trusted interface, or set `webdav.enabled:
> false` when on untrusted networks. (Hardening the default is tracked as
> future work.)

### Features

- **Read-only access** - No write operations allowed
- **iOS Files app integration** - Native support without additional apps
- **Path traversal protection** - Source paths are validated
- **CORS support** - Works with web browsers

### Configuration

```bash
# Enable WebDAV server (default: true)
export PIBACKUP_WEBDAV_ENABLE="true"

# WebDAV server port (default: 80)
export PIBACKUP_WEBDAV_PORT="80"

# Backup path (default: /share)
export PIBACKUP_BACKUP_PATH="/share"
```

### iOS Setup

1. **Open Files app** on your iPhone/iPad
2. **Tap "Browse"** → **"..."** → **"Connect to Server"**
3. **Enter server address**: `http://192.168.1.100`
4. **Tap "Connect"** - no username/password required by default
5. **Browse backups** - each backup appears as a folder

### API Endpoints

- **`/`** - WebDAV root (lists all backups)
- **`/status`** - Server status and backup count
- **`/backups`** - JSON list of available backups

### Example Usage

```bash
# Check server status
curl http://192.168.1.100/status

# List available backups
curl http://192.168.1.100/backups

# Access specific backup (via WebDAV)
# http://192.168.1.100/ABC123/
```

## How it works

- **Detection**: kernel uevents via a netlink socket (primary), falling back to
  fsnotify on `/dev` if the netlink monitor can't start.
- **Settling**: a short retry loop polls the device's sysfs attributes (USB
  subsystem link, removable flag, medium size) with exponential backoff, rather
  than a fixed sleep.
- **Mounting**: source devices are mounted `ro,noatime` via the mount service —
  the single place that owns `mount`/`umount`/`sync`.
- **Copy**: rsync copies the card's contents into the backup directory; the
  directory is stable per device, so a re-insertion resumes and completes an
  interrupted backup in place.
- **Concurrency**: a mutex prevents multiple backups running at once; a
  separate mutex debounces manual scans.
- **Cancellation**: all long-running operations (rsync, mount, umount, sync,
  blkid) honour the application context. SIGINT/SIGTERM and the shutdown touch
  button cancel it, so an in-flight backup is interrupted promptly.
- **Graceful shutdown**: SIGINT/SIGTERM stop the WebDAV server, halt feedback,
  and cancel the context.

## Backup naming

Names are stable per device and computed without writing to the card:

1. An existing `unique.id` on the card (read-only, backward compat).
2. The Pi-side registry keyed by the card's blkid UUID/serial
   (`<backup-path>/.device-names.json`); minted on first sight.
3. Timestamp fallback `backup_YYYYMMDD_HHMMSS` when no stable identifier exists.

## Advanced features

### Pi-side device name registry
Each device is identified by its stable blkid UUID or serial. On first sight a
random 6-character ID is minted and stored in `<backup-path>/.device-names.json`
on the **destination disk**, mapping that identifier to a friendly name. The
same card re-inserted later resolves to the same backup folder. The card
itself is never written.

### Permission Preservation
rsync is configured with `--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw` to normalise the
permissions of the backup copies for easy access on the Pi.

### Data Safety
After each backup, the application flushes the destination disk buffers with
`sync` and refreshes the backup directory timestamp. The pre-unmount `sync`
ensures the card's read-only mount can be cleanly unmounted.

### Parallel Operation Prevention
A mutex prevents multiple backup operations from running simultaneously. A
separate mutex debounces the manual-scan touch button so holding it cannot
launch overlapping scans.

### Clean Feedback Abstraction
The event-based feedback system keeps core logic separate from any UI/hardware:
- **Separation of concerns** - Core logic separate from feedback hardware
- **Flexibility** - Easy to implement new feedback methods
- **Simplicity** - Single `Notify` method
- **Extensibility** - Add new event types as needed

## Service management

```bash
# Check status
sudo systemctl status pibackup-go.service

# View logs (follow mode)
sudo journalctl -u pibackup-go.service -f

# View application logs
tail -f /share/logs/pibackup.log

# Restart
sudo systemctl restart pibackup-go.service

# Stop
sudo systemctl stop pibackup-go.service
```

## Why Go?

- **No dependencies**: Single binary, no Python/venv/pip
- **Simple deployment**: Just copy the binary
- **Reliable**: No complex service setup
- **Fast**: Efficient file operations
- **Cross-platform**: Easy to build for different architectures
- **Event-driven**: Real-time device detection
- **Robust logging**: Built-in rotation and structured events
- **Source-safe**: Read-only mounts, no card writes, context-based interruption
- **Clean abstractions**: Simple, focused interfaces

## Comparison

| Feature | Python Version | Go Version |
|---------|---------------|------------|
| Binary size | ~50MB (with deps) | ~5MB |
| Dependencies | Python, venv, pip, many packages | fsnotify + lumberjack + go-udev |
| Setup complexity | High (venv, service, dependencies) | Low (just copy binary) |
| Features | Full (hotspot, gallery, buttons) | Core backup features |
| Maintenance | High | Low |
| Device detection | Polling (5s delay) | Event-driven (uevent + fsnotify fallback) |
| Logging | Basic | Structured + rotation |
| Source safety | — | Read-only mounts, no card writes |
| Backup naming | unique.id on device | Pi-side registry by UUID/serial (card untouched) |
| Permission preservation | Yes | Yes |
| Data safety | Yes | Yes |
| Parallel prevention | Yes | Yes (+ manual-scan debounce) |
| Cancellation | — | Context-based; shutdown interrupts in-flight backup |
| User feedback | Physical buttons only | Abstract event system (touchphat/console/log/none) |

## Safety design (summary)

The single most important property is that the camera SD card is never
corrupted. This is enforced at multiple layers:

1. **Read-only mount by default** (`mount.readonly = true`): the filesystem
   layer cannot write to the card.
2. **No application writes to the source**: naming is done via a Pi-side
   registry; an existing `unique.id` is only ever read.
3. **`noatime`** on every mount: reading the card doesn't update access times
   (no metadata writes).
4. **`sync` before unmount**: the read-only mount is cleanly released.
5. **Context-based interruption**: shutdown never leaves a mount dangling; an
   interrupted backup is resumed and completed on the next insertion.

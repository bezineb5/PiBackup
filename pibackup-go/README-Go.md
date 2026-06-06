# PiBackup Go

A simple, lightweight photo backup tool for Raspberry Pi written in Go.

## Features

- **Simple**: Single binary, no dependencies
- **Lightweight**: ~5MB binary vs complex Python setup
- **Reliable**: Direct device detection and mounting
- **Fast**: Efficient file copying with rsync
- **Event-driven**: Uses fsnotify to watch for device changes (no polling)
- **Robust logging**: Automatic log rotation with structured events
- **Unique ID generation**: Creates and stores unique IDs on devices for consistent naming
- **Permission preservation**: Maintains file permissions during backup
- **Data safety**: Flushes disk buffers to ensure data is written
- **Parallel operation prevention**: Prevents multiple backups running simultaneously
- **Clean user feedback**: Abstract event-based feedback system
- **WebDAV server**: Remote access to backup content via iOS Files app

## What it does

1. **Watches** for USB storage devices being plugged in (using fsnotify)
2. **Automatically mounts** them when detected
3. **Runs rsync** to copy photos to `/share`
4. **Unmounts** when done
5. **Processes existing devices** on startup
6. **Logs everything** with automatic rotation

## Build

```bash
make build
```

This creates a `pibackup-go` binary for ARM (Raspberry Pi).

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

- **Instantly detect** the device (no polling delay)
- Mount it automatically
- Copy all files to `/share/[backup-name]`
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
- `-f, --feedback string` - Feedback type (cap1166, console, log, none)

#### Configuration Priority

1. **Command-line flags** (highest priority)
2. **Environment variables** (PIBACKUP_*)
3. **Config file** (config.yaml)
4. **Default values** (lowest priority)

### Skip Backup

To skip backup of a specific device, create a `.backupignore` file at the root of the device:

```bash
# On the device root directory
touch .backupignore
```

The application will detect this file and skip the backup, logging the reason.

### Backup naming

- If `unique.id` file exists on the device, uses that name
- Otherwise uses timestamp: `backup_20240729_143022`

## Logging

The application uses structured logging with automatic rotation:

### Log Location
- **Log files**: `/share/logs/pibackup.log`
- **Rotation**: 10MB per file, 5 backup files, 30 days retention
- **Compression**: Old log files are automatically compressed
- **Format**: JSON structured logging (slog)

### Log Format
```json
{"time":"2024-07-29T14:30:22.123Z","level":"INFO","msg":"application started","version":"1.0.0","source":"main.go:45"}
{"time":"2024-07-29T14:30:22.124Z","level":"INFO","msg":"watcher started","path":"/sys/block","source":"main.go:67"}
{"time":"2024-07-29T14:30:25.456Z","level":"INFO","msg":"device detected","device":"/dev/sda","event":"create","source":"main.go:89"}
{"time":"2024-07-29T14:30:27.789Z","level":"INFO","msg":"USB storage device detected","device":"/dev/sda","source":"main.go:95"}
{"time":"2024-07-29T14:30:27.790Z","level":"INFO","msg":"backup started","device":"/dev/sda","source":"main.go:98"}
{"time":"2024-07-29T14:30:28.123Z","level":"INFO","msg":"device mounted successfully","device":"/dev/sda","mount_point":"/media/sda","source":"main.go:115"}
{"time":"2024-07-29T14:30:45.456Z","level":"INFO","msg":"backup completed successfully","device":"/dev/sda","destination":"/share/backup_20240729_143022","duration_seconds":18.5,"source":"main.go:145"}
```

### Key Events
- `application started` - Application startup with version
- `device detected` - New device found
- `USB storage device detected` - USB storage device confirmed
- `backup started` - Backup process started
- `device mounted successfully` - Device mounted successfully
- `backup completed successfully` - Backup completed with duration
- `shutting down` - Application shutdown
- `generated and stored unique ID` - Unique ID created and stored on device
- `backup already in progress` - Parallel operation prevented

### Benefits of slog
- **Structured JSON** - Easy to parse and analyze
- **Source location** - Shows exact file and line number
- **Level-based filtering** - INFO, WARN, ERROR levels
- **Key-value pairs** - Structured data for each event
- **Performance** - Optimized for high-throughput logging

## User Feedback

The application uses a clean, abstract feedback system based on events:

### Core Concept

```go
type UserFeedback interface {
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

### Example Implementations

#### Console Output
```go
type ConsoleFeedback struct{}

func (c *ConsoleFeedback) Notify(event Event) {
    timestamp := event.Timestamp.Format("15:04:05")
    fmt.Printf("[%s] %s: %s\n", timestamp, event.Type, event.Message)
}
```

#### Structured Logging
```go
type LogFeedback struct {
    logger *slog.Logger
}

func (l *LogFeedback) Notify(event Event) {
    l.logger.Info("user feedback",
        "type", event.Type,
        "message", event.Message,
        "device", event.Device,
        "progress", event.Progress,
    )
}
```

#### Web Interface
```go
type WebFeedback struct {
    clients map[chan Event]bool
}

func (w *WebFeedback) Notify(event Event) {
    // Send event to all connected web clients
    for client := range w.clients {
        select {
        case client <- event:
        default:
            // Client not reading, remove it
        }
    }
}
```

#### CAP1166 Hardware Interface
```go
// Using periph.io for CAP1166 capacitive touch sensor
feedback, err := NewCAP1166Feedback()
if err != nil {
    log.Fatal(err)
}
```

### Usage

```go
// No feedback (default)
config := &Config{
    Feedback: nil,
}

// Console feedback
config := &Config{
    Feedback: &ConsoleFeedback{},
}

// CAP1166 hardware feedback
feedback, err := NewCAP1166Feedback()
if err != nil {
    log.Fatal(err)
}
config := &Config{
    Feedback: feedback,
}

// Custom feedback implementation
config := &Config{
    Feedback: &MyCustomFeedback{},
}
```

## Configuration

The application uses Viper for flexible configuration management with multiple sources:

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

Example configuration:
```yaml
backup:
  path: "/share"
  usb_mount_path: "/media"
  skip_patterns: [".backupignore"]

logging:
  level: "info"
  path: "/share/logs"
  max_size: 10
  max_backups: 20
  max_age: 120
  compress: true

webdav:
  enabled: true
  port: "80"
  username: ""
  password: ""

feedback:
  type: "cap1166"
  led_brightness: 50
```

### Environment Variables

All configuration values can be overridden with environment variables:

```bash
# CAP1166 hardware feedback (default)
export PIBACKUP_FEEDBACK="cap1166"

# Console output feedback
export PIBACKUP_FEEDBACK="console"

# Structured logging feedback
export PIBACKUP_FEEDBACK="log"

# No feedback
export PIBACKUP_FEEDBACK="none"
```

### Default Behavior

- **CAP1166 is the default** - If no environment variable is set, the application will attempt to use CAP1166 hardware feedback
- **Graceful fallback** - If CAP1166 initialization fails, the application falls back to no feedback and continues running
- **Unknown types** - If an unknown feedback type is specified, it falls back to CAP1166

## WebDAV Remote Access

The application includes a built-in WebDAV server for remote access to backup content from iOS devices.

### Features

- **Read-only access** - Safe exploration of backup content
- **iOS Files app integration** - Native support without additional apps
- **Auto-discovery** - Automatically shows available backups
- **CORS support** - Works with web browsers
- **Security** - Path traversal protection and write operation blocking

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
4. **Tap "Connect"** - no username/password required
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

### Security Features

- **Read-only access** - No write operations allowed
- **Path validation** - Prevents directory traversal attacks
- **CORS headers** - Safe for web browser access
- **Error logging** - All access attempts logged

## How it works

- **Event-driven**: Uses `fsnotify` to watch `/sys/block` for device changes
- **Instant detection**: No 5-second polling delay
- **Concurrent processing**: Each device is processed in its own goroutine
- **Graceful shutdown**: Handles SIGINT/SIGTERM properly
- **Structured logging**: All events logged with context and timing
- **Parallel prevention**: Mutex prevents multiple backups running simultaneously
- **Data safety**: Disk buffers flushed after each backup
- **Clean feedback**: Event-based user notification system
- **WebDAV server**: Remote access to backup content via iOS Files app

## Backup naming

- If `unique.id` file exists on the device, uses that name
- If no `unique.id` exists, generates a random 6-character ID (e.g., "ABC123") and stores it
- Falls back to timestamp: `backup_20240729_143022` if ID storage fails

## Advanced features

### Unique ID Generation
The application automatically generates unique 6-character IDs (e.g., "ABC123") and stores them in a `unique.id` file on the device. This ensures consistent backup naming across multiple insertions of the same device.

### Permission Preservation
Rsync is configured with `--chmod=Du=rwx,Dgo=rwx,Fu=rw,Fog=rw` to preserve file permissions and ensure proper access rights on the backup.

### Data Safety
After each backup, the application:
- Flushes disk buffers with `sync` command
- Updates destination directory timestamp
- Ensures all data is safely written to disk

### Parallel Operation Prevention
A mutex prevents multiple backup operations from running simultaneously, preventing conflicts and resource contention.

### Clean Feedback Abstraction
The event-based feedback system provides:
- **Separation of concerns** - Core logic separate from UI
- **Flexibility** - Easy to implement new feedback methods
- **Simplicity** - Single method interface
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
- **Production ready**: Proper error handling and data safety
- **Clean abstractions**: Simple, focused interfaces

## Comparison

| Feature | Python Version | Go Version |
|---------|---------------|------------|
| Binary size | ~50MB (with deps) | ~5MB |
| Dependencies | Python, venv, pip, many packages | fsnotify + lumberjack |
| Setup complexity | High (venv, service, dependencies) | Low (just copy binary) |
| Features | Full (hotspot, gallery, buttons) | Core backup features |
| Maintenance | High | Low |
| Device detection | Polling (5s delay) | Event-driven (instant) |
| Logging | Basic | Structured + rotation |
| Unique IDs | Yes | Yes |
| Permission preservation | Yes | Yes |
| Data safety | Yes | Yes |
| Parallel prevention | Yes | Yes |
| User feedback | Physical buttons only | Abstract event system |

Perfect for a robust backup solution!
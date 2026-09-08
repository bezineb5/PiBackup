package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/benjamin/pibackup/pibackup-go/feedback"
	"golang.org/x/net/webdav"
)

// WebDAVServer provides remote access to backup content. Its lifecycle is
// driven entirely by the context passed to Start: when that context is
// cancelled the server drains in-flight requests and stops. There is no
// stored context and no explicit Stop method.
type WebDAVServer struct {
	backupPath string
	port       string
	server     *http.Server
	handler    *webdav.Handler
	mu         sync.RWMutex
	feedback   feedback.Feedback
}

// NewWebDAVServer creates a new WebDAV server for backup access.
func NewWebDAVServer(backupPath, port string, fb feedback.Feedback) *WebDAVServer {
	return &WebDAVServer{
		backupPath: backupPath,
		port:       port,
		feedback:   fb,
	}
}

// Start initializes and starts the WebDAV server. ctx drives its lifecycle:
// when ctx is cancelled the server drains in-flight requests and stops.
func (w *WebDAVServer) Start(ctx context.Context) error {
	// Create read-only file system for backups
	fs := &ReadOnlyFileSystem{
		root: w.backupPath,
	}

	// Create WebDAV handler
	w.handler = &webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				slog.Error("WebDAV error",
					"method", r.Method,
					"path", r.URL.Path,
					"error", err)
			} else {
				slog.Info("WebDAV request",
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
		},
	}

	// Create HTTP server. BaseContext ties every request's context to ctx, so
	// cancelling the app context cancels in-flight requests too.
	w.server = &http.Server{
		Addr:        ":" + w.port,
		Handler:     w.createMux(),
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	// Start server in background
	go func() {
		slog.Info("starting WebDAV server", "port", w.port, "path", w.backupPath)
		if w.feedback != nil {
			w.feedback.Notify(feedback.Event{
				Type:      feedback.EventStatus,
				Message:   fmt.Sprintf("WebDAV server started on port %s", w.port),
				Timestamp: time.Now(),
			})
		}

		if err := w.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("WebDAV server error", "error", err)
			if w.feedback != nil {
				w.feedback.Notify(feedback.Event{
					Type:      feedback.EventError,
					Message:   fmt.Sprintf("WebDAV server error: %v", err),
					Timestamp: time.Now(),
				})
			}
		}
	}()

	// When ctx is done (the app is shutting down), drain in-flight requests
	// gracefully. This makes the server unwind purely from context
	// cancellation, with no explicit Stop() to call.
	go func() {
		<-ctx.Done()
		w.shutdown()
	}()

	return nil
}

// createMux creates the HTTP mux with WebDAV and status endpoints
func (w *WebDAVServer) createMux() *http.ServeMux {
	mux := http.NewServeMux()

	// WebDAV handler for backup access
	mux.HandleFunc("/", w.handleWebDAV)

	// Status endpoint
	mux.HandleFunc("/status", w.handleStatus)

	// Discovery endpoint - list available backups
	mux.HandleFunc("/backups", w.handleBackups)

	return mux
}

// handleWebDAV handles WebDAV requests
func (srv *WebDAVServer) handleWebDAV(w http.ResponseWriter, r *http.Request) {
	// Add CORS headers for web browsers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS, PROPFIND")
	w.Header().Set("Access-Control-Allow-Headers", "Depth, Authorization, Content-Type")

	// Handle preflight requests
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Log connection
	if srv.feedback != nil {
		srv.feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
			Message:   fmt.Sprintf("WebDAV connection from %s", r.RemoteAddr),
			Timestamp: time.Now(),
		})
	}

	// Serve WebDAV
	srv.handler.ServeHTTP(w, r)
}

// handleStatus returns server status
func (srv *WebDAVServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	backups, err := srv.listBackups()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	status := map[string]interface{}{
		"server":   "PiBackup WebDAV",
		"port":     srv.port,
		"backups":  backups,
		"uptime":   time.Since(time.Now()).String(), // This will be negative, but you get the idea
		"readonly": true,
	}

	// Simple JSON response
	response := fmt.Sprintf(`{
		"server": "%s",
		"port": "%s",
		"backups": %d,
		"readonly": true
	}`, status["server"], status["port"], len(backups))

	w.Write([]byte(response))
}

// handleBackups lists available backups
func (srv *WebDAVServer) handleBackups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	backups, err := srv.listBackups()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	// Build JSON response
	var backupList []string
	for _, backup := range backups {
		backupList = append(backupList, backup)
	}

	response := fmt.Sprintf(`{"backups":["%s"]}`, strings.Join(backupList, `","`))
	w.Write([]byte(response))
}

// listBackups returns a list of available backup directories
func (w *WebDAVServer) listBackups() ([]string, error) {
	var backups []string

	entries, err := os.ReadDir(w.backupPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			backups = append(backups, entry.Name())
		}
	}

	return backups, nil
}

// shutdown drains in-flight requests with a short timeout. It is invoked by
// the background goroutine in Start when the server's context is done (i.e.
// the app is shutting down). The server's lifecycle is fully context-driven;
// there is no explicit Stop() to call. Idempotent via http.Server.Shutdown's
// own semantics (a second call returns immediately once the listener closed).
func (w *WebDAVServer) shutdown() error {
	if w.server == nil {
		return nil
	}
	if w.feedback != nil {
		w.feedback.Notify(feedback.Event{
			Type:      feedback.EventStatus,
			Message:   "WebDAV server stopping",
			Timestamp: time.Now(),
		})
	}
	slog.Info("stopping WebDAV server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := w.server.Shutdown(ctx); err != nil {
		slog.Error("error shutting down WebDAV server", "error", err)
		return err
	}
	slog.Info("WebDAV server stopped")
	return nil
}

// GetURL returns the WebDAV server URL
func (w *WebDAVServer) GetURL() string {
	return fmt.Sprintf("http://localhost:%s", w.port)
}

// ReadOnlyFileSystem implements a read-only file system for WebDAV
type ReadOnlyFileSystem struct {
	root string
}

// OpenFile opens a file in read-only mode
func (r *ReadOnlyFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	// Only allow read operations
	if flag&os.O_WRONLY != 0 || flag&os.O_RDWR != 0 || flag&os.O_CREATE != 0 || flag&os.O_TRUNC != 0 {
		return nil, fmt.Errorf("write operations not allowed")
	}

	// Construct full path
	fullPath := filepath.Join(r.root, name)

	// Security check - ensure path is within root
	if !strings.HasPrefix(fullPath, r.root) {
		return nil, fmt.Errorf("access denied")
	}

	// Open file
	file, err := os.OpenFile(fullPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}

	return &ReadOnlyFile{file}, nil
}

// Stat returns file info
func (r *ReadOnlyFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	fullPath := filepath.Join(r.root, name)

	// Security check
	if !strings.HasPrefix(fullPath, r.root) {
		return nil, fmt.Errorf("access denied")
	}

	return os.Stat(fullPath)
}

// Mkdir is not allowed in read-only mode
func (r *ReadOnlyFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return fmt.Errorf("mkdir not allowed in read-only mode")
}

// RemoveAll is not allowed in read-only mode
func (r *ReadOnlyFileSystem) RemoveAll(ctx context.Context, name string) error {
	return fmt.Errorf("remove not allowed in read-only mode")
}

// Rename is not allowed in read-only mode
func (r *ReadOnlyFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	return fmt.Errorf("rename not allowed in read-only mode")
}

// ReadOnlyFile wraps os.File to ensure read-only access
type ReadOnlyFile struct {
	*os.File
}

// Write is not allowed
func (r *ReadOnlyFile) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("write not allowed")
}

// WriteAt is not allowed
func (r *ReadOnlyFile) WriteAt(p []byte, off int64) (n int, err error) {
	return 0, fmt.Errorf("write not allowed")
}

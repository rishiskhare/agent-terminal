package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"agent-terminal/internal/jsonrpc"
	"agent-terminal/internal/scrollback"
	"github.com/aymanbagabas/go-pty"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

//go:embed all:themes
var themeFs embed.FS

const scrollbackBytes = 2 << 20 // 2 MiB

type Session struct {
	ID  string
	Pty pty.Pty
	Buf *scrollback.Ring

	mu      sync.Mutex
	writeMu sync.Mutex // serializes websocket writes (replay, live, ping)
	client  *websocket.Conn
	cols    int
	rows    int
	closed  bool
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	port     int
	logger   *slog.Logger
}

func NewSessionManager(port int, logger *slog.Logger) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
		port:     port,
		logger:   logger,
	}
}

func (m *SessionManager) Get(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *SessionManager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.sessions))
	for id, s := range m.sessions {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if !closed {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *SessionManager) wsURL(id string) string {
	return fmt.Sprintf("ws://127.0.0.1:%d/tty/%s", m.port, id)
}

func (m *SessionManager) Create(cmdFactory func(tty pty.Pty) (*pty.Cmd, error)) (*Session, error) {
	tty, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create pty: %w", err)
	}

	cmd, err := cmdFactory(tty)
	if err != nil {
		tty.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		tty.Close()
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	id := strings.ToLower(rand.Text())
	session := &Session{
		ID:  id,
		Pty: tty,
		Buf: scrollback.New(scrollbackBytes),
	}

	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()

	go session.readLoop(m.logger)
	go func() {
		_ = cmd.Wait()
		session.markClosed()
	}()

	return session, nil
}

func (m *SessionManager) Destroy(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("invalid tty ID: %s", id)
	}
	session.close()
	return nil
}

func (s *Session) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *Session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
	_ = s.Pty.Close()
}

func (s *Session) readLoop(logger *slog.Logger) {
	buf := make([]byte, maxBufferSizeBytes)
	for {
		n, err := s.Pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			_, _ = s.Buf.Write(chunk)

			s.mu.Lock()
			conn := s.client
			s.mu.Unlock()
			if conn != nil {
				s.writeMu.Lock()
				writeErr := conn.WriteMessage(websocket.BinaryMessage, chunk)
				s.writeMu.Unlock()
				if writeErr != nil {
					logger.Debug("failed to write tty output to websocket", "error", writeErr)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				logger.Debug("pty read ended", "error", err, "session", s.ID)
			}
			s.markClosed()
			return
		}
	}
}

func (s *Session) attach(conn *websocket.Conn) {
	s.mu.Lock()
	if s.client != nil {
		_ = s.client.Close()
	}
	s.client = conn
	s.mu.Unlock()
}

func (s *Session) detach(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == conn {
		s.client = nil
	}
}

func NewCmdServe() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "serve",
		Hidden:       true,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create log directory: %v\n", err)
				os.Exit(1)
			}

			logFile, err := os.OpenFile(filepath.Join(cacheDir, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
				os.Exit(1)
			}
			defer logFile.Close()
			logger := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{}))

			port, err := getFreePort()
			if err != nil {
				return fmt.Errorf("failed to get free port: %w", err)
			}

			sessions := NewSessionManager(port, logger)
			messagingHost := NewMessagingHost(logger, sessions)

			handler := NewWebSocketHandler(sessions)

			logger.Info("Listening", "port", port)

			server := &http.Server{
				Addr:    fmt.Sprintf(":%d", port),
				Handler: handler,
			}

			done := make(chan error, 1)

			go func() {
				if err := messagingHost.Listen(); err != nil {
					logger.Error("Messaging host listen loop exited", "error", err)
					done <- err
				} else {
					logger.Info("Messaging host stopped normally")
					done <- nil
				}
			}()

			go func() {
				if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("HTTP server error", "error", err)
					done <- err
				}
			}()

			err = <-done
			logger.Info("Shutting down server")
			server.Shutdown(context.Background())
			return err
		},
	}

	return cmd
}

// CommandMetadata holds structured metadata extracted from script files.
type CommandMetadata struct {
	Title               string   `json:"title"`
	Contexts            []string `json:"contexts"`
	DocumentUrlPatterns []string `json:"documentUrlPatterns,omitempty"`
	TargetUrlPatterns   []string `json:"targetUrlPatterns,omitempty"`
}

func NewMessagingHost(logger *slog.Logger, sessions *SessionManager) *jsonrpc.Host {
	messagingHost := jsonrpc.NewHost(logger)

	messagingHost.HandleRequest("initialize", func(input []byte) (any, error) {
		var params struct {
			Version   string `json:"version"`
			BrowserID string `json:"browserId"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal initialize params: %w", err)
		}

		logger.Info("Received initialize notification", "version", params.Version, "browserId", params.BrowserID)
		return map[string]any{}, nil
	})

	messagingHost.HandleRequest("tty.create", func(input []byte) (any, error) {
		var params struct {
			Mode string   `json:"mode"`
			App  string   `json:"app"`
			Args []string `json:"args"`
			Cwd  string   `json:"cwd"`
			File string   `json:"file"`
		}

		if len(input) > 0 {
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("failed to unmarshal create params: %w", err)
			}
		}

		mode, app := params.Mode, params.App
		if mode == "" && app == "" {
			if defaultApp := k.String("defaultApp"); defaultApp != "" && appExists(defaultApp) {
				mode = "app"
				app = defaultApp
			}
		}

		session, err := sessions.Create(func(tty pty.Pty) (*pty.Cmd, error) {
			return buildPtyCommand(tty, mode, app, params.Args, params.Cwd)
		})
		if err != nil {
			return nil, err
		}

		return map[string]string{
			"url": sessions.wsURL(session.ID),
			"id":  session.ID,
		}, nil
	})

	messagingHost.HandleRequest("tty.attach", func(input []byte) (any, error) {
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attach params: %w", err)
		}
		if params.ID == "" {
			return nil, fmt.Errorf("id is required")
		}

		session, ok := sessions.Get(params.ID)
		if !ok {
			return nil, fmt.Errorf("invalid tty ID: %s", params.ID)
		}
		session.mu.Lock()
		closed := session.closed
		session.mu.Unlock()
		if closed {
			return nil, fmt.Errorf("tty session is closed: %s", params.ID)
		}

		return map[string]string{
			"url": sessions.wsURL(session.ID),
			"id":  session.ID,
		}, nil
	})

	messagingHost.HandleRequest("tty.list", func(input []byte) (any, error) {
		ids := sessions.List()
		items := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			items = append(items, map[string]string{"id": id})
		}
		return map[string]any{"sessions": items}, nil
	})

	messagingHost.HandleRequest("tty.destroy", func(input []byte) (any, error) {
		var params struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal destroy params: %w", err)
		}
		if err := sessions.Destroy(params.ID); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	})

	messagingHost.HandleNotification("tty.resize", func(input []byte) error {
		var requestParams struct {
			TTY  string `json:"tty"`
			Rows int    `json:"rows"`
			Cols int    `json:"cols"`
		}
		if err := json.Unmarshal(input, &requestParams); err != nil {
			return fmt.Errorf("failed to unmarshal resize params: %w", err)
		}

		session, ok := sessions.Get(requestParams.TTY)
		if !ok {
			return fmt.Errorf("invalid tty ID: %s", requestParams.TTY)
		}

		if err := session.Pty.Resize(requestParams.Cols, requestParams.Rows); err != nil {
			return fmt.Errorf("failed to set size for tty: %w", err)
		}
		session.mu.Lock()
		session.cols = requestParams.Cols
		session.rows = requestParams.Rows
		session.mu.Unlock()

		return nil
	})

	messagingHost.HandleRequest("xterm.getConfig", func(input []byte) (any, error) {
		var params struct {
			Variant string `json:"variant"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal xterm config params: %w", err)
		}

		var theme string
		if darkTheme := k.String("themeDark"); params.Variant == "dark" && darkTheme != "" {
			theme = darkTheme
		} else {
			theme = k.String("theme")
		}

		themeBytes, err := themeFs.ReadFile(filepath.Join("themes", theme+".json"))
		if err != nil {
			return nil, fmt.Errorf("failed to read theme file: %w", err)
		}

		xtermConfig := map[string]interface{}{
			"cursorBlink":                   true,
			"allowProposedApi":              true,
			"macOptionIsMeta":               true,
			"macOptionClickForcesSelection": true,
			"fontSize":                      13,
			"fontFamily":                    "Consolas,Liberation Mono,Menlo,Courier,monospace",
			"theme":                         json.RawMessage(themeBytes),
		}

		if err := k.Unmarshal("xterm", &xtermConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal xterm config: %w", err)
		}

		return xtermConfig, nil
	})

	messagingHost.HandleRequest("readFile", func(input []byte) (any, error) {
		var params struct {
			Path string `json:"path"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal readFile params: %w", err)
		}

		content, err := os.ReadFile(params.Path)
		if err != nil && errors.Is(err, os.ErrNotExist) {
			return map[string]any{
				"content": "",
			}, nil
		} else if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}

		return map[string]any{
			"content": string(content),
		}, nil
	})

	messagingHost.HandleRequest("writeFile", func(input []byte) (any, error) {
		var params struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}

		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal writeFile params: %w", err)
		}

		f, err := os.OpenFile(params.Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open file for writing: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString(params.Content); err != nil {
			return nil, fmt.Errorf("failed to write file content: %w", err)
		}

		return map[string]any{}, nil
	})

	messagingHost.HandleRequest("config.get", func(input []byte) (any, error) {
		cfg, err := readConfigMap()
		if err != nil {
			return nil, err
		}
		themes, _ := listThemeNames()
		apps, _ := listAppNames()
		return map[string]any{
			"config": cfg,
			"themes": themes,
			"apps":   apps,
			"path":   configPath(),
		}, nil
	})

	messagingHost.HandleRequest("config.set", func(input []byte) (any, error) {
		var params struct {
			Config map[string]any `json:"config"`
		}
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config.set params: %w", err)
		}
		if params.Config == nil {
			return nil, fmt.Errorf("config is required")
		}
		if err := writeConfigMap(params.Config); err != nil {
			return nil, err
		}
		cfg, err := readConfigMap()
		if err != nil {
			return nil, err
		}
		return map[string]any{"config": cfg}, nil
	})

	messagingHost.HandleRequest("doctor.status", func(input []byte) (any, error) {
		return runDoctor(false)
	})

	messagingHost.HandleRequest("doctor.fix", func(input []byte) (any, error) {
		return runDoctor(true)
	})

	return messagingHost
}

func listThemeNames() ([]string, error) {
	entries, err := themeFs.ReadDir("themes")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		names = append(names, name)
	}
	return names, nil
}

func listAppNames() ([]string, error) {
	entries, err := os.ReadDir(appDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		names = append(names, strings.TrimSuffix(name, filepath.Ext(name)))
	}
	return names, nil
}

func appExists(app string) bool {
	if app == "" {
		return false
	}
	entrypoint := filepath.Join(appDir, app)
	if _, err := os.Stat(entrypoint); err == nil {
		return true
	}
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return false
	}
	for _, file := range entries {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if strings.TrimSuffix(name, filepath.Ext(name)) == app {
			return true
		}
	}
	return false
}

func buildPtyCommand(tty pty.Pty, mode, app string, args []string, cwd string) (*pty.Cmd, error) {
	var cmd *pty.Cmd
	if mode == "app" && app != "" {
		entrypoint := filepath.Join(appDir, app)
		stat, err := os.Stat(entrypoint)

		if os.IsNotExist(err) {
			files, readErr := os.ReadDir(appDir)
			if readErr == nil {
				for _, file := range files {
					if file.IsDir() {
						continue
					}

					name := file.Name()
					nameWithoutExt := strings.TrimSuffix(name, filepath.Ext(name))

					if nameWithoutExt == app {
						entrypoint = filepath.Join(appDir, name)
						stat, err = os.Stat(entrypoint)
						break
					}
				}
			}
		}

		if err != nil {
			return nil, fmt.Errorf("failed to stat app entrypoint: %w", err)
		}

		if stat.IsDir() {
			return nil, fmt.Errorf("app entrypoint is a directory, expected a file: %s", entrypoint)
		}

		if stat.Mode()&0111 == 0 {
			if err := os.Chmod(entrypoint, 0755); err != nil {
				return nil, fmt.Errorf("failed to make app entrypoint executable: %w", err)
			}
		}

		cmd = tty.Command(entrypoint, args...)
	} else {
		cmd = tty.Command(k.String("command"), k.Strings("args")...)
	}

	cmd.Env = mergePtyEnv(os.Environ(), map[string]string{
		"TERM":         "xterm-256color",
		"TERM_PROGRAM": "agent-terminal",
	}, k.StringMap("env"))

	if cwd != "" {
		cmd.Dir = cwd
	} else {
		cmd.Dir = os.Getenv("HOME")
	}

	// Best-effort: clear stale agent-browser daemons after Chrome sleep/reopen
	// before the agent issues its first browser command.
	go healAgentBrowserBestEffort()

	return cmd, nil
}

// mergePtyEnv builds the PTY environment. Later maps win; keys replace any
// prior entry so config PATH (with the agent-browser shim) is not ignored
// behind a duplicate inherited PATH= (getenv uses the first occurrence).
func mergePtyEnv(base []string, overlays ...map[string]string) []string {
	envMap := map[string]string{}
	order := make([]string, 0, len(base)+8)
	for _, e := range base {
		key, val, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if _, exists := envMap[key]; !exists {
			order = append(order, key)
		}
		envMap[key] = val
	}
	for _, overlay := range overlays {
		for key, val := range overlay {
			if _, exists := envMap[key]; !exists {
				order = append(order, key)
			}
			envMap[key] = val
		}
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+envMap[key])
	}
	return out
}

func healAgentBrowserBestEffort() {
	real := loadRealAgentBrowserPath()
	if real == "" {
		return
	}
	if _, err := os.Stat(real); err != nil {
		return
	}
	if err := healStaleAgentBrowserSessions(real); err != nil {
		log.Printf("agent-browser heal: %v", err)
	}
}

func NewWebSocketHandler(sessions *SessionManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ttyID := strings.TrimPrefix(r.URL.Path, "/tty/")
		session, ok := sessions.Get(ttyID)
		if !ok {
			http.Error(w, fmt.Sprintf("invalid terminal ID: %s", ttyID), http.StatusBadRequest)
			return
		}

		HandleWebsocket(session)(w, r)
	})
}

func HandleWebsocket(session *Session) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		upgrader := getConnectionUpgrader(maxBufferSizeBytes)
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("failed to upgrade connection: %s", err)
			return
		}
		defer connection.Close()

		// Replay scrollback, then attach for live output — under writeMu so
		// the persistent reader cannot interleave frames mid-replay.
		session.writeMu.Lock()
		if snapshot := session.Buf.Bytes(); len(snapshot) > 0 {
			if err := connection.WriteMessage(websocket.BinaryMessage, snapshot); err != nil {
				session.writeMu.Unlock()
				log.Printf("failed to replay scrollback: %s", err)
				return
			}
		}
		session.attach(connection)
		session.writeMu.Unlock()
		defer session.detach(connection)

		lastPingTime := time.Now()
		connection.SetPongHandler(func(appData string) error {
			lastPingTime = time.Now()
			return nil
		})

		done := make(chan struct{})

		go func() {
			ticker := time.NewTicker(keepalivePingTimeout / 2)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					session.writeMu.Lock()
					err := connection.WriteMessage(websocket.PingMessage, []byte("keepalive"))
					session.writeMu.Unlock()
					if err != nil {
						return
					}
					if time.Since(lastPingTime) > keepalivePingTimeout {
						connection.Close()
						return
					}
				}
			}
		}()

		// Client input → PTY
		for {
			_, data, err := connection.ReadMessage()
			if err != nil {
				close(done)
				return
			}
			if _, err := session.Pty.Write(bytes.Trim(data, "\x00")); err != nil {
				log.Printf("failed to write to tty: %s", err)
				continue
			}
		}
	}
}

func getConnectionUpgrader(
	maxBufferSizeBytes int,
) websocket.Upgrader {
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		HandshakeTimeout: 0,
		ReadBufferSize:   maxBufferSizeBytes,
		WriteBufferSize:  maxBufferSizeBytes,
	}
}

// GetFreePort asks the kernel for a free open port that is ready to use.
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func ExtractMetadata(reader io.Reader) (CommandMetadata, error) {
	var result CommandMetadata
	scanner := bufio.NewScanner(reader)

	metadataRegex := regexp.MustCompile(`^.*?@agent-terminal\.(\w+)\s+(.*)\s*$`)

	for scanner.Scan() {
		line := scanner.Text()

		matches := metadataRegex.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}

		key := matches[1]
		rawValue := matches[2]

		switch key {
		case "title":
			result.Title = rawValue
		case "contexts":
			var contextsRaw []string
			err := json.Unmarshal([]byte(rawValue), &contextsRaw)
			if err != nil {
				return CommandMetadata{}, fmt.Errorf("failed to parse 'contexts' as string array from '%s': %w", rawValue, err)
			}
			result.Contexts = contextsRaw
		case "documentUrlPatterns":
			var patternsRaw []string
			if err := json.Unmarshal([]byte(rawValue), &patternsRaw); err != nil {
				return CommandMetadata{}, fmt.Errorf("failed to parse 'documentUrlPatterns' as string array from '%s': %w", rawValue, err)
			}
			result.DocumentUrlPatterns = patternsRaw
		case "targetUrlPatterns":
			var patternsRaw []string
			if err := json.Unmarshal([]byte(rawValue), &patternsRaw); err != nil {
				return CommandMetadata{}, fmt.Errorf("failed to parse 'targetUrlPatterns' as string array from '%s': %w", rawValue, err)
			}
			result.TargetUrlPatterns = patternsRaw
		}
	}

	if err := scanner.Err(); err != nil {
		return CommandMetadata{}, err
	}

	return result, nil
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/vokomarov/claude-code-approvals/internal/approvals"
	"github.com/vokomarov/claude-code-approvals/internal/config"
	"github.com/vokomarov/claude-code-approvals/internal/notifier"
	"github.com/vokomarov/claude-code-approvals/internal/telegram"
)

// Server holds all daemon state.
type Server struct {
	cfg        *config.Config
	store      *approvals.Store
	bot        *telegram.Bot
	httpServer *http.Server
	enabled    atomic.Bool
}

// permissionRequest is the JSON body received at POST /api/permission.
type permissionRequest struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	CWD       string          `json:"cwd"`
}

// New creates a Server. Returns an error if prerequisites fail
// (invalid Telegram token, invalid message template).
// The daemon starts in the enabled state.
func New(cfg *config.Config) (*Server, error) {
	if cfg.Telegram.MessageTemplate != "" {
		if _, err := template.New("").Parse(cfg.Telegram.MessageTemplate); err != nil {
			return nil, fmt.Errorf("invalid message_template: %w", err)
		}
	}

	if cfg.Timeouts.MacosNotificationSeconds > 0 && !notifier.IsAvailable() {
		slog.Warn("terminal-notifier not found; macOS notifications will be skipped")
		cfg.Timeouts.MacosNotificationSeconds = 0
	}

	store := approvals.NewStore()

	var bot *telegram.Bot
	if cfg.Timeouts.TelegramNotificationSeconds > 0 {
		var err error
		bot, err = telegram.NewBot(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.MessageTemplate)
		if err != nil {
			return nil, fmt.Errorf("telegram: %w", err)
		}
	}

	s := &Server{cfg: cfg, store: store, bot: bot}
	s.enabled.Store(true)
	return s, nil
}

// handler builds the HTTP mux. Extracted from Run for testability.
// ctx is the daemon lifetime context; it is captured by notification callbacks.
func (s *Server) handler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	mux.HandleFunc("POST /api/enable", func(w http.ResponseWriter, r *http.Request) {
		s.enabled.Store(true)
		slog.Info("daemon enabled")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /api/disable", func(w http.ResponseWriter, r *http.Request) {
		s.enabled.Store(false)
		slog.Info("daemon disabled")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /api/permission", func(w http.ResponseWriter, r *http.Request) {
		if !s.enabled.Load() {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var preq permissionRequest
		if err := json.NewDecoder(r.Body).Decode(&preq); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		req := approvals.NewRequest(preq.SessionID, preq.ToolName, string(preq.ToolInput))
		slog.Info("permission request received",
			"id", req.ID, "session", preq.SessionID, "tool", preq.ToolName)

		s.store.Add(req)
		defer func() {
			s.store.Delete(req.ID)
			req.Cancel()
		}()

		approvals.RunMachine(req, approvals.MachineOpts{
			MacosSeconds:    s.cfg.Timeouts.MacosNotificationSeconds,
			TelegramSeconds: s.cfg.Timeouts.TelegramNotificationSeconds,
			TotalSeconds:    s.cfg.Timeouts.TotalTimeoutSeconds,
			TimeoutPolicy:   s.cfg.Timeouts.TimeoutPolicy,
			OnMacos: func(ar *approvals.ApprovalRequest) {
				if s.cfg.Timeouts.MacosNotificationSeconds == 0 {
					return
				}
				title := fmt.Sprintf("Claude Code – %s", ar.ToolName)
				message := notifier.TruncateForMacOS(ar.ToolInput)
				timeoutSecs := s.cfg.Timeouts.TelegramNotificationSeconds - s.cfg.Timeouts.MacosNotificationSeconds
				if timeoutSecs <= 0 {
					slog.Warn("timeoutSecs for macOS notification is <= 0, defaulting to 30s")
					timeoutSecs = 30
				}
				go func() {
					result, err := notifier.Notify(ctx, title, message, s.cfg.MacOS.PhpStormBundleID, timeoutSecs)
					if err != nil {
						slog.Warn("terminal-notifier error", "id", ar.ID, "err", err)
						return
					}
					if result == "" {
						return // dismissed without interaction
					}
					decision := "deny"
					if result == "Approve" {
						decision = "allow"
					}
					select {
					case ar.Decision <- approvals.Decision{Value: decision, Source: "macos"}:
						slog.Info("macOS decision received", "id", ar.ID, "decision", decision)
					default:
					}
				}()
			},
			OnTelegram: func(ar *approvals.ApprovalRequest) {
				if s.bot == nil {
					return
				}
				if err := s.bot.SendApprovalRequest(ar); err != nil {
					slog.Error("telegram send failed", "id", ar.ID, "err", err)
				}
			},
		})

		select {
		case decision := <-req.Decision:
			slog.Info("permission decided",
				"id", req.ID, "decision", decision.Value, "source", decision.Source)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"decision":%q}`, decision.Value)
		case <-r.Context().Done():
			slog.Info("http connection dropped", "id", req.ID)
		}
	})

	return mux
}

// Run starts the daemon and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Daemon.Port)

	if s.bot != nil {
		go s.bot.PollForever(ctx, s.store)
	}

	s.httpServer = &http.Server{Addr: addr, Handler: s.handler(ctx)}

	slog.Info("daemon starting", "addr", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		slog.Info("daemon shutting down")
		s.shutdown()
		return nil
	}
}

func (s *Server) shutdown() {
	pending := s.store.All()
	for _, req := range pending {
		select {
		case req.Decision <- approvals.Decision{Value: "deny", Source: "timeout"}:
		default:
		}
		req.Cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		slog.Warn("http shutdown error", "err", err)
	}
	slog.Info("daemon stopped", "pending_flushed", len(pending))
}

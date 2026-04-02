package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"text/template"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/vokomarov/claude-code-approvals/internal/approvals"
	approvalmcp "github.com/vokomarov/claude-code-approvals/internal/mcp"
	"github.com/vokomarov/claude-code-approvals/internal/notifier"
	"github.com/vokomarov/claude-code-approvals/internal/telegram"
	"github.com/vokomarov/claude-code-approvals/internal/config"
)

// Server holds all daemon state.
type Server struct {
	cfg        *config.Config
	store      *approvals.Store
	bot        *telegram.Bot
	httpServer *http.Server
	mcpServer  *server.SSEServer
}

// New creates a Server. Returns an error if prerequisites fail
// (port conflict, invalid Telegram token, invalid message template).
func New(cfg *config.Config) (*Server, error) {
	// Validate message template at startup
	if cfg.Telegram.MessageTemplate != "" {
		if _, err := template.New("").Parse(cfg.Telegram.MessageTemplate); err != nil {
			return nil, fmt.Errorf("invalid message_template: %w", err)
		}
	}

	// Check terminal-notifier availability
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
	return s, nil
}

// Run starts the daemon and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Daemon.Port)

	// Start Telegram long-poll loop
	if s.bot != nil {
		go s.bot.PollForever(ctx, s.store)
	}

	// Build MCP server
	mcpSrv := server.NewMCPServer("cc-approvals", "1.0.0")
	tool := mcp.NewTool("request_permission",
		mcp.WithDescription("Request user approval for a Claude Code action"),
		mcp.WithString("tool_name", mcp.Required(), mcp.Description("Name of the tool requesting permission")),
		mcp.WithObject("tool_input", mcp.Required(), mcp.Description("Tool input parameters")),
		mcp.WithString("session_id", mcp.Description("Claude Code session identifier")),
	)

	cfg := s.cfg
	store := s.store
	bot := s.bot
	handlerOpts := approvalmcp.HandlerOpts{
		Store:           store,
		MacosSeconds:    cfg.Timeouts.MacosNotificationSeconds,
		TelegramSeconds: cfg.Timeouts.TelegramNotificationSeconds,
		TotalSeconds:    cfg.Timeouts.TotalTimeoutSeconds,
		TimeoutPolicy:   cfg.Timeouts.TimeoutPolicy,
		OnMacos: func(req *approvals.ApprovalRequest) {
			if cfg.Timeouts.MacosNotificationSeconds == 0 {
				return
			}
			title := fmt.Sprintf("Claude Code – %s", req.ToolName)
			message := notifier.TruncateForMacOS(req.ToolInput)
			timeoutSecs := cfg.Timeouts.TelegramNotificationSeconds - cfg.Timeouts.MacosNotificationSeconds
			if timeoutSecs <= 0 {
				timeoutSecs = 30
			}
			go func() {
				result, err := notifier.Notify(ctx, title, message, cfg.MacOS.PhpStormBundleID, timeoutSecs)
				if err != nil {
					slog.Warn("terminal-notifier error", "id", req.ID, "err", err)
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
				case req.Decision <- approvals.Decision{Value: decision, Source: "macos"}:
					slog.Info("macOS decision received", "id", req.ID, "decision", decision)
					req.Cancel()
				default:
				}
			}()
		},
		OnTelegram: func(req *approvals.ApprovalRequest) {
			if bot == nil {
				return
			}
			if err := bot.SendApprovalRequest(req); err != nil {
				slog.Error("telegram send failed", "id", req.ID, "err", err)
			}
		},
	}

	mcpSrv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolName, _ := req.Params.Arguments["tool_name"].(string)
		toolInputRaw, _ := req.Params.Arguments["tool_input"]
		sessionID, _ := req.Params.Arguments["session_id"].(string)

		toolInputJSON := fmt.Sprintf("%v", toolInputRaw)

		decision, err := approvalmcp.HandlePermissionRequest(handlerOpts, sessionID, toolName, toolInputJSON)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf(`{"decision":"%s"}`, decision)), nil
	})

	// HTTP mux: health + MCP SSE
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	sseSrv := server.NewSSEServer(mcpSrv, server.WithBaseURL(fmt.Sprintf("http://localhost:%d", cfg.Daemon.Port)))
	mux.Handle("/mcp", sseSrv)
	mux.Handle("/mcp/", sseSrv)

	s.httpServer = &http.Server{Addr: addr, Handler: mux}

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
	// Apply timeout_policy to all pending requests
	pending := s.store.All()
	for _, req := range pending {
		select {
		case req.Decision <- approvals.Decision{Value: s.cfg.Timeouts.TimeoutPolicy, Source: "timeout"}:
		default:
		}
		req.Cancel()
	}

	// Give in-flight responses 5s to flush
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.httpServer.Shutdown(ctx); err != nil {
		slog.Warn("http shutdown error", "err", err)
	}
	slog.Info("daemon stopped", "pending_flushed", len(pending))
}

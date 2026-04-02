package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/vokomarov/claude-code-approvals/internal/approvals"
)

// CallbackData formats the callback_data string for a Telegram inline button.
func CallbackData(action, requestID string) string {
	return action + ":" + requestID
}

// ParseCallback parses a callback_data string into a decision value and request ID.
// Returns decision ("allow" or "deny") and the request UUID.
func ParseCallback(data string) (decision, requestID string, err error) {
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid callback data: %q", data)
	}
	switch parts[0] {
	case "approve":
		decision = "allow"
	case "deny":
		decision = "deny"
	default:
		return "", "", fmt.Errorf("unknown action in callback: %q", parts[0])
	}
	return decision, parts[1], nil
}

// Bot wraps the Telegram API client.
type Bot struct {
	api     *tgbotapi.BotAPI
	chatID  int64
	tmplStr string // message template; empty = default
}

// NewBot creates a Bot. Returns an error if the token is invalid.
func NewBot(token string, chatID int64, messageTemplate string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("telegram bot init: %w", err)
	}
	return &Bot{api: api, chatID: chatID, tmplStr: messageTemplate}, nil
}

// SendApprovalRequest sends the approval notification for a request.
func (b *Bot) SendApprovalRequest(req *approvals.ApprovalRequest) error {
	data := FormatTemplateData(req.SessionID, req.ToolName, req.ToolInput, req.CreatedAt)
	text, err := RenderMessage(b.tmplStr, data)
	if err != nil {
		return fmt.Errorf("render template: %w", err)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Approve", CallbackData("approve", req.ID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Deny", CallbackData("deny", req.ID)),
		),
	)
	msg := tgbotapi.NewMessage(b.chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	_, err = b.api.Send(msg)
	return err
}

// PollForever runs the Telegram long-poll loop until the context is cancelled.
// When an inline button is pressed, it looks up the request by UUID in the store
// and writes the decision to the request's Decision channel.
func (b *Bot) PollForever(ctx context.Context, store *approvals.Store) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.api.GetUpdates(u)
		if err != nil {
			slog.Warn("telegram poll error, retrying", "err", err)
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}

		for _, update := range updates {
			u.Offset = update.UpdateID + 1
			if update.CallbackQuery == nil {
				continue
			}
			b.handleCallback(update.CallbackQuery, store)
		}
	}
}

func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery, store *approvals.Store) {
	decision, requestID, err := ParseCallback(cb.Data)
	if err != nil {
		slog.Warn("invalid callback data", "data", cb.Data, "err", err)
		return
	}

	req, ok := store.Get(requestID)
	if !ok {
		slog.Warn("callback for unknown request", "id", requestID)
		return
	}

	// Non-blocking send: if already decided, silently drop.
	select {
	case req.Decision <- approvals.Decision{Value: decision, Source: "telegram"}:
		slog.Info("telegram decision received", "id", requestID, "decision", decision)
		req.Cancel()
	default:
		slog.Info("telegram callback ignored (already decided)", "id", requestID)
	}

	// Acknowledge the callback to remove the loading state in Telegram UI.
	ack := tgbotapi.NewCallback(cb.ID, "")
	_, _ = b.api.Request(ack)
}

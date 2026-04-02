package telegram

import (
	"bytes"
	"strings"
	"text/template"
	"time"
)

// maxToolInputLen truncates ToolInput before rendering to leave ~296 chars
// of buffer for template boilerplate within Telegram's 4096-char message limit.
const maxToolInputLen = 3800

const defaultTemplate = `🔐 Claude Code Approval Required

Session: {{.SessionID}}
Tool:    {{.ToolName}}
Input:
` + "```" + `
{{.ToolInput}}
` + "```" + `

⏰ {{.CreatedAt}}

Waiting for response...`

// TemplateData holds the variables available in the Telegram message template.
type TemplateData struct {
	SessionID string
	ToolName  string
	ToolInput string // pre-truncated before rendering
	CreatedAt string // formatted time string, e.g. "15:04:05"
}

// RenderMessage renders the Telegram notification message.
// If tmplStr is empty, the default template is used.
// ToolInput is automatically truncated to maxToolInputLen before rendering.
func RenderMessage(tmplStr string, data TemplateData) (string, error) {
	if tmplStr == "" {
		tmplStr = defaultTemplate
	}
	tmpl, err := template.New("msg").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	// Truncate ToolInput to stay within Telegram's 4096 char limit.
	if len(data.ToolInput) > maxToolInputLen {
		data.ToolInput = data.ToolInput[:maxToolInputLen] + "...[truncated]"
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

// FormatTemplateData builds TemplateData from raw request fields.
func FormatTemplateData(sessionID, toolName, toolInput string, createdAt time.Time) TemplateData {
	return TemplateData{
		SessionID: sessionID,
		ToolName:  toolName,
		ToolInput: toolInput,
		CreatedAt: createdAt.Format("15:04:05"),
	}
}

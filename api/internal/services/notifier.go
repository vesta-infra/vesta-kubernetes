package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"kubernetes.getvesta.sh/api/internal/db"
)

type EventType string

const (
	EventDeployStarted  EventType = "deploy.started"
	EventDeployFailed   EventType = "deploy.failed"
	EventDeploySucceeded EventType = "deploy.succeeded"
	EventBuildStarted   EventType = "build.started"
	EventBuildSucceeded EventType = "build.succeeded"
	EventBuildFailed    EventType = "build.failed"
	EventAppRestarted   EventType = "app.restarted"
	EventAppScaled      EventType = "app.scaled"
	EventAppCreated     EventType = "app.created"
	EventAppDeleted     EventType = "app.deleted"
)

type NotificationEvent struct {
	Type        EventType `json:"type"`
	ProjectID   string    `json:"projectId"`
	AppID       string    `json:"appId"`
	Environment string    `json:"environment,omitempty"`
	Image       string    `json:"image,omitempty"`
	TriggeredBy string    `json:"triggeredBy,omitempty"`
	Message     string    `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	Extra       map[string]interface{} `json:"extra,omitempty"`
}

type Notifier struct {
	db     *db.DB
	client *http.Client
}

func NewNotifier(database *db.DB) *Notifier {
	return &Notifier{
		db: database,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Send dispatches a notification event to all matching channels for the project.
// It runs asynchronously — errors are logged and recorded in notification_history.
func (n *Notifier) Send(ctx context.Context, event NotificationEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	channels, err := n.db.GetChannelsForEvent(event.ProjectID, string(event.Type))
	if err != nil {
		log.Printf("[notify] error fetching channels for project %s event %s: %v", event.ProjectID, event.Type, err)
		return
	}

	for _, ch := range channels {
		go n.sendToChannel(ch, event)
	}
}

// SendTest sends a test event to a specific channel.
func (n *Notifier) SendTest(ctx context.Context, ch db.NotificationChannel) error {
	event := NotificationEvent{
		Type:      "test",
		ProjectID: ch.ProjectID,
		Message:   "This is a test notification from Vesta",
		Timestamp: time.Now().UTC(),
	}
	return n.dispatch(ch, event)
}

func (n *Notifier) sendToChannel(ch db.NotificationChannel, event NotificationEvent) {
	err := n.dispatch(ch, event)

	payloadBytes, _ := json.Marshal(event)
	record := &db.NotificationHistory{
		ChannelID:   ch.ID,
		ProjectID:   event.ProjectID,
		EventType:   string(event.Type),
		AppID:       event.AppID,
		Environment: event.Environment,
		Payload:     payloadBytes,
	}

	if err != nil {
		record.Status = "failed"
		record.Error = err.Error()
		log.Printf("[notify] failed to send %s to channel %s (%s): %v", event.Type, ch.Name, ch.Type, err)
	} else {
		record.Status = "sent"
	}

	if dbErr := n.db.InsertNotificationHistory(record); dbErr != nil {
		log.Printf("[notify] failed to record history: %v", dbErr)
	}
}

func (n *Notifier) dispatch(ch db.NotificationChannel, event NotificationEvent) error {
	switch ch.Type {
	case "slack":
		return n.sendSlack(ch, event)
	case "discord":
		return n.sendDiscord(ch, event)
	case "google_chat":
		return n.sendGoogleChat(ch, event)
	case "webhook":
		return n.sendWebhook(ch, event)
	case "email":
		return n.sendEmail(ch, event)
	default:
		return fmt.Errorf("unknown channel type: %s", ch.Type)
	}
}

// --- Slack ---

type slackConfig struct {
	WebhookURL string `json:"webhookUrl"`
}

func (n *Notifier) sendSlack(ch db.NotificationChannel, event NotificationEvent) error {
	var cfg slackConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid slack config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("slack webhookUrl is empty")
	}

	color := "#36a64f" // green
	if strings.Contains(string(event.Type), "failed") {
		color = "#dc3545"
	} else if strings.Contains(string(event.Type), "deleted") {
		color = "#6c757d"
	}

	fields := []map[string]interface{}{
		{"title": "Project", "value": event.ProjectID, "short": true},
		{"title": "Event", "value": string(event.Type), "short": true},
	}
	if event.AppID != "" {
		fields = append(fields, map[string]interface{}{"title": "App", "value": event.AppID, "short": true})
	}
	if event.Environment != "" {
		fields = append(fields, map[string]interface{}{"title": "Environment", "value": event.Environment, "short": true})
	}
	if event.Image != "" {
		fields = append(fields, map[string]interface{}{"title": "Image", "value": event.Image, "short": false})
	}
	if event.TriggeredBy != "" {
		fields = append(fields, map[string]interface{}{"title": "Triggered By", "value": event.TriggeredBy, "short": true})
	}

	payload := map[string]interface{}{
		"attachments": []map[string]interface{}{
			{
				"color":   color,
				"pretext": fmt.Sprintf("*Vesta — %s*", event.Type),
				"text":    event.Message,
				"fields":  fields,
				"ts":      event.Timestamp.Unix(),
			},
		},
	}

	return n.postJSON(cfg.WebhookURL, payload)
}

// --- Discord ---

type discordConfig struct {
	WebhookURL string `json:"webhookUrl"`
}

func (n *Notifier) sendDiscord(ch db.NotificationChannel, event NotificationEvent) error {
	var cfg discordConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid discord config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("discord webhookUrl is empty")
	}

	color := 0x36a64f
	if strings.Contains(string(event.Type), "failed") {
		color = 0xdc3545
	} else if strings.Contains(string(event.Type), "deleted") {
		color = 0x6c757d
	}

	fields := []map[string]interface{}{
		{"name": "Project", "value": event.ProjectID, "inline": true},
		{"name": "Event", "value": string(event.Type), "inline": true},
	}
	if event.AppID != "" {
		fields = append(fields, map[string]interface{}{"name": "App", "value": event.AppID, "inline": true})
	}
	if event.Environment != "" {
		fields = append(fields, map[string]interface{}{"name": "Environment", "value": event.Environment, "inline": true})
	}
	if event.Image != "" {
		fields = append(fields, map[string]interface{}{"name": "Image", "value": event.Image, "inline": false})
	}
	if event.TriggeredBy != "" {
		fields = append(fields, map[string]interface{}{"name": "Triggered By", "value": event.TriggeredBy, "inline": true})
	}

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("Vesta — %s", event.Type),
				"description": event.Message,
				"color":       color,
				"fields":      fields,
				"timestamp":   event.Timestamp.Format(time.RFC3339),
			},
		},
	}

	return n.postJSON(cfg.WebhookURL, payload)
}

// --- Google Chat ---

type googleChatConfig struct {
	WebhookURL string `json:"webhookUrl"`
}

func (n *Notifier) sendGoogleChat(ch db.NotificationChannel, event NotificationEvent) error {
	var cfg googleChatConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid google_chat config: %w", err)
	}
	if cfg.WebhookURL == "" {
		return fmt.Errorf("google_chat webhookUrl is empty")
	}

	widgets := []map[string]interface{}{
		{"keyValue": map[string]interface{}{"topLabel": "Project", "content": event.ProjectID}},
		{"keyValue": map[string]interface{}{"topLabel": "Event", "content": string(event.Type)}},
	}
	if event.AppID != "" {
		widgets = append(widgets, map[string]interface{}{"keyValue": map[string]interface{}{"topLabel": "App", "content": event.AppID}})
	}
	if event.Environment != "" {
		widgets = append(widgets, map[string]interface{}{"keyValue": map[string]interface{}{"topLabel": "Environment", "content": event.Environment}})
	}
	if event.Image != "" {
		widgets = append(widgets, map[string]interface{}{"keyValue": map[string]interface{}{"topLabel": "Image", "content": event.Image}})
	}

	payload := map[string]interface{}{
		"cards": []map[string]interface{}{
			{
				"header": map[string]interface{}{
					"title":    fmt.Sprintf("Vesta — %s", event.Type),
					"subtitle": event.Message,
				},
				"sections": []map[string]interface{}{
					{"widgets": widgets},
				},
			},
		},
	}

	return n.postJSON(cfg.WebhookURL, payload)
}

// --- Custom Webhook ---

type webhookConfig struct {
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

func (n *Notifier) sendWebhook(ch db.NotificationChannel, event NotificationEvent) error {
	var cfg webhookConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid webhook config: %w", err)
	}
	if cfg.URL == "" {
		return fmt.Errorf("webhook url is empty")
	}

	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Vesta-Webhook/1.0")

	if cfg.Secret != "" {
		mac := hmac.New(sha256.New, []byte(cfg.Secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Vesta-Signature", "sha256="+sig)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// --- Email ---

type emailConfig struct {
	SMTPHost string   `json:"smtpHost"`
	SMTPPort string   `json:"smtpPort"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

func (n *Notifier) sendEmail(ch db.NotificationChannel, event NotificationEvent) error {
	var cfg emailConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid email config: %w", err)
	}
	if cfg.SMTPHost == "" || len(cfg.To) == 0 {
		return fmt.Errorf("email config missing smtpHost or recipients")
	}

	subject := fmt.Sprintf("[Vesta] %s — %s", event.Type, event.ProjectID)
	if event.AppID != "" {
		subject = fmt.Sprintf("[Vesta] %s — %s/%s", event.Type, event.ProjectID, event.AppID)
	}

	body := fmt.Sprintf("Event: %s\nProject: %s\n", event.Type, event.ProjectID)
	if event.AppID != "" {
		body += fmt.Sprintf("App: %s\n", event.AppID)
	}
	if event.Environment != "" {
		body += fmt.Sprintf("Environment: %s\n", event.Environment)
	}
	if event.Image != "" {
		body += fmt.Sprintf("Image: %s\n", event.Image)
	}
	if event.TriggeredBy != "" {
		body += fmt.Sprintf("Triggered By: %s\n", event.TriggeredBy)
	}
	body += fmt.Sprintf("\n%s\n\nTimestamp: %s\n", event.Message, event.Timestamp.Format(time.RFC3339))

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		cfg.From, strings.Join(cfg.To, ", "), subject, body)

	addr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	}

	return smtp.SendMail(addr, auth, cfg.From, cfg.To, []byte(msg))
}

// SendPasswordResetEmail sends a reset email using the SMTP config from any email notification channel.
func (n *Notifier) SendPasswordResetEmail(toEmail, resetToken string) error {
	ch, err := n.db.GetAnyEmailChannel()
	if err != nil {
		return fmt.Errorf("failed to look up email channel: %w", err)
	}
	if ch == nil {
		return fmt.Errorf("no email notification channel configured")
	}

	var cfg emailConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid email config: %w", err)
	}
	if cfg.SMTPHost == "" {
		return fmt.Errorf("email channel SMTP host is empty")
	}

	subject := "[Vesta] Password Reset"
	body := fmt.Sprintf(
		"You requested a password reset for your Vesta account.\n\n"+
			"Use the following code to reset your password:\n\n"+
			"    %s\n\n"+
			"This code expires in 1 hour. If you did not request this, ignore this email.\n",
		resetToken,
	)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		cfg.From, toEmail, subject, body)

	addr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	}

	return smtp.SendMail(addr, auth, cfg.From, []string{toEmail}, []byte(msg))
}

// SendInviteEmail sends a welcome/invite email to a newly created user.
// It silently returns nil if no email channel is configured.
func (n *Notifier) SendInviteEmail(toEmail, username, role, loginURL string) error {
	ch, err := n.db.GetAnyEmailChannel()
	if err != nil || ch == nil {
		return nil // no email channel — skip silently
	}

	var cfg emailConfig
	if err := json.Unmarshal(ch.Config, &cfg); err != nil {
		return fmt.Errorf("invalid email config: %w", err)
	}
	if cfg.SMTPHost == "" {
		return nil
	}

	subject := "[Vesta] You've been invited"

	accentColor := "#E8590C"
	bgColor := "#0C0C0E"
	cardBg := "#16161A"
	borderColor := "#2A2A30"
	textPrimary := "#E4E4E7"
	textMuted := "#8B8B94"

	buttonBlock := ""
	if loginURL != "" {
		buttonBlock = fmt.Sprintf(`<tr><td style="padding:32px 0 0">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
<td style="background:%s;border-radius:6px;padding:14px 32px">
<a href="%s" style="color:#fff;font-family:'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;font-weight:600;letter-spacing:0.5px;text-decoration:none;text-transform:uppercase" target="_blank">Accept Invite</a>
</td></tr></table>
</td></tr>`, accentColor, loginURL)
	}

	body := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0"></head>
<body style="margin:0;padding:0;background:%s;-webkit-font-smoothing:antialiased">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:%s">
<tr><td align="center" style="padding:48px 24px">
<table role="presentation" width="520" cellpadding="0" cellspacing="0" border="0" style="max-width:520px;width:100%%">

<!-- Logo -->
<tr><td style="padding:0 0 40px">
<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
<td style="background:%s;border-radius:8px;width:40px;height:40px;text-align:center;vertical-align:middle;font-family:'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;font-size:18px;font-weight:700;color:#fff">V</td>
<td style="padding-left:12px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:17px;font-weight:600;color:%s;letter-spacing:-0.2px">Vesta</td>
</tr></table>
</td></tr>

<!-- Card -->
<tr><td style="background:%s;border:1px solid %s;border-radius:12px;padding:40px">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0">

<tr><td style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:22px;font-weight:700;color:%s;line-height:1.3;padding:0 0 8px">You're in.</td></tr>
<tr><td style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:15px;color:%s;line-height:1.6;padding:0 0 28px">You've been invited to join <strong style="color:%s">Vesta</strong> as a team member. Your account is ready.</td></tr>

<!-- Details -->
<tr><td style="background:%s;border:1px solid %s;border-radius:8px;padding:20px">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0">
<tr>
<td style="font-family:'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;font-size:11px;font-weight:600;color:%s;text-transform:uppercase;letter-spacing:1px;padding:0 0 12px" colspan="2">Account Details</td>
</tr>
<tr>
<td style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:13px;color:%s;padding:6px 0;width:90px">Username</td>
<td style="font-family:'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;color:%s;padding:6px 0">%s</td>
</tr>
<tr><td colspan="2" style="border-top:1px solid %s;font-size:1px;line-height:1px;padding:0">&nbsp;</td></tr>
<tr>
<td style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:13px;color:%s;padding:6px 0;width:90px">Email</td>
<td style="font-family:'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;color:%s;padding:6px 0">%s</td>
</tr>
<tr><td colspan="2" style="border-top:1px solid %s;font-size:1px;line-height:1px;padding:0">&nbsp;</td></tr>
<tr>
<td style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:13px;color:%s;padding:6px 0;width:90px">Role</td>
<td style="font-family:'SF Mono',SFMono-Regular,Menlo,Consolas,monospace;font-size:13px;color:%s;padding:6px 0"><span style="background:%s;color:#fff;padding:2px 8px;border-radius:4px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px">%s</span></td>
</tr>
</table>
</td></tr>

%s

<tr><td style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:13px;color:%s;line-height:1.6;padding:28px 0 0">Log in with the credentials provided by your administrator.</td></tr>

</table>
</td></tr>

<!-- Footer -->
<tr><td style="padding:32px 0 0;text-align:center">
<p style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;font-size:12px;color:%s;margin:0">Vesta &mdash; Self-hosted PaaS for Kubernetes</p>
</td></tr>

</table>
</td></tr>
</table>
</body>
</html>`,
		bgColor, bgColor,
		accentColor, textPrimary,
		cardBg, borderColor,
		textPrimary, textMuted, textPrimary,
		bgColor, borderColor,
		accentColor,
		textMuted, textPrimary, username,
		borderColor,
		textMuted, textPrimary, toEmail,
		borderColor,
		textMuted, textPrimary, accentColor, role,
		buttonBlock,
		textMuted,
		textMuted,
	)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		cfg.From, toEmail, subject, body)

	addr := fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort)
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
	}

	return smtp.SendMail(addr, auth, cfg.From, []string{toEmail}, []byte(msg))
}

// --- Helpers ---

func (n *Notifier) postJSON(url string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := n.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s returned status %d", url, resp.StatusCode)
	}
	return nil
}

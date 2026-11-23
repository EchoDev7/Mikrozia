package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/echodev7/mikrozia/v3/internal/web/locale"
	"github.com/echodev7/mikrozia/v3/internal/web/service"
)

const (
	defaultDiscordBaseURL = "https://discord.com/api/v10"
	discordUserAgent      = "DiscordBot (https://github.com/EchoDev7/Mikrozia, 3.x)"

	ColorGreen  = 0x2ECC71
	ColorRed    = 0xE74C3C
	ColorOrange = 0xF39C12
	ColorBlue   = 0x3498DB
)

// FileAttachment represents a file attachment to be uploaded with a Discord message.
// Exactly one of Data or Reader must be provided. When Reader is set, the caller
// retains ownership of closing any underlying file or ReadCloser.
type FileAttachment struct {
	Filename string
	Data     []byte
	Reader   io.Reader
}

// MessagePayload represents the Discord create message payload.
type MessagePayload struct {
	Content string  `json:"content,omitempty"`
	Embeds  []Embed `json:"embeds,omitempty"`
}

// Embed represents a Discord embed object.
type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

// EmbedField represents a field in a Discord embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// EmbedFooter represents a footer in a Discord embed.
type EmbedFooter struct {
	Text string `json:"text"`
}

// DiscordService manages communication with the Discord API.
type DiscordService struct {
	settingService service.SettingService
	httpClient     *http.Client
	baseURL        string
}

// NewDiscordService creates a new DiscordService.
func NewDiscordService(settingService service.SettingService) *DiscordService {
	return &DiscordService{
		settingService: settingService,
		baseURL:        defaultDiscordBaseURL,
	}
}

// SetHTTPClient sets a custom HTTP client (useful for unit testing).
func (s *DiscordService) SetHTTPClient(client *http.Client) {
	s.httpClient = client
}

// SetBaseURL sets a custom base URL for the Discord API (useful for testing with httptest).
func (s *DiscordService) SetBaseURL(url string) {
	s.baseURL = strings.TrimRight(url, "/")
}

func (s *DiscordService) getClient() *http.Client {
	if s.httpClient != nil {
		return s.httpClient
	}
	return s.settingService.NewProxiedHTTPClient(10 * time.Second)
}

func (s *DiscordService) getBaseURL() string {
	if s.baseURL != "" {
		return s.baseURL
	}
	return defaultDiscordBaseURL
}

func (s *DiscordService) authCredentials() (token string, channelID string, err error) {
	rawToken, err := s.settingService.GetDiscordBotToken()
	if err != nil || strings.TrimSpace(rawToken) == "" {
		return "", "", errors.New("discord bot token is not configured")
	}
	rawChannel, err := s.settingService.GetDiscordChannelId()
	if err != nil || strings.TrimSpace(rawChannel) == "" {
		return "", "", errors.New("discord channel id is not configured")
	}

	cleanToken := strings.TrimSpace(rawToken)
	cleanToken = strings.TrimPrefix(cleanToken, "Bot ")
	cleanToken = strings.TrimSpace(cleanToken)
	if cleanToken == "" {
		return "", "", errors.New("discord bot token is not configured")
	}
	return cleanToken, strings.TrimSpace(rawChannel), nil
}

// discordCharLen counts what Discord's caps count: a rune outside the BMP is
// two units there, so a rune count understates an emoji-bearing field.
func discordCharLen(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// RateLimitedError is a 429, carrying the wait Discord asks for so a caller
// sending several messages can back off instead of losing the rest of them.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("discord rate limited (429): retry after %s", e.RetryAfter)
}

func parseDiscordResponse(resp *http.Response) error {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyStr := string(respBody)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("discord bad request (400): %s", bodyStr)
	case http.StatusUnauthorized:
		return errors.New("discord unauthorized (401): invalid bot token")
	case http.StatusForbidden:
		return errors.New("discord forbidden (403): bot lacks permissions for channel")
	case http.StatusNotFound:
		return errors.New("discord not found (404): channel not found")
	case http.StatusTooManyRequests:
		var limited struct {
			RetryAfter float64 `json:"retry_after"`
		}
		_ = json.Unmarshal(respBody, &limited)
		return &RateLimitedError{RetryAfter: time.Duration(limited.RetryAfter * float64(time.Second))}
	default:
		return fmt.Errorf("discord API error (%d): %s", resp.StatusCode, bodyStr)
	}
}

// SendMessage sends a Discord message payload to the configured channel.
func (s *DiscordService) SendMessage(ctx context.Context, payload MessagePayload) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cleanToken, channelID, err := s.authCredentials()
	if err != nil {
		return err
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	endpoint := fmt.Sprintf("%s/channels/%s/messages", s.getBaseURL(), channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create discord request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+cleanToken)
	req.Header.Set("User-Agent", discordUserAgent)

	resp, err := s.getClient().Do(req)
	if err != nil {
		return fmt.Errorf("discord request failed: %w", err)
	}
	defer resp.Body.Close()

	return parseDiscordResponse(resp)
}

// SendMessageWithFiles sends a Discord message payload with optional file attachments
// using streaming multipart/form-data over an io.Pipe without buffering attachments in RAM.
func (s *DiscordService) SendMessageWithFiles(ctx context.Context, payload MessagePayload, files ...FileAttachment) error {
	if len(files) == 0 {
		return s.SendMessage(ctx, payload)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cleanToken, channelID, err := s.authCredentials()
	if err != nil {
		return err
	}

	for i, file := range files {
		if strings.TrimSpace(file.Filename) == "" {
			return fmt.Errorf("discord attachment %d filename is empty", i)
		}
		if file.Reader != nil && file.Data != nil {
			return fmt.Errorf("discord attachment %d (%s) cannot specify both Data and Reader", i, file.Filename)
		}
		if file.Reader == nil && file.Data == nil {
			return fmt.Errorf("discord attachment %d (%s) source is nil", i, file.Filename)
		}
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	contentType := writer.FormDataContentType()

	writeErrCh := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() {
			if writeErr != nil {
				_ = pw.CloseWithError(writeErr)
			} else {
				_ = pw.Close()
			}
			writeErrCh <- writeErr
		}()

		if err := writer.WriteField("payload_json", string(payloadBytes)); err != nil {
			writeErr = fmt.Errorf("write payload_json: %w", err)
			return
		}
		for i, file := range files {
			part, err := writer.CreateFormFile(fmt.Sprintf("files[%d]", i), file.Filename)
			if err != nil {
				writeErr = fmt.Errorf("create form file part %d: %w", i, err)
				return
			}
			src := file.Reader
			if src == nil {
				src = bytes.NewReader(file.Data)
			}
			if _, err := io.Copy(part, src); err != nil {
				writeErr = fmt.Errorf("write form file part %d: %w", i, err)
				return
			}
		}
		if err := writer.Close(); err != nil {
			writeErr = fmt.Errorf("close multipart writer: %w", err)
			return
		}
	}()

	endpoint := fmt.Sprintf("%s/channels/%s/messages", s.getBaseURL(), channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		if writeErr := <-writeErrCh; writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
			return fmt.Errorf("create discord request: %w (stream: %w)", err, writeErr)
		}
		return fmt.Errorf("create discord request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bot "+cleanToken)
	req.Header.Set("User-Agent", discordUserAgent)

	resp, reqErr := s.getClient().Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	_ = pr.Close()
	writeErr := <-writeErrCh

	if reqErr != nil {
		if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
			return fmt.Errorf("discord upload stream failed: %w (request: %w)", writeErr, reqErr)
		}
		return fmt.Errorf("discord request failed: %w", reqErr)
	}
	if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
		return writeErr
	}

	return parseDiscordResponse(resp)
}

// SendEmbed is a helper to send an embed payload.
func (s *DiscordService) SendEmbed(ctx context.Context, embed Embed) error {
	return s.SendMessage(ctx, MessagePayload{
		Embeds: []Embed{embed},
	})
}

// translator renders messages in the configured Discord bot language, read once per message.
func translator(settingService service.SettingService) func(key string, params ...string) string {
	lang, err := settingService.GetDiscordLang()
	if err != nil || lang == "" {
		lang = "en-US"
	}
	return func(key string, params ...string) string {
		return locale.I18nForLang(lang, key, params...)
	}
}

// SendTest sends a test embed to verify Discord bot configuration.
func (s *DiscordService) SendTest(ctx context.Context) error {
	tr := translator(s.settingService)
	now := time.Now().UTC().Format(time.RFC3339)
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "mikrozia"
	}
	embed := Embed{
		Title:       tr("discord.test.title"),
		Description: tr("discord.test.body"),
		Color:       ColorGreen,
		Timestamp:   now,
		Fields: []EmbedField{
			{Name: tr("host"), Value: hostname, Inline: true},
		},
		Footer: &EmbedFooter{
			Text: tr("discord.footer"),
		},
	}
	return s.SendEmbed(ctx, embed)
}

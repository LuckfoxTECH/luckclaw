package channels

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"
	"luckclaw/internal/providers/transcription"
)

func telegramTrunc(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

type mediaGroupBuf struct {
	senderID           string
	chatID             string
	contents           []string
	media              []string
	metadata           map[string]any
	sessionKeyOverride string
}

type TelegramChannel struct {
	cfg         config.TelegramConfig
	bus         *bus.MessageBus
	client      *http.Client
	offset      int64
	groqAPIKey  string
	transcriber interface{ Transcribe(string) (string, error) }
	botID       int64  // from getMe, for mention detection
	botUsername string // from getMe, for @mention in groups

	mediaGroupBufs   map[string]*mediaGroupBuf
	mediaGroupTimers map[string]*time.Timer
	mediaGroupMu     sync.Mutex

	recorder PlaceholderRecorder
	globalUX *config.UXConfig
}

func NewTelegram(cfg config.TelegramConfig, b *bus.MessageBus, groqAPIKey string, webCfg config.WebToolsConfig) *TelegramChannel {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	transport.Proxy = webCfg.ProxyFunc() // system proxy by default, tools.web when set

	client := &http.Client{Timeout: 70 * time.Second, Transport: transport}
	return &TelegramChannel{
		cfg:              cfg,
		bus:              b,
		groqAPIKey:       groqAPIKey,
		transcriber:      &transcription.GroqTranscription{APIKey: groqAPIKey, HTTP: client},
		client:           client,
		mediaGroupBufs:   make(map[string]*mediaGroupBuf),
		mediaGroupTimers: make(map[string]*time.Timer),
	}
}

func (c *TelegramChannel) Name() string { return "telegram" }

// SetPlaceholderRecorder injects the recorder for Typing/Placeholder UX.
func (c *TelegramChannel) SetPlaceholderRecorder(r PlaceholderRecorder) {
	c.recorder = r
}

// SetGlobalUX implements GlobalUXSetter.
func (c *TelegramChannel) SetGlobalUX(ux *config.UXConfig) {
	c.globalUX = ux
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("telegram token is not configured")
	}
	if err := c.fetchBotInfo(ctx); err != nil {
		return fmt.Errorf("telegram getMe failed: %w", err)
	}
	log.Printf("[telegram] starting long polling, allowFrom=%v", c.cfg.AllowFrom)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[telegram] context cancelled, stopping")
			return nil
		default:
		}
		if err := c.pollOnce(ctx); err != nil {
			log.Printf("[telegram] poll failed: %v, retrying in 2s", err)
			time.Sleep(2 * time.Second)
		}
	}
}

func (c *TelegramChannel) Stop(ctx context.Context) error {
	c.client.CloseIdleConnections()
	return nil
}

func (c *TelegramChannel) pollOnce(ctx context.Context) error {
	q := url.Values{}
	q.Set("timeout", "60")
	q.Set("allowed_updates", `["message"]`)
	if c.offset > 0 {
		q.Set("offset", strconv.FormatInt(c.offset, 10))
	}

	u := c.apiURL("getUpdates") + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var parsed struct {
		OK     bool `json:"ok"`
		Result []struct {
			UpdateID int64 `json:"update_id"`
			Message  struct {
				MessageID       int64  `json:"message_id"`
				MessageThreadID *int64 `json:"message_thread_id,omitempty"`
				From            struct {
					ID int64 `json:"id"`
				} `json:"from"`
				Chat struct {
					ID   int64  `json:"id"`
					Type string `json:"type"`
				} `json:"chat"`
				Text    string `json:"text"`
				Caption string `json:"caption"`
				Photo   []struct {
					FileID string `json:"file_id"`
				} `json:"photo"`
				Voice struct {
					FileID string `json:"file_id"`
				} `json:"voice"`
				Audio struct {
					FileID string `json:"file_id"`
				} `json:"audio"`
				MediaGroupID   string `json:"media_group_id,omitempty"`
				ReplyToMessage *struct {
					From struct {
						ID int64 `json:"id"`
					} `json:"from"`
				} `json:"reply_to_message,omitempty"`
				Entities []struct {
					Type   string `json:"type"`
					Offset int    `json:"offset"`
					Length int    `json:"length"`
				} `json:"entities,omitempty"`
			} `json:"message"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		log.Printf("[telegram] getUpdates parse error: %v", err)
		return err
	}
	if !parsed.OK {
		log.Printf("[telegram] getUpdates api error: %s", string(b))
		return fmt.Errorf("telegram api error")
	}
	for _, upd := range parsed.Result {
		c.offset = upd.UpdateID + 1
		text := strings.TrimSpace(upd.Message.Text)
		if text == "" {
			text = strings.TrimSpace(upd.Message.Caption)
		}
		hasPhoto := len(upd.Message.Photo) > 0
		hasVoice := upd.Message.Voice.FileID != ""
		hasAudio := upd.Message.Audio.FileID != ""

		// Allow photo-only, voice-only, or audio-only messages
		if text == "" && !hasPhoto && !hasVoice && !hasAudio {
			continue
		}
		if text == "" && hasPhoto {
			text = "[Image]"
		}
		if text == "" && (hasVoice || hasAudio) {
			fileID := upd.Message.Voice.FileID
			if fileID == "" {
				fileID = upd.Message.Audio.FileID
			}
			mediaType := "voice"
			if hasAudio {
				mediaType = "audio"
			}
			if c.transcriber != nil {
				if path, err := c.downloadFileToTemp(ctx, fileID, mediaType); err == nil {
					if tr, err := c.transcriber.Transcribe(path); err == nil && tr != "" {
						text = "[transcription: " + tr + "]"
					} else {
						text = "[" + mediaType + "]"
					}
					_ = os.Remove(path)
				} else {
					text = "[" + mediaType + "]"
				}
			} else {
				text = "[" + mediaType + "]"
			}
		}
		senderID := strconv.FormatInt(upd.Message.From.ID, 10)
		if !IsAllowed(c.cfg.AllowFrom, senderID) {
			log.Printf("[telegram] message from %s not in allowFrom (config=%v), ignored", senderID, c.cfg.AllowFrom)
			continue
		}
		chatID := strconv.FormatInt(upd.Message.Chat.ID, 10)

		// Group trigger: in groups/supergroups, apply mention_only / prefixes
		chatType := strings.ToLower(strings.TrimSpace(upd.Message.Chat.Type))
		if chatType == "group" || chatType == "supergroup" {
			replyToID := int64(0)
			if upd.Message.ReplyToMessage != nil {
				replyToID = upd.Message.ReplyToMessage.From.ID
			}
			rawText := upd.Message.Text
			if rawText == "" {
				rawText = upd.Message.Caption
			}
			mentioned := c.isMentioned(replyToID, upd.Message.Entities, rawText)
			if respond, trimmed := ShouldRespondInGroup(c.cfg.GroupTrigger, mentioned, text); !respond {
				continue
			} else {
				text = trimmed
			}
			if text == "" && !hasPhoto && !hasVoice && !hasAudio {
				continue
			}
		}

		// Build session key with topic support for supergroups
		metadata := map[string]any{
			"message_id": upd.Message.MessageID,
			"chat_type":  upd.Message.Chat.Type,
		}
		sessionKeyOverride := ""
		if upd.Message.MessageThreadID != nil {
			threadID := *upd.Message.MessageThreadID
			metadata["thread_id"] = threadID
			sessionKeyOverride = fmt.Sprintf("telegram:%s:topic:%d", chatID, threadID)
		}

		c.maybeStartTypingAndPlaceholder(ctx, chatID, metadata)

		var media []string
		if hasPhoto {
			// Take largest photo (last in array)
			fileID := upd.Message.Photo[len(upd.Message.Photo)-1].FileID
			if dataURI := c.downloadPhotoAsDataURI(ctx, fileID); dataURI != "" {
				media = append(media, dataURI)
			}
		}

		// Media group: buffer and flush after 600ms
		if mgID := strings.TrimSpace(upd.Message.MediaGroupID); mgID != "" {
			key := chatID + ":" + mgID
			c.mediaGroupMu.Lock()
			if c.mediaGroupBufs[key] == nil {
				c.mediaGroupBufs[key] = &mediaGroupBuf{
					senderID:           senderID,
					chatID:             chatID,
					metadata:           metadata,
					sessionKeyOverride: sessionKeyOverride,
				}
				c.maybeStartTypingAndPlaceholder(ctx, chatID, metadata)
			}
			buf := c.mediaGroupBufs[key]
			if text != "" && text != "[Image]" {
				buf.contents = append(buf.contents, text)
			}
			buf.media = append(buf.media, media...)
			if c.mediaGroupTimers[key] == nil {
				t := time.AfterFunc(600*time.Millisecond, func() {
					c.flushMediaGroup(key)
				})
				c.mediaGroupTimers[key] = t
			}
			c.mediaGroupMu.Unlock()
			continue
		}

		msg := bus.InboundMessage{
			Channel:  "telegram",
			SenderID: senderID,
			ChatID:   chatID,
			Content:  text,
			Media:    media,
			Metadata: metadata,
		}
		if sessionKeyOverride != "" {
			msg.Metadata["session_key_override"] = sessionKeyOverride
		}
		log.Printf("[telegram] inbound: sender=%s chat=%s text=%q", senderID, chatID, telegramTrunc(text, 80))
		_ = c.bus.PublishInbound(ctx, msg)
	}
	return nil
}

func (c *TelegramChannel) flushMediaGroup(key string) {
	c.mediaGroupMu.Lock()
	buf := c.mediaGroupBufs[key]
	delete(c.mediaGroupBufs, key)
	if t := c.mediaGroupTimers[key]; t != nil {
		t.Stop()
		delete(c.mediaGroupTimers, key)
	}
	c.mediaGroupMu.Unlock()
	if buf == nil {
		return
	}
	content := strings.Join(buf.contents, "\n")
	if content == "" && len(buf.media) > 0 {
		content = "[Images]"
	} else if content == "" {
		content = "[empty message]"
	}
	msg := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: buf.senderID,
		ChatID:   buf.chatID,
		Content:  content,
		Media:    buf.media,
		Metadata: buf.metadata,
	}
	if buf.sessionKeyOverride != "" {
		msg.Metadata["session_key_override"] = buf.sessionKeyOverride
	}
	log.Printf("[telegram] inbound: sender=%s chat=%s mediaGroup text=%q", buf.senderID, buf.chatID, telegramTrunc(content, 80))
	_ = c.bus.PublishInbound(context.Background(), msg)
}

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("telegram token is not configured")
	}

	// Send media attachments first (file paths)
	for _, path := range msg.Media {
		if err := c.sendMediaFile(ctx, msg.ChatID, path, msg.Metadata); err != nil {
			log.Printf("[telegram] sendMediaFile failed: %v", err)
		}
	}

	content := msg.Content
	if content == "" {
		return nil
	}
	chunks := splitMessage(content, 4000)
	for _, chunk := range chunks {
		form := url.Values{}
		form.Set("chat_id", msg.ChatID)
		form.Set("text", markdownToTelegramHTML(chunk))
		form.Set("parse_mode", "HTML")

		// Reply to message if configured
		if c.cfg.ReplyToMessage {
			if msgID, ok := msg.Metadata["message_id"]; ok {
				form.Set("reply_to_message_id", fmt.Sprintf("%v", msgID))
			}
		}

		// Thread/topic support
		if threadID, ok := msg.Metadata["thread_id"]; ok {
			form.Set("message_thread_id", fmt.Sprintf("%v", threadID))
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendMessage"), strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := c.client.Do(req)
		if err != nil {
			return err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			log.Printf("[telegram] sendMessage HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		// If HTML parse fails, retry as plain text
		if resp.StatusCode != 200 {
			var tgErr struct {
				OK          bool   `json:"ok"`
				Description string `json:"description"`
			}
			if json.Unmarshal(respBody, &tgErr) == nil && strings.Contains(tgErr.Description, "parse") {
				form.Set("text", chunk)
				form.Del("parse_mode")
				req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendMessage"), strings.NewReader(form.Encode()))
				req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp2, err := c.client.Do(req2)
				if err != nil {
					return err
				}
				_, _ = io.ReadAll(resp2.Body)
				resp2.Body.Close()
			}
		}
	}
	return nil
}

// SendAndTrack sends a message and returns the Telegram message_id for later editing.
func (c *TelegramChannel) SendAndTrack(ctx context.Context, msg bus.OutboundMessage) (string, error) {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return "", fmt.Errorf("telegram token is not configured")
	}

	form := url.Values{}
	form.Set("chat_id", msg.ChatID)
	content := msg.Content
	if len(content) > 4000 {
		content = content[:4000]
	}
	form.Set("text", content)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendMessage"), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var parsed struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if !parsed.OK {
		return "", fmt.Errorf("telegram sendMessage failed")
	}
	return strconv.FormatInt(parsed.Result.MessageID, 10), nil
}

// EditMessage edits a previously sent Telegram message.
func (c *TelegramChannel) EditMessage(ctx context.Context, chatID, messageID, newContent string) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("telegram token is not configured")
	}
	if len(newContent) > 4000 {
		newContent = newContent[:4000]
	}
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("message_id", messageID)
	form.Set("text", markdownToTelegramHTML(newContent))
	form.Set("parse_mode", "HTML")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("editMessageText"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return err
	}
	if !parsed.OK {
		return fmt.Errorf("telegram editMessageText failed")
	}
	return nil
}

func (c *TelegramChannel) apiURL(method string) string {
	return "https://api.telegram.org/bot" + c.cfg.Token + "/" + method
}

func (c *TelegramChannel) fetchBotInfo(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("getMe"), nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("getMe not ok")
	}
	c.botID = out.Result.ID
	c.botUsername = strings.ToLower(strings.TrimSpace(out.Result.Username))
	return nil
}

// isMentioned returns true if the message mentions the bot (reply-to-bot or @username).
func (c *TelegramChannel) isMentioned(replyToUserID int64, entities []struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}, text string) bool {
	if replyToUserID != 0 && replyToUserID == c.botID {
		return true
	}
	if c.botUsername == "" {
		return false
	}
	mention := "@" + c.botUsername
	for _, e := range entities {
		if (e.Type == "mention" || e.Type == "text_mention") && e.Offset >= 0 && e.Offset+e.Length <= len(text) {
			seg := strings.ToLower(strings.TrimSpace(text[e.Offset : e.Offset+e.Length]))
			if seg == mention || seg == c.botUsername {
				return true
			}
		}
	}
	return false
}

func (c *TelegramChannel) downloadFileToTemp(ctx context.Context, fileID, ext string) (string, error) {
	getURL := c.apiURL("getFile") + "?file_id=" + url.QueryEscape(fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var fileResp struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&fileResp) != nil || !fileResp.OK || fileResp.Result.FilePath == "" {
		return "", fmt.Errorf("getFile failed")
	}
	downloadURL := "https://api.telegram.org/file/bot" + c.cfg.Token + "/" + fileResp.Result.FilePath
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	resp2, err := c.client.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	f, err := os.CreateTemp("", "tg-*."+ext)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(resp2.Body, 25*1024*1024))
	_ = f.Close()
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (c *TelegramChannel) downloadPhotoAsDataURI(ctx context.Context, fileID string) string {
	// getFile
	getURL := c.apiURL("getFile") + "?file_id=" + url.QueryEscape(fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return ""
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var fileResp struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&fileResp) != nil || !fileResp.OK || fileResp.Result.FilePath == "" {
		return ""
	}
	// Download file
	downloadURL := "https://api.telegram.org/file/bot" + c.cfg.Token + "/" + fileResp.Result.FilePath
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	resp2, err := c.client.Do(req2)
	if err != nil {
		return ""
	}
	defer resp2.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp2.Body, 5*1024*1024))
	if err != nil || len(data) == 0 {
		return ""
	}
	mime := "image/jpeg"
	if strings.HasSuffix(strings.ToLower(fileResp.Result.FilePath), ".png") {
		mime = "image/png"
	} else if strings.HasSuffix(strings.ToLower(fileResp.Result.FilePath), ".gif") {
		mime = "image/gif"
	} else if strings.HasSuffix(strings.ToLower(fileResp.Result.FilePath), ".webp") {
		mime = "image/webp"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func (c *TelegramChannel) sendMediaFile(ctx context.Context, chatID, path string, metadata map[string]any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 10*1024*1024 {
		return fmt.Errorf("file too large")
	}
	ext := strings.ToLower(filepath.Ext(path))
	method := "sendDocument"
	field := "document"
	if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp" {
		method, field = "sendPhoto", "photo"
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		_ = mw.WriteField("chat_id", chatID)
		if metadata != nil {
			if threadID, ok := metadata["thread_id"]; ok {
				_ = mw.WriteField("message_thread_id", fmt.Sprintf("%v", threadID))
			}
		}
		fw, _ := mw.CreateFormFile(field, filepath.Base(path))
		_, _ = fw.Write(data)
		_ = mw.Close()
		_ = pw.Close()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL(method), pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram send media failed: %d", resp.StatusCode)
	}
	return nil
}

// maybeStartTypingAndPlaceholder triggers Typing and Placeholder before publishing inbound.
func (c *TelegramChannel) maybeStartTypingAndPlaceholder(ctx context.Context, chatID string, metadata map[string]any) {
	if c.recorder == nil {
		return
	}
	typingEnabled := c.cfg.Typing || c.cfg.SendProgress
	if c.globalUX != nil && c.globalUX.Typing {
		typingEnabled = true
	}
	if typingEnabled {
		if stop, err := c.StartTyping(ctx, chatID, metadata); err == nil {
			c.recorder.RecordTypingStop("telegram", chatID, stop)
		}
	}
	placeholderEnabled := c.cfg.Placeholder.Enabled
	if c.globalUX != nil && c.globalUX.Placeholder.Enabled {
		placeholderEnabled = true
	}
	if placeholderEnabled {
		if phID, err := c.SendPlaceholder(ctx, chatID, metadata); err == nil && phID != "" {
			c.recorder.RecordPlaceholder("telegram", chatID, phID)
		}
	}
}

// StartTyping implements TypingCapable. Sends typing action and repeats every 4s (Telegram expires ~5s).
func (c *TelegramChannel) StartTyping(ctx context.Context, chatID string, metadata map[string]any) (func(), error) {
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("action", "typing")
	if metadata != nil {
		if tid, ok := metadata["thread_id"]; ok {
			form.Set("message_thread_id", fmt.Sprintf("%v", tid))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendChatAction"), strings.NewReader(form.Encode()))
	if err != nil {
		return func() {}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return func() {}, err
	}
	resp.Body.Close()

	typingCtx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				f := url.Values{}
				f.Set("chat_id", chatID)
				f.Set("action", "typing")
				if metadata != nil {
					if tid, ok := metadata["thread_id"]; ok {
						f.Set("message_thread_id", fmt.Sprintf("%v", tid))
					}
				}
				r, _ := http.NewRequestWithContext(typingCtx, http.MethodPost, c.apiURL("sendChatAction"), strings.NewReader(f.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp2, err := c.client.Do(r)
				if err != nil {
					return
				}
				resp2.Body.Close()
			}
		}
	}()
	return cancel, nil
}

// SendPlaceholder implements PlaceholderCapable.
func (c *TelegramChannel) SendPlaceholder(ctx context.Context, chatID string, metadata map[string]any) (string, error) {
	text := c.cfg.Placeholder.Text
	if c.globalUX != nil && c.globalUX.Placeholder.Text != "" {
		text = c.globalUX.Placeholder.Text
	}
	if text == "" {
		text = "Thinking... 💭"
	}
	form := url.Values{}
	form.Set("chat_id", chatID)
	form.Set("text", text)
	form.Set("parse_mode", "HTML")
	if metadata != nil {
		if tid, ok := metadata["thread_id"]; ok {
			form.Set("message_thread_id", fmt.Sprintf("%v", tid))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("sendMessage"), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var parsed struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || !parsed.OK {
		return "", fmt.Errorf("telegram sendMessage failed")
	}
	return strconv.FormatInt(parsed.Result.MessageID, 10), nil
}

// markdownToTelegramHTML converts markdown to Telegram-safe HTML (parse_mode=HTML).
func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}
	// 1. Extract and protect code blocks
	codeBlockRe := regexp.MustCompile("(?s)```[\\w]*\\n?(.*?)```")
	codeBlocks := []string{}
	text = codeBlockRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := codeBlockRe.FindStringSubmatch(m)
		if len(sub) >= 2 {
			codeBlocks = append(codeBlocks, sub[1])
			return "\x00CB" + fmt.Sprintf("%d", len(codeBlocks)-1) + "\x00"
		}
		return m
	})

	// 1.5. Convert markdown tables to box-drawing
	lines := strings.Split(text, "\n")
	var rebuilt []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if regexp.MustCompile(`^\s*\|.+\|`).MatchString(line) {
			var tbl []string
			for i < len(lines) && regexp.MustCompile(`^\s*\|.+\|`).MatchString(lines[i]) {
				tbl = append(tbl, lines[i])
				i++
			}
			i--
			box := renderTableBox(tbl)
			if box != strings.Join(tbl, "\n") {
				codeBlocks = append(codeBlocks, box)
				rebuilt = append(rebuilt, "\x00CB"+fmt.Sprintf("%d", len(codeBlocks)-1)+"\x00")
			} else {
				rebuilt = append(rebuilt, tbl...)
			}
		} else {
			rebuilt = append(rebuilt, line)
		}
	}
	text = strings.Join(rebuilt, "\n")

	// 2. Extract and protect inline code
	inlineCodeRe := regexp.MustCompile("`([^`]+)`")
	inlineCodes := []string{}
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := inlineCodeRe.FindStringSubmatch(m)
		if len(sub) >= 2 {
			inlineCodes = append(inlineCodes, sub[1])
			return "\x00IC" + fmt.Sprintf("%d", len(inlineCodes)-1) + "\x00"
		}
		return m
	})

	// 3. Headers -> plain text
	text = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`).ReplaceAllString(text, "$1")
	// 4. Blockquotes -> plain text
	text = regexp.MustCompile(`(?m)^>\s*(.*)$`).ReplaceAllString(text, "$1")
	// 5. Escape HTML
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	// 6. Links
	text = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(text, "<a href=\"$2\">$1</a>")
	// 7. Bold
	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "<b>$1</b>")
	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "<b>$1</b>")
	// 8. Italic _text_ (simple match; word boundaries not supported in Go regexp)
	text = regexp.MustCompile(`_([^_\s]+)_`).ReplaceAllString(text, "<i>$1</i>")
	// 9. Strikethrough
	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "<s>$1</s>")
	// 10. Bullet lists
	text = regexp.MustCompile(`(?m)^[-*]\s+`).ReplaceAllString(text, "• ")
	// 11. Restore inline code
	for i, code := range inlineCodes {
		esc := strings.ReplaceAll(code, "&", "&amp;")
		esc = strings.ReplaceAll(esc, "<", "&lt;")
		esc = strings.ReplaceAll(esc, ">", "&gt;")
		text = strings.Replace(text, "\x00IC"+fmt.Sprintf("%d", i)+"\x00", "<code>"+esc+"</code>", 1)
	}
	// 12. Restore code blocks
	for i, code := range codeBlocks {
		esc := strings.ReplaceAll(code, "&", "&amp;")
		esc = strings.ReplaceAll(esc, "<", "&lt;")
		esc = strings.ReplaceAll(esc, ">", "&gt;")
		text = strings.Replace(text, "\x00CB"+fmt.Sprintf("%d", i)+"\x00", "<pre><code>"+esc+"</code></pre>", 1)
	}
	return text
}

func stripMd(s string) string {
	s = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile("`([^`]+)`").ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

func runeWidth(r rune) int {
	// CJK and other East Asian fullwidth chars count as 2
	if r >= 0x1100 && r <= 0x11FF {
		return 2 // Hangul
	}
	if r >= 0x2E80 && r <= 0x9FFF {
		return 2 // CJK
	}
	if r >= 0xA000 && r <= 0xA4CF {
		return 2 // Yi, etc
	}
	if r >= 0xAC00 && r <= 0xD7AF {
		return 2 // Hangul syllables
	}
	if r >= 0xF900 && r <= 0xFAFF {
		return 2 // CJK compat
	}
	if r >= 0xFF00 && r <= 0xFFEF {
		return 2 // Fullwidth forms
	}
	return 1
}

func stringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

func renderTableBox(tableLines []string) string {
	sepRe := regexp.MustCompile(`^:?-+:?$`)
	var rows [][]string
	hasSep := false
	for _, line := range tableLines {
		parts := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		cells := make([]string, len(parts))
		for i, p := range parts {
			cells[i] = stripMd(strings.TrimSpace(p))
		}
		allSep := true
		for _, c := range cells {
			if c != "" && !sepRe.MatchString(c) {
				allSep = false
				break
			}
		}
		if allSep && len(cells) > 0 {
			hasSep = true
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 || !hasSep {
		return strings.Join(tableLines, "\n")
	}
	ncols := 0
	for _, r := range rows {
		if len(r) > ncols {
			ncols = len(r)
		}
	}
	widths := make([]int, ncols)
	for c := 0; c < ncols; c++ {
		for _, r := range rows {
			if c < len(r) && stringWidth(r[c]) > widths[c] {
				widths[c] = stringWidth(r[c])
			}
		}
	}
	pad := func(cells []string) string {
		var parts []string
		for c, w := range widths {
			cell := ""
			if c < len(cells) {
				cell = cells[c]
			}
			diff := w - stringWidth(cell)
			if diff < 0 {
				diff = 0
			}
			parts = append(parts, cell+strings.Repeat(" ", diff))
		}
		return strings.Join(parts, "  ")
	}
	var hlineParts []string
	for _, w := range widths {
		hlineParts = append(hlineParts, strings.Repeat("─", w))
	}
	hline := strings.Join(hlineParts, "  ")
	var out []string
	out = append(out, pad(rows[0]))
	out = append(out, hline)
	for _, r := range rows[1:] {
		ext := make([]string, ncols)
		copy(ext, r)
		out = append(out, pad(ext))
	}
	return strings.Join(out, "\n")
}

// escapeMarkdownV2 escapes special characters for Telegram MarkdownV2 parse mode.
// Characters that need escaping: _ * [ ] ( ) ~ ` > # + - = | { } . !
func escapeMarkdownV2(s string) string {
	// Preserve code blocks and inline code
	var result strings.Builder
	i := 0
	for i < len(s) {
		// Check for code blocks ```
		if i+2 < len(s) && s[i:i+3] == "```" {
			end := strings.Index(s[i+3:], "```")
			if end >= 0 {
				result.WriteString(s[i : i+3+end+3])
				i = i + 3 + end + 3
				continue
			}
		}
		// Check for inline code `
		if s[i] == '`' {
			end := strings.Index(s[i+1:], "`")
			if end >= 0 {
				result.WriteString(s[i : i+1+end+1])
				i = i + 1 + end + 1
				continue
			}
		}
		// Escape special chars outside code blocks
		if strings.ContainsRune("_*[]()~>#+-=|{}.!", rune(s[i])) {
			result.WriteByte('\\')
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

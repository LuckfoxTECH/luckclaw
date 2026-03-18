package channels

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"luckclaw/internal/bus"
	"luckclaw/internal/config"
)

type placeholderEntry struct {
	id        string
	createdAt time.Time
}

type typingEntry struct {
	stop      func()
	createdAt time.Time
}

type Manager struct {
	cfg      config.Config
	bus      *bus.MessageBus
	channels map[string]Channel
	mu       sync.Mutex
	wg       sync.WaitGroup

	// progressMsgIDs tracks the platform message ID for the last
	// tool_progress message per chat, enabling in-place editing.
	progressMu     sync.Mutex
	progressMsgIDs map[string]string // key: "channel:chatID"

	// placeholders and typingStops for Typing/Placeholder UX
	placeholders sync.Map // "channel:chatID" → placeholderEntry
	typingStops  sync.Map // "channel:chatID" → typingEntry
}

func NewManager(cfg config.Config, b *bus.MessageBus) *Manager {
	m := &Manager{
		cfg:            cfg,
		bus:            b,
		channels:       map[string]Channel{},
		progressMsgIDs: map[string]string{},
	}
	m.init()
	return m
}

// RecordPlaceholder registers a placeholder message for later editing.
func (m *Manager) RecordPlaceholder(channel, chatID, placeholderID string) {
	key := channel + ":" + chatID
	m.placeholders.Store(key, placeholderEntry{id: placeholderID, createdAt: time.Now()})
}

// RecordTypingStop registers a typing stop function for later invocation.
func (m *Manager) RecordTypingStop(channel, chatID string, stop func()) {
	key := channel + ":" + chatID
	m.typingStops.Store(key, typingEntry{stop: stop, createdAt: time.Now()})
}

// SendPlaceholder sends a "Thinking…" placeholder for the given channel/chatID.
// Returns true if a placeholder was sent and recorded.
func (m *Manager) SendPlaceholder(ctx context.Context, channel, chatID string) bool {
	m.mu.Lock()
	ch, ok := m.channels[channel]
	m.mu.Unlock()
	if !ok {
		return false
	}
	pc, ok := ch.(PlaceholderCapable)
	if !ok {
		return false
	}
	phID, err := pc.SendPlaceholder(ctx, chatID, nil)
	if err != nil || phID == "" {
		return false
	}
	m.RecordPlaceholder(channel, chatID, phID)
	return true
}

// preSend stops typing, edits placeholder if present. Returns true if message
// was edited into placeholder (caller should skip Send for content-only messages).
func (m *Manager) preSend(ctx context.Context, name string, msg bus.OutboundMessage, ch Channel) bool {
	key := name + ":" + msg.ChatID

	// 1. Stop typing
	if v, loaded := m.typingStops.LoadAndDelete(key); loaded {
		if entry, ok := v.(typingEntry); ok {
			entry.stop()
		}
	}

	// 2. Try editing placeholder (only when no media; media needs separate Send)
	if len(msg.Media) == 0 {
		if v, loaded := m.placeholders.LoadAndDelete(key); loaded {
			if entry, ok := v.(placeholderEntry); ok && entry.id != "" {
				if editable, ok := ch.(EditableChannel); ok {
					if err := editable.EditMessage(ctx, msg.ChatID, entry.id, msg.Content); err == nil {
						return true
					}
				}
			}
		}
	}
	return false
}

func (m *Manager) init() {
	if m.cfg.Channels.Telegram.Enabled {
		var ch Channel = NewTelegram(m.cfg.Channels.Telegram, m.bus, m.cfg.Providers.Groq.APIKey, m.cfg.Tools.Web)
		if setter, ok := ch.(interface{ SetPlaceholderRecorder(PlaceholderRecorder) }); ok {
			setter.SetPlaceholderRecorder(m)
		}
		if uxSetter, ok := ch.(GlobalUXSetter); ok {
			uxSetter.SetGlobalUX((&m.cfg).UXPtr())
		}
		m.channels["telegram"] = ch
	}
	if m.cfg.Channels.Discord.Enabled {
		var ch Channel = NewDiscord(m.cfg.Channels.Discord, m.bus, m.cfg.Tools.Web)
		if setter, ok := ch.(interface{ SetPlaceholderRecorder(PlaceholderRecorder) }); ok {
			setter.SetPlaceholderRecorder(m)
		}
		if uxSetter, ok := ch.(GlobalUXSetter); ok {
			uxSetter.SetGlobalUX((&m.cfg).UXPtr())
		}
		m.channels["discord"] = ch
	}
	if m.cfg.Channels.Feishu.Enabled {
		m.channels["feishu"] = NewFeishu(m.cfg.Channels.Feishu, m.bus, m.cfg.Tools.Web)
	}
	if m.cfg.Channels.Slack.Enabled {
		m.channels["slack"] = NewSlack(m.cfg.Channels.Slack, m.bus)
	}
	if m.cfg.Channels.DingTalk.Enabled {
		m.channels["dingtalk"] = NewDingTalk(m.cfg.Channels.DingTalk, m.bus)
	}
	if m.cfg.Channels.QQ.Enabled {
		m.channels["qq"] = NewQQ(m.cfg.Channels.QQ, m.bus)
	}
	if m.cfg.Channels.WorkWeixin.Enabled {
		m.channels["workweixin"] = NewWorkWeixin(m.cfg.Channels.WorkWeixin, m.bus, m.cfg.Tools.Web)
	}
}

// RegisterChannel adds a channel (e.g. webui for WebSocket clients).
func (m *Manager) RegisterChannel(name string, ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if setter, ok := ch.(interface{ SetPlaceholderRecorder(PlaceholderRecorder) }); ok {
		setter.SetPlaceholderRecorder(m)
	}
	if uxSetter, ok := ch.(GlobalUXSetter); ok {
		uxSetter.SetGlobalUX((&m.cfg).UXPtr())
	}
	m.channels[name] = ch
}

// ValidateAllowFrom returns an error if any enabled channel has empty allowFrom
// (denies all). Prevents misconfiguration where no one can access the bot.
func (m *Manager) ValidateAllowFrom() error {
	c := m.cfg.Channels
	checks := []struct {
		name string
		ok   bool
	}{
		{"telegram", c.Telegram.Enabled},
		{"discord", c.Discord.Enabled},
		{"feishu", c.Feishu.Enabled},
		{"slack", c.Slack.Enabled},
		{"dingtalk", c.DingTalk.Enabled},
		{"qq", c.QQ.Enabled},
		{"workweixin", c.WorkWeixin.Enabled},
	}
	for _, ch := range checks {
		if !ch.ok {
			continue
		}
		var allow []string
		switch ch.name {
		case "telegram":
			allow = c.Telegram.AllowFrom
		case "discord":
			allow = c.Discord.AllowFrom
		case "feishu":
			allow = c.Feishu.AllowFrom
		case "slack":
			allow = c.Slack.AllowFrom
		case "dingtalk":
			allow = c.DingTalk.AllowFrom
		case "qq":
			allow = c.QQ.AllowFrom
		case "workweixin":
			allow = c.WorkWeixin.AllowFrom
		}
		if len(allow) == 0 {
			return fmt.Errorf(`channel %q has empty allowFrom (denies all). Set ["*"] to allow everyone, or add specific user IDs`, ch.name)
		}
	}
	return nil
}

func (m *Manager) StartAll(ctx context.Context) error {
	if err := m.ValidateAllowFrom(); err != nil {
		return err
	}
	for name, ch := range m.channels {
		m.wg.Add(1)
		go func(n string, c Channel) {
			defer m.wg.Done()
			if err := c.Start(ctx); err != nil {
				log.Printf("[channels] %s: %v", n, err)
			}
		}(name, ch)
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.dispatchOutbound(ctx)
	}()
	return nil
}

// StopAll stops all channels and waits for their goroutines to finish.
func (m *Manager) StopAll(ctx context.Context) error {
	for _, ch := range m.channels {
		_ = ch.Stop(ctx)
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return nil
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.bus.Outbound:
			ch, ok := m.channels[msg.Channel]
			if !ok {
				var names []string
				for k := range m.channels {
					names = append(names, k)
				}
				log.Printf("[channels] dispatchOutbound: channel %q not found, available: %v", msg.Channel, names)
				continue
			}
			if msg.Type == bus.MsgToolProgress {
				m.handleToolProgress(ctx, ch, msg)
				continue
			}
			// Final response: clear any tracked progress message for this chat
			m.clearProgressMsg(msg.Channel, msg.ChatID)
			log.Printf("[channels] dispatchOutbound: sending to %s:%s", msg.Channel, msg.ChatID)
			if m.preSend(ctx, msg.Channel, msg, ch) {
				// Placeholder was edited with content; skip Send
			} else if err := ch.Send(ctx, msg); err != nil {
				log.Printf("[channels] dispatchOutbound: send error: %v", err)
			}
		}
	}
}

func (m *Manager) handleToolProgress(ctx context.Context, ch Channel, msg bus.OutboundMessage) {
	key := msg.Channel + ":" + msg.ChatID

	editable, canEdit := ch.(EditableChannel)
	if !canEdit {
		// Channel doesn't support editing — send as a regular message
		_ = ch.Send(ctx, msg)
		return
	}

	m.progressMu.Lock()
	prevID := m.progressMsgIDs[key]
	m.progressMu.Unlock()

	if prevID != "" && msg.ReplyMessageID != "" {
		// Try to edit the existing progress message in-place
		if err := editable.EditMessage(ctx, msg.ChatID, prevID, msg.Content); err == nil {
			return
		}
		// Edit failed — fall through to send a new message
	}

	// Send new progress message and track its ID
	msgID, err := editable.SendAndTrack(ctx, msg)
	if err != nil {
		_ = ch.Send(ctx, msg)
		return
	}
	m.progressMu.Lock()
	m.progressMsgIDs[key] = msgID
	m.progressMu.Unlock()
}

func (m *Manager) clearProgressMsg(channel, chatID string) {
	key := channel + ":" + chatID
	m.progressMu.Lock()
	delete(m.progressMsgIDs, key)
	m.progressMu.Unlock()
}

func (m *Manager) StatusLines() []string {
	lines := []string{}
	tg := m.cfg.Channels.Telegram
	lines = append(lines, fmt.Sprintf("Telegram\t%v\t%s", tg.Enabled, tg.Token))
	dc := m.cfg.Channels.Discord
	lines = append(lines, fmt.Sprintf("Discord\t%v\t%s", dc.Enabled, dc.GatewayURL))
	fs := m.cfg.Channels.Feishu
	lines = append(lines, fmt.Sprintf("Feishu\t%v\t%s", fs.Enabled, fs.AppID))
	sl := m.cfg.Channels.Slack
	lines = append(lines, fmt.Sprintf("Slack\t%v\t%s", sl.Enabled, sl.BotToken))
	dt := m.cfg.Channels.DingTalk
	lines = append(lines, fmt.Sprintf("DingTalk\t%v\t%s", dt.Enabled, dt.AppKey))
	qq := m.cfg.Channels.QQ
	lines = append(lines, fmt.Sprintf("QQ\t%v\t%s", qq.Enabled, qq.AppID))
	ww := m.cfg.Channels.WorkWeixin
	lines = append(lines, fmt.Sprintf("WorkWeixin\t%v\t%s", ww.Enabled, ww.BotID))
	return lines
}

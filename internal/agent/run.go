package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"luckclaw/internal/bus"
	"luckclaw/internal/providers/openaiapi"
)

func (a *AgentLoop) Run(ctx context.Context, b *bus.MessageBus) error {
	a.SetBus(b)
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-b.Inbound:
			sessionKey := msg.Channel + ":" + msg.ChatID
			if msg.Metadata != nil {
				if override, ok := msg.Metadata["session_key_override"].(string); ok && override != "" {
					sessionKey = override
				}
			}
			// /stop cancels in-flight processing for this session and its subagent/spawn children
			if strings.TrimSpace(msg.Content) == "/stop" {
				n := a.Queue.CancelSessionAndChildren(sessionKey)
				content := "No active task to stop."
				if n > 0 {
					content = fmt.Sprintf("⏹ Stopped %d task(s).", n)
				}
				_ = b.PublishOutbound(ctx, bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: content,
				})
				continue
			}
			// /subagents list|kill|info|spawn
			if handled, content := a.handleSubagentsCmd(ctx, msg.Content, sessionKey, msg.Channel, msg.ChatID, b); handled {
				_ = b.PublishOutbound(ctx, bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: content,
				})
				continue
			}
			// Global semaphore: acquire before spawning to avoid unbounded goroutines
			if a.globalSem != nil && !a.globalSem.Acquire(ctx) {
				_ = b.PublishOutbound(ctx, bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: "Error: max concurrent tasks reached, try again later.",
				})
				continue
			}
			m := msg
			go func() {
				if a.globalSem != nil {
					defer a.globalSem.Release()
				}
				out, streamed, err := a.ProcessDirectWithContext(ctx, m.Content, sessionKey, m.Channel, m.ChatID, m.Media)
				if err != nil {
					var re *RetryExhaustedError
					if errors.As(err, &re) && a.Logger != nil {
						a.Logger.Error(fmt.Sprintf(
							"Retry exhausted (%d attempts): %s",
							len(re.Attempts), AttemptsJSON(re.Attempts),
						))
					}
					errMsg := err.Error()
					var fe *openaiapi.FailoverError
					if errors.As(err, &fe) {
						errMsg = fe.UserMessage()
					}
					out = "Error: " + errMsg
					streamed = false
				}
				if !streamed && out != "" {
					_ = b.PublishOutbound(ctx, bus.OutboundMessage{
						Channel: m.Channel,
						ChatID:  m.ChatID,
						Content: out,
					})
				}
			}()
		}
	}
}

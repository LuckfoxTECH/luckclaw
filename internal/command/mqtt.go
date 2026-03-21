package command

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"luckclaw/internal/paths"
	"luckclaw/internal/tools"
)

// MQTTHandler is the mqtt command handler
type MQTTHandler struct {
	Tool *tools.MQTTTool
}

// Execute executes the mqtt command
func (h *MQTTHandler) Execute(input Input) (Output, error) {
	h.ensureTool(input)

	if len(input.Args) == 0 {
		return h.showStatus()
	}

	sub := strings.ToLower(strings.TrimSpace(input.Args[0]))
	args := input.Args[1:]

	switch sub {
	case "list", "status":
		if len(args) != 0 {
			return Output{Content: "Error: too many arguments\nUsage: mqtt list", IsFinal: true}, nil
		}
		return h.showStatus()
	case "add", "connect":
		return h.connect(args, input.Flags, input.Context)
	case "disconnect":
		return h.disconnect(args)
	case "publish":
		return h.publish(args, input.Flags)
	case "subscribe":
		return h.subscribe(args, input.Flags, input.Context)
	case "logs":
		return h.showLogs(args, input.Flags)
	case "saved":
		if len(args) != 0 {
			return Output{Content: "Error: too many arguments\nUsage: mqtt saved", IsFinal: true}, nil
		}
		return h.showSaved()
	case "rm", "delete":
		return h.remove(args)
	case "restore":
		return h.restore(args, input.Context)
	case "clients":
		if len(args) != 0 {
			return Output{Content: "Error: too many arguments\nUsage: mqtt clients", IsFinal: true}, nil
		}
		return h.listClients()
	case "info", "show":
		return h.showStatus()
	default:
		return Output{Content: fmt.Sprintf("Error: unknown subcommand %q\n\n%s", sub, h.helpText()), IsFinal: true}, nil
	}
}

func (h *MQTTHandler) ensureTool(input Input) {
	if h.Tool != nil {
		h.ensureWorkspace(h.Tool, input)
		return
	}
	if input.Tools != nil {
		if t := input.Tools.Get("mqtt"); t != nil {
			if mt, ok := t.(*tools.MQTTTool); ok {
				h.Tool = mt
				h.ensureWorkspace(h.Tool, input)
				return
			}
		}
	}
	ws := h.resolveWorkspace(input)
	h.Tool = &tools.MQTTTool{Workspace: ws, LogDir: ws}
}

func (h *MQTTHandler) ensureWorkspace(tool *tools.MQTTTool, input Input) {
	if strings.TrimSpace(tool.Workspace) == "" || strings.TrimSpace(tool.LogDir) == "" {
		ws := h.resolveWorkspace(input)
		if strings.TrimSpace(tool.Workspace) == "" {
			tool.Workspace = ws
		}
		if strings.TrimSpace(tool.LogDir) == "" {
			tool.LogDir = ws
		}
	}
}

func (h *MQTTHandler) resolveWorkspace(input Input) string {
	ws, err := paths.WorkspaceDir()
	if err != nil || strings.TrimSpace(ws) == "" {
		if input.Config != nil {
			ws, _ = paths.ExpandUser(input.Config.Agents.Defaults.Workspace)
		}
	}
	ws = strings.TrimSpace(ws)
	if ws == "" {
		ws, _ = paths.ExpandUser("~/luckclaw")
	}
	return ws
}

func (h *MQTTHandler) showStatus() (Output, error) {
	statusStr, _ := h.Tool.HandleStatus()
	savedStr, _ := h.Tool.HandleSaved()

	var b strings.Builder
	b.WriteString(statusStr + "\n\n" + savedStr + "\n")
	b.WriteString("\nUsage:\n")
	b.WriteString("  mqtt list\n")
	b.WriteString("  mqtt connect <client_id> <broker> [--username <user>] [--password <pass>]\n")
	b.WriteString("  mqtt disconnect <client_id>\n")
	b.WriteString("  mqtt rm <client_id>\n")
	b.WriteString("  mqtt publish <client_id> <topic> <payload> [--qos 0|1|2]\n")
	b.WriteString("  mqtt subscribe <client_id> <topic> [--qos 0|1|2]\n")
	b.WriteString("  mqtt logs <client_id> [--topic <topic>] [--limit N]\n")

	return Output{
		Content:    strings.TrimRight(b.String(), "\n"),
		IsMarkdown: true,
		IsFinal:    true,
	}, nil
}

func (h *MQTTHandler) connect(args []string, flags map[string]string, ctx context.Context) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing arguments\nUsage: mqtt connect <client_id> <broker> | mqtt add <client_id> <broker>", IsFinal: true}, nil
	}

	// Single arg: try to restore saved connection
	if len(args) == 1 {
		clientID := strings.TrimSpace(args[0])
		result, err := h.Tool.HandleRestore(ctx, map[string]any{"client_id": clientID})
		if err != nil {
			return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
		}
		return Output{Content: result, IsFinal: true}, nil
	}

	broker := strings.TrimSpace(args[0])
	clientID := strings.TrimSpace(args[1])

	// Detect legacy order: if first arg looks like client_id, swap
	if !strings.Contains(broker, "://") && !strings.Contains(broker, ".") {
		broker, clientID = clientID, broker
	}

	username := flags["username"]
	password := flags["password"]

	// Parse inline flags from args
	for i := 2; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			return Output{Content: fmt.Sprintf("Error: unexpected argument %q\nUsage: mqtt connect <client_id> <broker> [--username <user>] [--password <pass>]", args[i]), IsFinal: true}, nil
		}
		if i+1 >= len(args) {
			return Output{Content: fmt.Sprintf("Error: missing value for %s\nUsage: mqtt connect <client_id> <broker> [--username <user>] [--password <pass>]", args[i]), IsFinal: true}, nil
		}
		switch strings.ToLower(args[i]) {
		case "--username":
			username = strings.TrimSpace(args[i+1])
		case "--password":
			password = strings.TrimSpace(args[i+1])
		default:
			return Output{Content: fmt.Sprintf("Error: unknown flag %s\nUsage: mqtt connect <client_id> <broker> [--username <user>] [--password <pass>]", args[i]), IsFinal: true}, nil
		}
		i++
	}

	result, err := h.Tool.HandleConnect(map[string]any{
		"broker":        broker,
		"client_id":     clientID,
		"username":      username,
		"password":      password,
		"clean_session": true,
	})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) disconnect(args []string) (Output, error) {
	if len(args) < 1 {
		clients := h.Tool.ListClients()
		if len(clients) == 0 {
			return Output{Content: "Error: missing <client_id>\nUsage: mqtt disconnect <client_id>\n\nNo MQTT clients available.", IsFinal: true}, nil
		}
		var b strings.Builder
		b.WriteString("Usage: mqtt disconnect <client_id>\n\nAvailable clients:\n")
		for _, c := range clients {
			status := "disconnected"
			if c.Connected {
				status = "connected"
			}
			b.WriteString(fmt.Sprintf("  - %s: broker=%s status=%s\n", c.ClientID, c.Broker, status))
		}
		return Output{Content: strings.TrimRight(b.String(), "\n"), IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: mqtt disconnect <client_id>", IsFinal: true}, nil
	}

	clientID := strings.TrimSpace(args[0])
	result, err := h.Tool.HandleDisconnect(map[string]any{"client_id": clientID})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) publish(args []string, flags map[string]string) (Output, error) {
	if len(args) < 3 {
		clients := h.Tool.ListClients()
		if len(clients) == 0 {
			return Output{Content: "Error: missing arguments\nUsage: mqtt publish <client_id> <topic> <payload> [--qos 0|1|2]\n\nNo MQTT clients available.", IsFinal: true}, nil
		}
		var b strings.Builder
		b.WriteString("Usage: mqtt publish <client_id> <topic> <payload> [--qos 0|1|2]\n\nAvailable clients:\n")
		for _, c := range clients {
			status := "disconnected"
			if c.Connected {
				status = "connected"
			}
			b.WriteString(fmt.Sprintf("  - %s: broker=%s status=%s\n", c.ClientID, c.Broker, status))
		}
		return Output{Content: strings.TrimRight(b.String(), "\n"), IsFinal: true}, nil
	}

	clientID := strings.TrimSpace(args[0])
	topic := strings.TrimSpace(args[1])
	payload := strings.TrimSpace(args[2])
	qos := 0

	if q, ok := flags["qos"]; ok {
		fmt.Sscanf(q, "%d", &qos)
	}
	for i := 3; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			return Output{Content: fmt.Sprintf("Error: unexpected argument %q\nUsage: mqtt publish <client_id> <topic> <payload> [--qos 0|1|2]", args[i]), IsFinal: true}, nil
		}
		if i+1 >= len(args) {
			return Output{Content: fmt.Sprintf("Error: missing value for %s\nUsage: mqtt publish <client_id> <topic> <payload> [--qos 0|1|2]", args[i]), IsFinal: true}, nil
		}
		switch strings.ToLower(args[i]) {
		case "--qos":
			fmt.Sscanf(args[i+1], "%d", &qos)
		default:
			return Output{Content: fmt.Sprintf("Error: unknown flag %s\nUsage: mqtt publish <client_id> <topic> <payload> [--qos 0|1|2]", args[i]), IsFinal: true}, nil
		}
		i++
	}

	result, err := h.Tool.HandlePublish(map[string]any{
		"client_id": clientID,
		"topic":     topic,
		"payload":   payload,
		"qos":       qos,
	})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) subscribe(args []string, flags map[string]string, ctx context.Context) (Output, error) {
	if len(args) < 2 {
		clients := h.Tool.ListClients()
		if len(clients) == 0 {
			return Output{Content: "Error: missing arguments\nUsage: mqtt subscribe <client_id> <topic> [--qos 0|1|2]\n\nNo MQTT clients available.", IsFinal: true}, nil
		}
		var b strings.Builder
		b.WriteString("Usage: mqtt subscribe <client_id> <topic> [--qos 0|1|2]\n\nAvailable clients:\n")
		for _, c := range clients {
			status := "disconnected"
			if c.Connected {
				status = "connected"
			}
			b.WriteString(fmt.Sprintf("  - %s: broker=%s status=%s\n", c.ClientID, c.Broker, status))
		}
		return Output{Content: strings.TrimRight(b.String(), "\n"), IsFinal: true}, nil
	}

	clientID := strings.TrimSpace(args[0])
	topic := strings.TrimSpace(args[1])
	qos := 0

	if q, ok := flags["qos"]; ok {
		fmt.Sscanf(q, "%d", &qos)
	}
	for i := 2; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			return Output{Content: fmt.Sprintf("Error: unexpected argument %q\nUsage: mqtt subscribe <client_id> <topic> [--qos 0|1|2]", args[i]), IsFinal: true}, nil
		}
		if i+1 >= len(args) {
			return Output{Content: fmt.Sprintf("Error: missing value for %s\nUsage: mqtt subscribe <client_id> <topic> [--qos 0|1|2]", args[i]), IsFinal: true}, nil
		}
		switch strings.ToLower(args[i]) {
		case "--qos":
			fmt.Sscanf(args[i+1], "%d", &qos)
		default:
			return Output{Content: fmt.Sprintf("Error: unknown flag %s\nUsage: mqtt subscribe <client_id> <topic> [--qos 0|1|2]", args[i]), IsFinal: true}, nil
		}
		i++
	}

	result, err := h.Tool.HandleSubscribe(ctx, map[string]any{
		"client_id": clientID,
		"topic":     topic,
		"qos":       qos,
	})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) showLogs(args []string, flags map[string]string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <client_id>\nUsage: mqtt logs <client_id> [--topic <topic>] [--limit N]", IsFinal: true}, nil
	}

	clientID := strings.TrimSpace(args[0])
	topic := ""
	limit := 50

	if t, ok := flags["topic"]; ok {
		topic = t
	}
	if l, ok := flags["limit"]; ok {
		fmt.Sscanf(l, "%d", &limit)
	}
	for i := 1; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			return Output{Content: fmt.Sprintf("Error: unexpected argument %q\nUsage: mqtt logs <client_id> [--topic <topic>] [--limit N]", args[i]), IsFinal: true}, nil
		}
		if i+1 >= len(args) {
			return Output{Content: fmt.Sprintf("Error: missing value for %s\nUsage: mqtt logs <client_id> [--topic <topic>] [--limit N]", args[i]), IsFinal: true}, nil
		}
		switch strings.ToLower(args[i]) {
		case "--topic":
			topic = strings.TrimSpace(args[i+1])
		case "--limit":
			fmt.Sscanf(args[i+1], "%d", &limit)
		default:
			return Output{Content: fmt.Sprintf("Error: unknown flag %s\nUsage: mqtt logs <client_id> [--topic <topic>] [--limit N]", args[i]), IsFinal: true}, nil
		}
		i++
	}

	result, err := h.Tool.HandleLogs(map[string]any{
		"client_id": clientID,
		"topic":     topic,
		"limit":     limit,
	})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) showSaved() (Output, error) {
	result, err := h.Tool.HandleSaved()
	if err != nil {
		return Output{Error: err, IsFinal: true}, nil
	}
	return Output{
		Content:    result,
		IsMarkdown: true,
		IsFinal:    true,
	}, nil
}

func (h *MQTTHandler) remove(args []string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <client_id>\nUsage: mqtt rm <client_id>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: mqtt rm <client_id>", IsFinal: true}, nil
	}
	clientID := strings.TrimSpace(args[0])
	result, err := h.Tool.HandleRemove(map[string]any{"client_id": clientID})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) restore(args []string, ctx context.Context) (Output, error) {
	clientID := ""
	if len(args) > 0 {
		clientID = strings.TrimSpace(args[0])
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: mqtt restore [client_id]", IsFinal: true}, nil
	}
	result, err := h.Tool.HandleRestore(ctx, map[string]any{"client_id": clientID})
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: result, IsFinal: true}, nil
}

func (h *MQTTHandler) listClients() (Output, error) {
	clients := h.Tool.ListClients()
	if len(clients) == 0 {
		return Output{Content: "No MQTT connections.", IsFinal: true}, nil
	}
	var b strings.Builder
	b.WriteString("MQTT Clients:\n")
	var ids []string
	for _, c := range clients {
		ids = append(ids, c.ClientID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, c := range clients {
			if c.ClientID == id {
				status := "disconnected"
				if c.Connected {
					status = "connected"
				}
				b.WriteString(fmt.Sprintf("  - %s: broker=%s status=%s topics=%d\n", c.ClientID, c.Broker, status, len(c.Topics)))
				break
			}
		}
	}
	return Output{Content: strings.TrimRight(b.String(), "\n"), IsFinal: true}, nil
}

func (h *MQTTHandler) helpText() string {
	return `MQTT commands:
  mqtt list                              List connections
  mqtt connect <client_id> <broker>      Connect to broker
  mqtt disconnect <client_id>            Disconnect from broker
  mqtt publish <client_id> <topic> <payload>  Publish message
  mqtt subscribe <client_id> <topic>     Subscribe to topic
  mqtt logs <client_id>                  Show message logs
  mqtt rm <client_id>                    Remove saved connection
  mqtt clients                           List all clients`
}

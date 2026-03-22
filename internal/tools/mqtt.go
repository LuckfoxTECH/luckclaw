package tools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"luckclaw/internal/logging"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// TopicAlert configures alert delivery when a subscribed topic receives a message.
type TopicAlert struct {
	Channel        string    `json:"channel"`          // delivery channel (e.g. "telegram", "discord")
	ChatID         string    `json:"chat_id"`          // target chat ID
	MinIntervalSec int       `json:"min_interval_sec"` // minimum seconds between alerts (0 = no limit)
	Enabled        bool      `json:"enabled"`          // whether alert is enabled
	LastAlertTime  time.Time `json:"-"`                // last alert sent time (not persisted)
}

type MQTTTool struct {
	Workspace string
	LogDir    string
	Logger    logging.Logger

	// OnMessage is called when a subscribed topic receives a message and alert is configured.
	OnMessage func(clientID, topic, payload, channel, chatID string)

	// OnTopicMessage is called when any subscribed topic receives a message (for TUI display).
	OnTopicMessage func(clientID, topic, payload string)

	connections map[string]*mqttConnection
	mu          sync.RWMutex
}

func (t *MQTTTool) debugf(format string, args ...any) {
	if t.Logger != nil {
		t.Logger.Debug(fmt.Sprintf(format, args...))
		return
	}
	log.Printf(format, args...)
}

func (t *MQTTTool) ensureConnectionsLocked() {
	if t.connections == nil {
		t.connections = make(map[string]*mqttConnection)
	}
}

type mqttPersistedFile struct {
	Version     int                 `json:"version"`
	Connections []mqttPersistedConn `json:"connections"`
}

type mqttPersistedConn struct {
	ClientID     string                        `json:"client_id"`
	Broker       string                        `json:"broker"`
	Username     string                        `json:"username,omitempty"`
	CleanSession bool                          `json:"clean_session"`
	TopicAlerts  map[string]mqttPersistedAlert `json:"topic_alerts,omitempty"`
}

type mqttPersistedAlert struct {
	Channel        string `json:"channel"`
	ChatID         string `json:"chat_id"`
	MinIntervalSec int    `json:"min_interval_sec"`
	Enabled        bool   `json:"enabled"`
}

type mqttConnection struct {
	ID          string
	Broker      string
	ClientID    string
	Username    string
	Password    string
	Clean       bool
	Client      mqtt.Client
	LogDir      string
	Topics      map[string]mqtt.MessageHandler
	TopicAlerts map[string]*TopicAlert
}

func (t *MQTTTool) Name() string { return "mqtt" }

func (t *MQTTTool) Description() string {
	return "Connect to MQTT brokers, publish messages, and subscribe to topics with background monitoring. " +
		"Actions: connect, disconnect, publish, subscribe, unsubscribe, status, logs, saved, list, restore."
}

func (t *MQTTTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action: connect, disconnect, publish, subscribe, unsubscribe, status, logs, saved, list, restore",
				"enum":        []any{"connect", "disconnect", "publish", "subscribe", "unsubscribe", "status", "logs", "saved", "list", "restore"},
			},
			"client_id": map[string]any{
				"type":        "string",
				"description": "Unique client identifier for the connection",
			},
			"broker": map[string]any{
				"type":        "string",
				"description": "MQTT broker URL (e.g. tcp://localhost:1883, ssl://localhost:8883)",
			},
			"username": map[string]any{
				"type":        "string",
				"description": "Username for authentication (optional)",
			},
			"password": map[string]any{
				"type":        "string",
				"description": "Password for authentication (optional)",
			},
			"clean_session": map[string]any{
				"type":        "boolean",
				"description": "Clean session on connect (default true)",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "MQTT topic for publish or subscribe",
			},
			"payload": map[string]any{
				"type":        "string",
				"description": "Message payload to publish",
			},
			"qos": map[string]any{
				"type":        "integer",
				"description": "QoS level: 0, 1, or 2 (default 0)",
				"minimum":     0,
				"maximum":     2,
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max number of log entries to return for logs action (default 50)",
				"minimum":     1,
				"maximum":     1000,
			},
			"alert_channel": map[string]any{
				"type":        "string",
				"description": "Channel for alert delivery (e.g. 'telegram', 'discord')",
			},
			"alert_chat_id": map[string]any{
				"type":        "string",
				"description": "Chat ID for alert delivery",
			},
			"alert_interval": map[string]any{
				"type":        "integer",
				"description": "Minimum seconds between alerts (0 = no limit, default 0)",
				"minimum":     0,
			},
		},
		"required": []any{"action"},
	}
}

func (t *MQTTTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}

	switch action {
	case "add":
		return t.HandleAdd(args)
	case "connect":
		return t.HandleConnect(args)
	case "disconnect":
		return t.HandleDisconnect(args)
	case "publish":
		return t.HandlePublish(args)
	case "subscribe":
		return t.HandleSubscribe(ctx, args)
	case "unsubscribe":
		return t.HandleUnsubscribe(args)
	case "status":
		return t.HandleStatus()
	case "logs":
		return t.HandleLogs(args)
	case "saved":
		return t.HandleSaved()
	case "list":
		return t.HandleList()
	case "restore":
		return t.HandleRestore(ctx, args)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (t *MQTTTool) persistedPath() string {
	ws := strings.TrimSpace(t.Workspace)
	if ws == "" {
		ws = "."
	}
	return filepath.Join(ws, ".luckclaw", "mqtt_connections.json")
}

func (t *MQTTTool) loadPersisted() (*mqttPersistedFile, error) {
	path := t.persistedPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &mqttPersistedFile{Version: 1}, nil
		}
		return nil, err
	}
	var f mqttPersistedFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return &f, nil
}

func (t *MQTTTool) savePersisted(f *mqttPersistedFile) error {
	path := t.persistedPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (t *MQTTTool) persistCurrentLocked() error {
	t.ensureConnectionsLocked()
	ids := make([]string, 0, len(t.connections))
	for id := range t.connections {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := &mqttPersistedFile{Version: 1}
	out.Connections = make([]mqttPersistedConn, 0, len(ids))
	for _, id := range ids {
		c := t.connections[id]
		if c == nil {
			continue
		}
		pc := mqttPersistedConn{
			ClientID:     c.ClientID,
			Broker:       c.Broker,
			Username:     c.Username,
			CleanSession: c.Clean,
		}
		// Persist topic alerts
		if len(c.TopicAlerts) > 0 {
			pc.TopicAlerts = make(map[string]mqttPersistedAlert)
			for topic, alert := range c.TopicAlerts {
				pc.TopicAlerts[topic] = mqttPersistedAlert{
					Channel:        alert.Channel,
					ChatID:         alert.ChatID,
					MinIntervalSec: alert.MinIntervalSec,
					Enabled:        alert.Enabled,
				}
			}
		}
		out.Connections = append(out.Connections, pc)
	}
	return t.savePersisted(out)
}

func (t *MQTTTool) HandleConnect(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	broker, _ := args["broker"].(string)
	username, _ := args["username"].(string)
	password, _ := args["password"].(string)
	clean, _ := args["clean_session"].(bool)

	clientID = strings.TrimSpace(clientID)
	broker = strings.TrimSpace(broker)
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}
	if broker == "" {
		return "", fmt.Errorf("broker is required")
	}
	if !strings.Contains(broker, "://") {
		broker = "tcp://" + broker
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureConnectionsLocked()

	if existing, ok := t.connections[clientID]; ok {
		if existing.Client != nil && existing.Client.IsConnected() {
			return fmt.Sprintf("Client %q already connected to %s", clientID, broker), nil
		}
	}

	opts := mqtt.NewClientOptions().
		SetClientID(clientID).
		AddBroker(broker).
		SetCleanSession(clean).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectTimeout(10 * time.Second)
	opts.SetConnectRetryInterval(2 * time.Second)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		t.debugf("[mqtt] on_connect client_id=%s broker=%s", clientID, broker)
	})
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		t.debugf("[mqtt] connection_lost client_id=%s broker=%s err=%v", clientID, broker, err)
	})
	opts.SetReconnectingHandler(func(c mqtt.Client, o *mqtt.ClientOptions) {
		_ = o
		t.debugf("[mqtt] reconnecting client_id=%s broker=%s", clientID, broker)
	})
	opts.SetConnectionAttemptHandler(func(brokerURL *url.URL, tlsCfg *tls.Config) *tls.Config {
		t.debugf("[mqtt] connect_attempt client_id=%s broker=%s url=%s", clientID, broker, brokerURL.String())
		return tlsCfg
	})

	if username != "" {
		opts.SetUsername(username)
	}
	if password != "" {
		opts.SetPassword(password)
	}

	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		t.debugf("[mqtt] received client_id=%s topic=%s payload=%s", clientID, msg.Topic(), string(msg.Payload()))
	})

	conn := &mqttConnection{
		ID:          clientID,
		Broker:      broker,
		ClientID:    clientID,
		Username:    username,
		Password:    password,
		Clean:       clean,
		Topics:      make(map[string]mqtt.MessageHandler),
		TopicAlerts: make(map[string]*TopicAlert),
	}

	logDir := t.LogDir
	if logDir == "" {
		logDir = t.Workspace
	}
	if logDir == "" {
		tmpDir, err := os.MkdirTemp("", "mqtt-*")
		if err != nil {
			logDir = "/tmp"
		} else {
			logDir = tmpDir
		}
	}
	conn.LogDir = logDir

	t.debugf("[mqtt] connect start client_id=%s broker=%s clean_session=%v", clientID, broker, clean)
	client := mqtt.NewClient(opts)
	t.debugf("[mqtt] client created client_id=%s broker=%s", clientID, broker)
	token := client.Connect()
	t.debugf("[mqtt] connect issued client_id=%s broker=%s", clientID, broker)
	if !token.WaitTimeout(15 * time.Second) {
		t.debugf("[mqtt] connect timeout client_id=%s broker=%s", clientID, broker)
		return "", fmt.Errorf("failed to connect to %s: timeout", broker)
	}
	if token.Error() != nil {
		t.debugf("[mqtt] connect error client_id=%s broker=%s err=%v", clientID, broker, token.Error())
		return "", fmt.Errorf("failed to connect to %s: %w", broker, token.Error())
	}
	t.debugf("[mqtt] connect ok client_id=%s broker=%s", clientID, broker)

	conn.Client = client
	t.connections[clientID] = conn
	_ = t.persistCurrentLocked()

	return fmt.Sprintf("Connected to %s as %q (log_dir: %s)", broker, clientID, logDir), nil
}

func (t *MQTTTool) HandleAdd(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	broker, _ := args["broker"].(string)
	username, _ := args["username"].(string)
	password, _ := args["password"].(string)
	clean, _ := args["clean_session"].(bool)

	clientID = strings.TrimSpace(clientID)
	broker = strings.TrimSpace(broker)
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}
	if broker == "" {
		return "", fmt.Errorf("broker is required")
	}
	if !strings.Contains(broker, "://") {
		broker = "tcp://" + broker
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureConnectionsLocked()

	// Check if already exists
	if existing, ok := t.connections[clientID]; ok {
		return fmt.Sprintf("Client %q already exists (broker: %s, connected: %v)", clientID, existing.Broker, existing.Client != nil && existing.Client.IsConnected()), nil
	}

	// Create connection entry without actually connecting
	conn := &mqttConnection{
		ID:          clientID,
		Broker:      broker,
		ClientID:    clientID,
		Username:    username,
		Password:    password,
		Clean:       clean,
		Topics:      make(map[string]mqtt.MessageHandler),
		TopicAlerts: make(map[string]*TopicAlert),
	}

	logDir := t.LogDir
	if logDir == "" {
		logDir = t.Workspace
	}
	conn.LogDir = logDir

	t.connections[clientID] = conn
	_ = t.persistCurrentLocked()

	user := ""
	if username != "" {
		user = fmt.Sprintf(" username=%s", username)
	}
	return fmt.Sprintf("Added connection %q (broker: %s%s). Use 'mqtt connect %s' to connect.", clientID, broker, user, clientID), nil
}

func (t *MQTTTool) HandleDisconnect(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	clientID = strings.TrimSpace(clientID)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	conn, ok := t.connections[clientID]
	if !ok {
		return fmt.Sprintf("Client %q not found", clientID), nil
	}

	if conn.Client != nil && conn.Client.IsConnected() {
		conn.Client.Disconnect(1000)
	}

	// We do NOT remove from t.connections here, so it stays in "saved" state.
	return fmt.Sprintf("Disconnected client %q", clientID), nil
}

func (t *MQTTTool) HandleRemove(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	clientID = strings.TrimSpace(clientID)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureConnectionsLocked()

	c, ok := t.connections[clientID]
	if !ok {
		return fmt.Sprintf("Client %q not found", clientID), nil
	}
	if c.Client != nil && c.Client.IsConnected() {
		c.Client.Disconnect(250)
	}
	delete(t.connections, clientID)
	_ = t.persistCurrentLocked()
	return fmt.Sprintf("Removed client %q", clientID), nil
}

func (t *MQTTTool) HandleClear() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureConnectionsLocked()

	count := len(t.connections)
	if count == 0 {
		return "No connections to clear.", nil
	}

	// Disconnect all clients first
	for _, c := range t.connections {
		if c.Client != nil && c.Client.IsConnected() {
			c.Client.Disconnect(250)
		}
	}

	// Clear all connections
	t.connections = make(map[string]*mqttConnection)
	_ = t.persistCurrentLocked()
	return fmt.Sprintf("Cleared %d connection(s).", count), nil
}

func (t *MQTTTool) HandleSaved() (string, error) {
	f, err := t.loadPersisted()
	if err != nil {
		return "", err
	}
	if len(f.Connections) == 0 {
		return fmt.Sprintf("No saved MQTT connections. (file: %s)", t.persistedPath()), nil
	}
	var b strings.Builder
	b.WriteString("Saved MQTT connections:\n")
	for _, c := range f.Connections {
		user := ""
		if strings.TrimSpace(c.Username) != "" {
			user = " username=" + c.Username
		}
		b.WriteString(fmt.Sprintf("- %s: broker=%s clean_session=%v%s\n", c.ClientID, c.Broker, c.CleanSession, user))
	}
	b.WriteString(fmt.Sprintf("\nFile: %s", t.persistedPath()))
	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *MQTTTool) HandleList() (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.connections) == 0 {
		return "No MQTT connections.", nil
	}

	var b strings.Builder
	b.WriteString("MQTT Connections:\n")

	for id, conn := range t.connections {
		status := "disconnected"
		if conn.Client != nil && conn.Client.IsConnected() {
			status = "connected"
		}
		user := ""
		if strings.TrimSpace(conn.Username) != "" {
			user = " username=" + conn.Username
		}
		b.WriteString(fmt.Sprintf("- %s: broker=%s status=%s%s\n", id, conn.Broker, status, user))

		// Show subscribed topics with alert status
		if len(conn.Topics) > 0 {
			b.WriteString("  Subscribed topics:\n")
			for topic := range conn.Topics {
				alertStatus := "no alert"
				if alert, ok := conn.TopicAlerts[topic]; ok && alert.Enabled {
					alertStatus = fmt.Sprintf("alert → %s:%s (interval: %ds)", alert.Channel, alert.ChatID, alert.MinIntervalSec)
				} else if alert, ok := conn.TopicAlerts[topic]; ok && !alert.Enabled {
					alertStatus = "alert disabled"
				}
				b.WriteString(fmt.Sprintf("    - %s [%s]\n", topic, alertStatus))
			}
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *MQTTTool) HandleUnsubscribe(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	topic, _ := args["topic"].(string)

	clientID = strings.TrimSpace(clientID)
	topic = strings.TrimSpace(topic)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	conn, ok := t.connections[clientID]
	if !ok {
		return "", fmt.Errorf("client %q not found", clientID)
	}

	// Remove alert if exists
	if conn.TopicAlerts != nil {
		delete(conn.TopicAlerts, topic)
	}

	// Unsubscribe from topic
	if conn.Client != nil && conn.Client.IsConnected() {
		if _, exists := conn.Topics[topic]; exists {
			token := conn.Client.Unsubscribe(topic)
			if token.Wait() && token.Error() != nil {
				return "", fmt.Errorf("unsubscribe failed: %w", token.Error())
			}
			delete(conn.Topics, topic)
		}
	} else {
		// Client not connected, just remove from local tracking
		delete(conn.Topics, topic)
	}

	_ = t.persistCurrentLocked()
	return fmt.Sprintf("Unsubscribed from topic %q (client: %s)", topic, clientID), nil
}

func (t *MQTTTool) HandleRestore(ctx context.Context, args map[string]any) (string, error) {
	_ = ctx
	clientID, _ := args["client_id"].(string)
	clientID = strings.TrimSpace(clientID)

	f, err := t.loadPersisted()
	if err != nil {
		return "", err
	}
	if len(f.Connections) == 0 {
		return fmt.Sprintf("No saved MQTT connections to restore. (file: %s)", t.persistedPath()), nil
	}

	var targets []mqttPersistedConn
	if clientID != "" {
		for _, c := range f.Connections {
			if c.ClientID == clientID {
				targets = append(targets, c)
				break
			}
		}
		if len(targets) == 0 {
			return fmt.Sprintf("Saved client %q not found. (file: %s)", clientID, t.persistedPath()), nil
		}
	} else {
		targets = f.Connections
	}

	var b strings.Builder
	for _, c := range targets {
		out, err := t.HandleConnect(map[string]any{
			"client_id":     c.ClientID,
			"broker":        c.Broker,
			"username":      c.Username,
			"password":      "",
			"clean_session": c.CleanSession,
		})
		if err != nil {
			b.WriteString(fmt.Sprintf("Restore %s: error: %v\n", c.ClientID, err))
			continue
		}
		b.WriteString(out + "\n")

		// Restore topic alerts
		if len(c.TopicAlerts) > 0 {
			t.mu.Lock()
			if conn := t.connections[c.ClientID]; conn != nil {
				if conn.TopicAlerts == nil {
					conn.TopicAlerts = make(map[string]*TopicAlert)
				}
				for topic, pa := range c.TopicAlerts {
					conn.TopicAlerts[topic] = &TopicAlert{
						Channel:        pa.Channel,
						ChatID:         pa.ChatID,
						MinIntervalSec: pa.MinIntervalSec,
						Enabled:        pa.Enabled,
					}
				}
			}
			t.mu.Unlock()
			b.WriteString(fmt.Sprintf("  Restored %d topic alert(s) for %s\n", len(c.TopicAlerts), c.ClientID))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *MQTTTool) HandlePublish(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	topic, _ := args["topic"].(string)
	payload, _ := args["payload"].(string)
	qos, _ := args["qos"].(int)

	clientID = strings.TrimSpace(clientID)
	topic = strings.TrimSpace(topic)
	payloadStr := fmt.Sprintf("%v", payload)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	t.mu.RLock()
	conn, ok := t.connections[clientID]
	t.mu.RUnlock()

	if !ok || conn.Client == nil || !conn.Client.IsConnected() {
		return "", fmt.Errorf("client %q is not connected", clientID)
	}

	qosLevel := byte(qos)
	token := conn.Client.Publish(topic, qosLevel, false, payloadStr)
	if token.Wait() && token.Error() != nil {
		return "", fmt.Errorf("publish failed: %w", token.Error())
	}

	return fmt.Sprintf("Published to %q (client: %s, QoS: %d, payload: %q)", topic, clientID, qos, payloadStr), nil
}

func (t *MQTTTool) HandleSubscribe(ctx context.Context, args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	topic, _ := args["topic"].(string)
	qos, _ := args["qos"].(int)

	clientID = strings.TrimSpace(clientID)
	topic = strings.TrimSpace(topic)

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}
	if topic == "" {
		return "", fmt.Errorf("topic is required")
	}

	t.mu.RLock()
	conn, ok := t.connections[clientID]
	t.mu.RUnlock()

	if !ok || conn.Client == nil || !conn.Client.IsConnected() {
		return "", fmt.Errorf("client %q is not connected", clientID)
	}

	// Configure alert if parameters provided
	alertChannel, _ := args["alert_channel"].(string)
	alertChatID, _ := args["alert_chat_id"].(string)
	minInterval, _ := args["alert_interval"].(int)

	if alertChannel != "" && alertChatID != "" {
		t.mu.Lock()
		if conn.TopicAlerts == nil {
			conn.TopicAlerts = make(map[string]*TopicAlert)
		}
		conn.TopicAlerts[topic] = &TopicAlert{
			Channel:        strings.TrimSpace(alertChannel),
			ChatID:         strings.TrimSpace(alertChatID),
			MinIntervalSec: minInterval,
			Enabled:        true,
		}
		t.mu.Unlock()
		_ = t.persistCurrentLocked()
	}

	qosLevel := byte(qos)
	if qos < 0 || qos > 2 {
		qosLevel = 0
	}

	safeTopic := t.sanitizeFilename(topic)
	logPath := filepath.Join(conn.LogDir, fmt.Sprintf("%s_%s.log", clientID, safeTopic))

	handler := func(client mqtt.Client, msg mqtt.Message) {
		ts := time.Now().Format(time.RFC3339)
		payload := string(msg.Payload())
		entry := fmt.Sprintf("[%s] topic=%s qos=%d payload=%q\n", ts, msg.Topic(), msg.Qos(), payload)
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			f.WriteString(entry)
			f.Close()
		}
		t.debugf("[mqtt] %s", strings.TrimRight(entry, "\n"))

		// Check for topic alert
		t.mu.RLock()
		alert, hasAlert := conn.TopicAlerts[msg.Topic()]
		needsSend := false
		var alertChannel, alertChatID string
		if hasAlert && alert.Enabled && t.OnMessage != nil {
			now := time.Now()
			if alert.MinIntervalSec <= 0 || now.Sub(alert.LastAlertTime) >= time.Duration(alert.MinIntervalSec)*time.Second {
				alert.LastAlertTime = now
				needsSend = true
				alertChannel = alert.Channel
				alertChatID = alert.ChatID
			}
		}
		t.mu.RUnlock()

		if needsSend {
			t.OnMessage(clientID, msg.Topic(), payload, alertChannel, alertChatID)
		}

		// Call OnTopicMessage for TUI display
		if t.OnTopicMessage != nil {
			t.OnTopicMessage(clientID, msg.Topic(), payload)
		}
	}

	token := conn.Client.Subscribe(topic, qosLevel, handler)
	if token.Wait() && token.Error() != nil {
		return "", fmt.Errorf("subscribe failed: %w", token.Error())
	}

	conn.Topics[topic] = handler

	result := fmt.Sprintf("Subscribed to %q (client: %s, QoS: %d, log: %s)", topic, clientID, qos, logPath)
	if alertChannel != "" && alertChatID != "" {
		result += fmt.Sprintf("\nAlert configured: → %s:%s (interval: %ds)", alertChannel, alertChatID, minInterval)
	}
	return result, nil
}

func (t *MQTTTool) sanitizeFilename(topic string) string {
	safe := strings.ReplaceAll(topic, "/", "_")
	safe = strings.ReplaceAll(safe, "#", "_")
	safe = strings.ReplaceAll(safe, "+", "_")
	safe = strings.ReplaceAll(safe, "*", "_")
	return safe
}

func (t *MQTTTool) HandleStatus() (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.connections) == 0 {
		return "No MQTT connections.", nil
	}

	var sb strings.Builder
	sb.WriteString("MQTT Connections:\n")
	for id, conn := range t.connections {
		status := "disconnected"
		if conn.Client != nil && conn.Client.IsConnected() {
			status = "connected"
		}
		sb.WriteString(fmt.Sprintf("- %s: broker=%s status=%s topics=%d\n", id, conn.Broker, status, len(conn.Topics)))
	}
	return strings.TrimSpace(sb.String()), nil
}

func (t *MQTTTool) HandleLogs(args map[string]any) (string, error) {
	clientID, _ := args["client_id"].(string)
	topic, _ := args["topic"].(string)
	limit, _ := args["limit"].(int)

	clientID = strings.TrimSpace(clientID)
	topic = strings.TrimSpace(topic)

	if limit == 0 {
		limit = 50
	}

	if clientID == "" {
		return "", fmt.Errorf("client_id is required")
	}

	t.mu.RLock()
	conn, ok := t.connections[clientID]
	t.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("client %q not found", clientID)
	}

	var logPath string
	if topic != "" {
		safeTopic := t.sanitizeFilename(topic)
		logPath = filepath.Join(conn.LogDir, fmt.Sprintf("%s_%s.log", clientID, safeTopic))
	} else {
		logPath = filepath.Join(conn.LogDir, fmt.Sprintf("%s_*.log", clientID))
	}

	matches, err := filepath.Glob(logPath)
	if err != nil || len(matches) == 0 {
		return fmt.Sprintf("No logs found for client %q", clientID), nil
	}

	var allLines []string
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		allLines = append(allLines, lines...)
	}

	total := len(allLines)
	start := 0
	if total > limit {
		start = total - limit
	}

	var sb strings.Builder
	for i := start; i < total; i++ {
		line := strings.TrimSpace(allLines[i])
		if line != "" {
			sb.WriteString(line + "\n")
		}
	}

	result := sb.String()
	if result == "" {
		return fmt.Sprintf("No log entries for client %q", clientID), nil
	}

	return result, nil
}

type mqttTopicAlert struct {
	Topic          string `json:"topic"`
	Channel        string `json:"channel"`
	ChatID         string `json:"chat_id"`
	MinIntervalSec int    `json:"min_interval_sec"`
	Enabled        bool   `json:"enabled"`
}

type mqttClientInfo struct {
	ClientID    string           `json:"client_id"`
	Broker      string           `json:"broker"`
	Username    string           `json:"username,omitempty"`
	Connected   bool             `json:"connected"`
	LogDir      string           `json:"log_dir"`
	Topics      []string         `json:"topics"`
	TopicAlerts []mqttTopicAlert `json:"topic_alerts,omitempty"`
}

func (t *MQTTTool) ListClients() []mqttClientInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	clients := make([]mqttClientInfo, 0, len(t.connections))
	for _, conn := range t.connections {
		topics := make([]string, 0, len(conn.Topics))
		for t := range conn.Topics {
			topics = append(topics, t)
		}
		connected := conn.Client != nil && conn.Client.IsConnected()

		var topicAlerts []mqttTopicAlert
		for topic, alert := range conn.TopicAlerts {
			topicAlerts = append(topicAlerts, mqttTopicAlert{
				Topic:          topic,
				Channel:        alert.Channel,
				ChatID:         alert.ChatID,
				MinIntervalSec: alert.MinIntervalSec,
				Enabled:        alert.Enabled,
			})
		}

		clients = append(clients, mqttClientInfo{
			ClientID:    conn.ClientID,
			Broker:      conn.Broker,
			Username:    conn.Username,
			Connected:   connected,
			LogDir:      conn.LogDir,
			Topics:      topics,
			TopicAlerts: topicAlerts,
		})
	}
	return clients
}

func (t *MQTTTool) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.ListClients())
}

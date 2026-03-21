package command

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"luckclaw/internal/paths"
	"luckclaw/internal/tools"
)

// ModbusHandler is the modbus command handler
type ModbusHandler struct{}

// Execute executes the modbus command
func (h *ModbusHandler) Execute(input Input) (Output, error) {
	ws, err := h.resolveWorkspace(input)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}

	if len(input.Args) == 0 {
		return h.listDevices(ws)
	}

	sub := strings.ToLower(strings.TrimSpace(input.Args[0]))
	args := input.Args[1:]

	switch sub {
	case "list", "status":
		return h.listDevices(ws)
	case "path":
		return Output{Content: tools.ModbusConfigPath(ws), IsFinal: true}, nil
	case "template":
		return h.showTemplate(ws)
	case "add", "set":
		return h.addDevice(ws, args)
	case "use":
		return h.useDevice(ws, args)
	case "off", "disconnect":
		return h.disconnect(ws)
	case "info", "show":
		return h.showInfo(ws, args)
	case "rm", "remove", "del", "delete":
		return h.removeDevice(ws, args)
	default:
		return Output{Content: fmt.Sprintf("Error: unknown subcommand %q\n\n%s", sub, h.helpText()), IsFinal: true}, nil
	}
}

func (h *ModbusHandler) resolveWorkspace(input Input) (string, error) {
	if input.Config != nil {
		ws, err := paths.ExpandUser(input.Config.DefaultWorkspace())
		if err == nil && strings.TrimSpace(ws) != "" {
			return ws, nil
		}
		ws, err = paths.ExpandUser(input.Config.Agents.Defaults.Workspace)
		if err == nil && strings.TrimSpace(ws) != "" {
			return ws, nil
		}
	}
	ws, err := paths.WorkspaceDir()
	if err == nil && strings.TrimSpace(ws) != "" {
		return ws, nil
	}
	return "", fmt.Errorf("workspace is not configured")
}

func (h *ModbusHandler) listDevices(ws string) (Output, error) {
	cfg, err := tools.ModbusLoadConfig(ws)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}

	var names []string
	for k := range cfg.Devices {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Modbus config: %s\n", tools.ModbusConfigPath(ws)))
	if cfg.Active != "" {
		b.WriteString(fmt.Sprintf("Active: %s\n", cfg.Active))
	} else {
		b.WriteString("Active: (none)\n")
	}
	b.WriteString(fmt.Sprintf("Devices: %d\n", len(names)))
	for _, n := range names {
		d := cfg.Devices[n]
		port := d.Port
		if port == 0 {
			port = 502
		}
		unit := d.UnitID
		if unit == 0 {
			unit = 1
		}
		line := fmt.Sprintf("  - %s: %s:%d unit_id=%d", n, d.Host, port, unit)
		if strings.TrimSpace(d.Description) != "" {
			line += " (" + strings.TrimSpace(d.Description) + ")"
		}
		if n == cfg.Active {
			line += " [active]"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + h.helpText())

	return Output{Content: strings.TrimRight(b.String(), "\n"), IsFinal: true}, nil
}

func (h *ModbusHandler) showTemplate(ws string) (Output, error) {
	template := map[string]any{
		"version": 1,
		"active":  "plc1",
		"devices": map[string]any{
			"plc1": map[string]any{
				"host":        "192.168.0.10",
				"port":        502,
				"unit_id":     1,
				"timeout_ms":  2000,
				"description": "optional note",
			},
		},
	}
	b, _ := json.MarshalIndent(template, "", "  ")
	var out strings.Builder
	out.WriteString("Save this JSON to:\n")
	out.WriteString("  " + tools.ModbusConfigPath(ws) + "\n\n")
	out.WriteString(string(b) + "\n")
	return Output{Content: strings.TrimRight(out.String(), "\n"), IsFinal: true}, nil
}

func (h *ModbusHandler) addDevice(ws string, args []string) (Output, error) {
	if len(args) < 4 {
		cfg, err := tools.ModbusLoadConfig(ws)
		if err != nil || len(cfg.Devices) == 0 {
			return Output{Content: "Error: missing arguments\nUsage: modbus add <name> <host> <port> <unit_id> [timeout_ms]\n\nNo saved devices. Provide all arguments.", IsFinal: true}, nil
		}
		return h.showUsageWithDevices(cfg, "Error: missing arguments\nUsage: modbus add <name> <host> <port> <unit_id> [timeout_ms]\n\nSaved devices:")
	}
	if len(args) > 5 {
		return Output{Content: "Error: too many arguments\nUsage: modbus add <name> <host> <port> <unit_id> [timeout_ms]", IsFinal: true}, nil
	}

	name := strings.TrimSpace(args[0])
	host := strings.TrimSpace(args[1])
	port, err := strconv.Atoi(strings.TrimSpace(args[2]))
	if err != nil || port <= 0 || port > 65535 {
		return Output{Content: "Error: invalid port", IsFinal: true}, nil
	}
	unitID, err := strconv.Atoi(strings.TrimSpace(args[3]))
	if err != nil || unitID < 0 || unitID > 255 {
		return Output{Content: "Error: invalid unit_id (0-255)", IsFinal: true}, nil
	}
	timeoutMS := 0
	if len(args) >= 5 {
		timeoutMS, err = strconv.Atoi(strings.TrimSpace(args[4]))
		if err != nil || timeoutMS < 0 {
			return Output{Content: "Error: invalid timeout_ms", IsFinal: true}, nil
		}
	}
	if name == "" {
		return Output{Content: "Error: name is required", IsFinal: true}, nil
	}
	if host == "" {
		return Output{Content: "Error: host is required", IsFinal: true}, nil
	}

	cfg, err := tools.ModbusLoadConfig(ws)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	cfg.Devices[name] = tools.ModbusDeviceConfig{
		Host:      host,
		Port:      port,
		UnitID:    unitID,
		TimeoutMS: timeoutMS,
	}
	cfg.Active = name
	if err := tools.ModbusSaveConfig(ws, cfg); err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: fmt.Sprintf("Saved device %q and set it active.\nConfig: %s", name, tools.ModbusConfigPath(ws)), IsFinal: true}, nil
}

func (h *ModbusHandler) useDevice(ws string, args []string) (Output, error) {
	if len(args) < 1 {
		cfg, err := tools.ModbusLoadConfig(ws)
		if err != nil || len(cfg.Devices) == 0 {
			return Output{Content: "Error: missing <name>\nUsage: modbus use <name>\n\nNo saved devices. Add one with modbus set.", IsFinal: true}, nil
		}
		return h.showUsageWithDevices(cfg, "Error: missing <name>\nUsage: modbus use <name>\n\nSaved devices:")
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: modbus use <name>", IsFinal: true}, nil
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return Output{Content: "Error: name is required", IsFinal: true}, nil
	}

	cfg, err := tools.ModbusLoadConfig(ws)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	if _, ok := cfg.Devices[name]; !ok {
		return Output{Content: fmt.Sprintf("Error: device %q not found", name), IsFinal: true}, nil
	}
	cfg.Active = name
	if err := tools.ModbusSaveConfig(ws, cfg); err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: fmt.Sprintf("Active device set to %q", name), IsFinal: true}, nil
}

func (h *ModbusHandler) disconnect(ws string) (Output, error) {
	cfg, err := tools.ModbusLoadConfig(ws)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	cfg.Active = ""
	if err := tools.ModbusSaveConfig(ws, cfg); err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: "Modbus device disconnected (active device cleared)", IsFinal: true}, nil
}

func (h *ModbusHandler) showInfo(ws string, args []string) (Output, error) {
	name := ""
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: modbus info [name]", IsFinal: true}, nil
	}

	cfg, err := tools.ModbusLoadConfig(ws)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	key, dc, ok := tools.ModbusResolveDevice(cfg, name)
	if !ok {
		if key == "" {
			return Output{Content: "Error: no active device configured", IsFinal: true}, nil
		}
		return Output{Content: fmt.Sprintf("Error: device %q not found", key), IsFinal: true}, nil
	}
	b, _ := json.MarshalIndent(dc, "", "  ")
	return Output{Content: fmt.Sprintf("%s:\n%s", key, string(b)), IsFinal: true}, nil
}

func (h *ModbusHandler) removeDevice(ws string, args []string) (Output, error) {
	if len(args) < 1 {
		cfg, err := tools.ModbusLoadConfig(ws)
		if err != nil || len(cfg.Devices) == 0 {
			return Output{Content: "Error: missing <name>\nUsage: modbus rm <name>\n\nNo saved devices.", IsFinal: true}, nil
		}
		return h.showUsageWithDevices(cfg, "Error: missing <name>\nUsage: modbus rm <name>\n\nSaved devices:")
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: modbus rm <name>", IsFinal: true}, nil
	}

	name := strings.TrimSpace(args[0])
	if name == "" {
		return Output{Content: "Error: name is required", IsFinal: true}, nil
	}

	cfg, err := tools.ModbusLoadConfig(ws)
	if err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	if _, ok := cfg.Devices[name]; !ok {
		return Output{Content: fmt.Sprintf("Error: device %q not found", name), IsFinal: true}, nil
	}
	delete(cfg.Devices, name)
	if cfg.Active == name {
		cfg.Active = ""
	}
	if err := tools.ModbusSaveConfig(ws, cfg); err != nil {
		return Output{Content: "Error: " + err.Error(), IsFinal: true}, nil
	}
	return Output{Content: fmt.Sprintf("Deleted device %q", name), IsFinal: true}, nil
}

func (h *ModbusHandler) showUsageWithDevices(cfg tools.ModbusWorkspaceConfig, header string) (Output, error) {
	var names []string
	for k := range cfg.Devices {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, n := range names {
		d := cfg.Devices[n]
		port := d.Port
		if port == 0 {
			port = 502
		}
		line := fmt.Sprintf("  - %s: %s:%d unit_id=%d", n, d.Host, port, d.UnitID)
		if n == cfg.Active {
			line += " [active]"
		}
		b.WriteString(line + "\n")
	}
	return Output{Content: strings.TrimRight(b.String(), "\n"), IsFinal: true}, nil
}

func (h *ModbusHandler) helpText() string {
	return `Modbus commands:
  modbus list                              List configured devices
  modbus add <name> <host> <port> <unit_id>  Add/update a device
  modbus use <name>                        Set active device
  modbus off                               Clear active device
  modbus info [name]                       Show device details
  modbus rm <name>                         Remove a device
  modbus template                          Show JSON template
  modbus path                              Show config file path`
}

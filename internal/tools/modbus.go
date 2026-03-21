package tools

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ModbusDeviceConfig represents a Modbus device configuration
type ModbusDeviceConfig struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	UnitID      int    `json:"unit_id"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
	Description string `json:"description,omitempty"`
}

// ModbusWorkspaceConfig represents the Modbus configuration for a workspace
type ModbusWorkspaceConfig struct {
	Version int                           `json:"version"`
	Active  string                        `json:"active,omitempty"`
	Devices map[string]ModbusDeviceConfig `json:"devices,omitempty"`
}

// ModbusConfigPath returns the path to the Modbus config file
func ModbusConfigPath(workspace string) string {
	return filepath.Join(workspace, ".modbus", "config.json")
}

// ModbusLoadConfig loads the Modbus configuration from a workspace
func ModbusLoadConfig(workspace string) (ModbusWorkspaceConfig, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ModbusWorkspaceConfig{}, errors.New("workspace is required")
	}
	p := ModbusConfigPath(workspace)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ModbusWorkspaceConfig{Version: 1, Devices: map[string]ModbusDeviceConfig{}}, nil
		}
		return ModbusWorkspaceConfig{}, err
	}
	if strings.TrimSpace(string(b)) == "" {
		return ModbusWorkspaceConfig{Version: 1, Devices: map[string]ModbusDeviceConfig{}}, nil
	}
	var cfg ModbusWorkspaceConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return ModbusWorkspaceConfig{}, err
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Devices == nil {
		cfg.Devices = map[string]ModbusDeviceConfig{}
	}
	cfg.Active = strings.TrimSpace(cfg.Active)
	return cfg, nil
}

// ModbusSaveConfig saves the Modbus configuration to a workspace
func ModbusSaveConfig(workspace string, cfg ModbusWorkspaceConfig) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return errors.New("workspace is required")
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Devices == nil {
		cfg.Devices = map[string]ModbusDeviceConfig{}
	}
	cfg.Active = strings.TrimSpace(cfg.Active)
	dir := filepath.Dir(ModbusConfigPath(workspace))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(ModbusConfigPath(workspace), out, 0o644)
}

// ModbusResolveDevice resolves a Modbus device by name or returns the active device
func ModbusResolveDevice(cfg ModbusWorkspaceConfig, name string) (string, ModbusDeviceConfig, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(cfg.Active)
	}
	if name == "" {
		return "", ModbusDeviceConfig{}, false
	}
	d, ok := cfg.Devices[name]
	return name, d, ok
}

var modbusGlobalTransactionID uint32

// ModbusTCPClient represents a Modbus TCP client
type ModbusTCPClient struct {
	Host      string
	Port      int
	UnitID    byte
	Timeout   time.Duration
	KeepAlive time.Duration
}

func (c ModbusTCPClient) addr() string {
	host := strings.TrimSpace(c.Host)
	return fmt.Sprintf("%s:%d", host, c.Port)
}

func (c ModbusTCPClient) do(ctx context.Context, pdu []byte) ([]byte, error) {
	if strings.TrimSpace(c.Host) == "" {
		return nil, errors.New("host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if len(pdu) == 0 {
		return nil, errors.New("pdu is required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	if c.KeepAlive > 0 {
		d.KeepAlive = c.KeepAlive
	}
	conn, err := d.DialContext(ctx, "tcp", c.addr())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	tid := uint16(atomic.AddUint32(&modbusGlobalTransactionID, 1) % 65536)
	req := make([]byte, 7+len(pdu))
	binary.BigEndian.PutUint16(req[0:2], tid)
	binary.BigEndian.PutUint16(req[2:4], 0)
	binary.BigEndian.PutUint16(req[4:6], uint16(1+len(pdu)))
	req[6] = c.UnitID
	copy(req[7:], pdu)
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}

	hdr := make([]byte, 7)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint16(hdr[0:2]) != tid {
		return nil, errors.New("transaction id mismatch")
	}
	if binary.BigEndian.Uint16(hdr[2:4]) != 0 {
		return nil, errors.New("protocol id mismatch")
	}
	if hdr[6] != c.UnitID {
		return nil, errors.New("unit id mismatch")
	}
	length := int(binary.BigEndian.Uint16(hdr[4:6]))
	if length < 2 {
		return nil, errors.New("invalid length")
	}
	pduLen := length - 1
	respPDU := make([]byte, pduLen)
	if _, err := io.ReadFull(conn, respPDU); err != nil {
		return nil, err
	}
	return respPDU, nil
}

// ModbusReadHoldingRegisters reads holding registers from a Modbus device
func ModbusReadHoldingRegisters(ctx context.Context, c ModbusTCPClient, address, quantity int) ([]uint16, error) {
	return modbusReadRegisters(ctx, c, 0x03, address, quantity)
}

// ModbusReadInputRegisters reads input registers from a Modbus device
func ModbusReadInputRegisters(ctx context.Context, c ModbusTCPClient, address, quantity int) ([]uint16, error) {
	return modbusReadRegisters(ctx, c, 0x04, address, quantity)
}

func modbusReadRegisters(ctx context.Context, c ModbusTCPClient, fn byte, address, quantity int) ([]uint16, error) {
	if quantity <= 0 || quantity > 125 {
		return nil, errors.New("quantity must be between 1 and 125")
	}
	if address < 0 || address > 0xFFFF {
		return nil, errors.New("address must be between 0 and 65535")
	}
	pdu := make([]byte, 5)
	pdu[0] = fn
	binary.BigEndian.PutUint16(pdu[1:3], uint16(address))
	binary.BigEndian.PutUint16(pdu[3:5], uint16(quantity))
	resp, err := c.do(ctx, pdu)
	if err != nil {
		return nil, err
	}
	if err := modbusCheckException(resp, fn); err != nil {
		return nil, err
	}
	if len(resp) < 2 || resp[0] != fn {
		return nil, errors.New("invalid response")
	}
	byteCount := int(resp[1])
	if byteCount != quantity*2 {
		return nil, errors.New("unexpected byte count")
	}
	if len(resp) != 2+byteCount {
		return nil, errors.New("invalid response length")
	}
	out := make([]uint16, quantity)
	for i := 0; i < quantity; i++ {
		out[i] = binary.BigEndian.Uint16(resp[2+i*2 : 2+i*2+2])
	}
	return out, nil
}

// ModbusReadCoils reads coils from a Modbus device
func ModbusReadCoils(ctx context.Context, c ModbusTCPClient, address, quantity int) ([]bool, error) {
	if quantity <= 0 || quantity > 2000 {
		return nil, errors.New("quantity must be between 1 and 2000")
	}
	if address < 0 || address > 0xFFFF {
		return nil, errors.New("address must be between 0 and 65535")
	}
	fn := byte(0x01)
	pdu := make([]byte, 5)
	pdu[0] = fn
	binary.BigEndian.PutUint16(pdu[1:3], uint16(address))
	binary.BigEndian.PutUint16(pdu[3:5], uint16(quantity))
	resp, err := c.do(ctx, pdu)
	if err != nil {
		return nil, err
	}
	if err := modbusCheckException(resp, fn); err != nil {
		return nil, err
	}
	if len(resp) < 2 || resp[0] != fn {
		return nil, errors.New("invalid response")
	}
	byteCount := int(resp[1])
	if len(resp) != 2+byteCount {
		return nil, errors.New("invalid response length")
	}
	out := make([]bool, quantity)
	for i := 0; i < quantity; i++ {
		b := resp[2+(i/8)]
		out[i] = (b & (1 << uint(i%8))) != 0
	}
	return out, nil
}

// ModbusWriteSingleRegister writes a single register value to a Modbus device
func ModbusWriteSingleRegister(ctx context.Context, c ModbusTCPClient, address, value int) (uint16, error) {
	if address < 0 || address > 0xFFFF {
		return 0, errors.New("address must be between 0 and 65535")
	}
	if value < 0 || value > 0xFFFF {
		return 0, errors.New("value must be between 0 and 65535")
	}
	fn := byte(0x06)
	pdu := make([]byte, 5)
	pdu[0] = fn
	binary.BigEndian.PutUint16(pdu[1:3], uint16(address))
	binary.BigEndian.PutUint16(pdu[3:5], uint16(value))
	resp, err := c.do(ctx, pdu)
	if err != nil {
		return 0, err
	}
	if err := modbusCheckException(resp, fn); err != nil {
		return 0, err
	}
	if len(resp) != 5 || resp[0] != fn {
		return 0, errors.New("invalid response")
	}
	return binary.BigEndian.Uint16(resp[3:5]), nil
}

// ModbusWriteMultipleRegisters writes multiple register values to a Modbus device
func ModbusWriteMultipleRegisters(ctx context.Context, c ModbusTCPClient, address int, values []uint16) (int, error) {
	if address < 0 || address > 0xFFFF {
		return 0, errors.New("address must be between 0 and 65535")
	}
	if len(values) == 0 || len(values) > 123 {
		return 0, errors.New("values length must be between 1 and 123")
	}
	fn := byte(0x10)
	quantity := len(values)
	pdu := make([]byte, 6+quantity*2)
	pdu[0] = fn
	binary.BigEndian.PutUint16(pdu[1:3], uint16(address))
	binary.BigEndian.PutUint16(pdu[3:5], uint16(quantity))
	pdu[5] = byte(quantity * 2)
	for i, v := range values {
		binary.BigEndian.PutUint16(pdu[6+i*2:6+i*2+2], v)
	}
	resp, err := c.do(ctx, pdu)
	if err != nil {
		return 0, err
	}
	if err := modbusCheckException(resp, fn); err != nil {
		return 0, err
	}
	if len(resp) != 5 || resp[0] != fn {
		return 0, errors.New("invalid response")
	}
	wrote := int(binary.BigEndian.Uint16(resp[3:5]))
	return wrote, nil
}

// ModbusWriteSingleCoil writes a single coil value to a Modbus device
func ModbusWriteSingleCoil(ctx context.Context, c ModbusTCPClient, address int, on bool) (bool, error) {
	if address < 0 || address > 0xFFFF {
		return false, errors.New("address must be between 0 and 65535")
	}
	fn := byte(0x05)
	pdu := make([]byte, 5)
	pdu[0] = fn
	binary.BigEndian.PutUint16(pdu[1:3], uint16(address))
	if on {
		binary.BigEndian.PutUint16(pdu[3:5], 0xFF00)
	} else {
		binary.BigEndian.PutUint16(pdu[3:5], 0x0000)
	}
	resp, err := c.do(ctx, pdu)
	if err != nil {
		return false, err
	}
	if err := modbusCheckException(resp, fn); err != nil {
		return false, err
	}
	if len(resp) != 5 || resp[0] != fn {
		return false, errors.New("invalid response")
	}
	v := binary.BigEndian.Uint16(resp[3:5])
	return v == 0xFF00, nil
}

func modbusCheckException(resp []byte, expectedFn byte) error {
	if len(resp) < 2 {
		return errors.New("invalid response")
	}
	fn := resp[0]
	if fn == expectedFn {
		return nil
	}
	if fn == (expectedFn | 0x80) {
		code := resp[1]
		return fmt.Errorf("modbus exception: function=0x%02X code=0x%02X", expectedFn, code)
	}
	return errors.New("unexpected function code")
}

// ModbusTCPTool is the tool wrapper for Modbus TCP operations
type ModbusTCPTool struct {
	Workspace string
}

func (t *ModbusTCPTool) Name() string { return "modbus_tcp" }
func (t *ModbusTCPTool) Description() string {
	return "Communicate with Modbus TCP devices (read/write registers/coils) and manage per-workspace Modbus device configs."
}

func (t *ModbusTCPTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []any{
					"config_get",
					"config_set_active",
					"config_upsert_device",
					"config_delete_device",
					"read_holding_registers",
					"read_input_registers",
					"read_coils",
					"write_single_register",
					"write_multiple_registers",
					"write_single_coil",
				},
			},
			"device": map[string]any{
				"type":        "string",
				"description": "Workspace device name. If empty, uses active device for Modbus actions.",
			},
			"host": map[string]any{
				"type":        "string",
				"description": "Host/IP override for Modbus actions (bypasses workspace config).",
			},
			"port": map[string]any{
				"type":        "integer",
				"description": "TCP port (default 502).",
				"minimum":     1,
				"maximum":     65535,
			},
			"unit_id": map[string]any{
				"type":        "integer",
				"description": "Modbus unit id / slave address (default 1).",
				"minimum":     0,
				"maximum":     255,
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"description": "TCP read/write deadline (default 2000ms).",
				"minimum":     100,
				"maximum":     600000,
			},
			"address": map[string]any{
				"type":        "integer",
				"description": "Start address (0-65535).",
				"minimum":     0,
				"maximum":     65535,
			},
			"quantity": map[string]any{
				"type":        "integer",
				"description": "Read quantity.",
				"minimum":     1,
				"maximum":     2000,
			},
			"value": map[string]any{
				"type":        "integer",
				"description": "Single value (register value 0-65535).",
				"minimum":     0,
				"maximum":     65535,
			},
			"values": map[string]any{
				"type":        "array",
				"description": "Multiple register values (0-65535).",
				"items": map[string]any{
					"type":    "integer",
					"minimum": 0,
					"maximum": 65535,
				},
			},
			"coil": map[string]any{
				"type":        "boolean",
				"description": "Coil state for write_single_coil.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional device description (config_upsert_device).",
			},
		},
		"required": []any{"action"},
	}
}

func (t *ModbusTCPTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}

	switch action {
	case "config_get":
		return t.handleConfigGet()
	case "config_set_active":
		return t.handleConfigSetActive(args)
	case "config_upsert_device":
		return t.handleConfigUpsertDevice(args)
	case "config_delete_device":
		return t.handleConfigDeleteDevice(args)
	}

	start := time.Now()
	client, resolved, err := t.resolveClientForAction(args)
	if err != nil {
		return "", err
	}
	deviceName := strings.TrimSpace(resolved.DeviceName)

	address, _ := args["address"].(int)
	quantity, _ := args["quantity"].(int)
	value, _ := args["value"].(int)
	valuesAny, _ := args["values"].([]any)
	coil, _ := args["coil"].(bool)

	resp := map[string]any{
		"action":     action,
		"device":     deviceName,
		"host":       client.Host,
		"port":       client.Port,
		"unit_id":    int(client.UnitID),
		"timeout_ms": int(client.Timeout / time.Millisecond),
	}

	switch action {
	case "read_holding_registers":
		if quantity == 0 {
			quantity = 1
		}
		out, err := ModbusReadHoldingRegisters(ctx, client, address, quantity)
		if err != nil {
			return "", err
		}
		vals := make([]int, 0, len(out))
		for _, v := range out {
			vals = append(vals, int(v))
		}
		resp["address"] = address
		resp["quantity"] = quantity
		resp["values"] = vals
	case "read_input_registers":
		if quantity == 0 {
			quantity = 1
		}
		out, err := ModbusReadInputRegisters(ctx, client, address, quantity)
		if err != nil {
			return "", err
		}
		vals := make([]int, 0, len(out))
		for _, v := range out {
			vals = append(vals, int(v))
		}
		resp["address"] = address
		resp["quantity"] = quantity
		resp["values"] = vals
	case "read_coils":
		if quantity == 0 {
			quantity = 1
		}
		out, err := ModbusReadCoils(ctx, client, address, quantity)
		if err != nil {
			return "", err
		}
		resp["address"] = address
		resp["quantity"] = quantity
		resp["values"] = out
	case "write_single_register":
		out, err := ModbusWriteSingleRegister(ctx, client, address, value)
		if err != nil {
			return "", err
		}
		resp["address"] = address
		resp["value"] = int(out)
	case "write_multiple_registers":
		if len(valuesAny) == 0 {
			return "", fmt.Errorf("values is required")
		}
		vals := make([]uint16, 0, len(valuesAny))
		for _, v := range valuesAny {
			switch vv := v.(type) {
			case int:
				if vv < 0 || vv > 0xFFFF {
					return "", fmt.Errorf("values must be between 0 and 65535")
				}
				vals = append(vals, uint16(vv))
			case float64:
				iv := int(vv)
				if iv < 0 || iv > 0xFFFF {
					return "", fmt.Errorf("values must be between 0 and 65535")
				}
				vals = append(vals, uint16(iv))
			default:
				return "", fmt.Errorf("values must be integers")
			}
		}
		wrote, err := ModbusWriteMultipleRegisters(ctx, client, address, vals)
		if err != nil {
			return "", err
		}
		resp["address"] = address
		resp["quantity"] = wrote
	case "write_single_coil":
		out, err := ModbusWriteSingleCoil(ctx, client, address, coil)
		if err != nil {
			return "", err
		}
		resp["address"] = address
		resp["coil"] = out
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}

	resp["duration_ms"] = time.Since(start).Milliseconds()
	b, _ := json.MarshalIndent(resp, "", "  ")
	return string(b), nil
}

type resolvedModbusTarget struct {
	DeviceName string
}

func (t *ModbusTCPTool) resolveClientForAction(args map[string]any) (ModbusTCPClient, resolvedModbusTarget, error) {
	host, _ := args["host"].(string)
	host = strings.TrimSpace(host)
	port, _ := args["port"].(int)
	unitID, _ := args["unit_id"].(int)
	timeoutMS, _ := args["timeout_ms"].(int)
	device, _ := args["device"].(string)
	device = strings.TrimSpace(device)

	if port == 0 {
		port = 502
	}
	if unitID == 0 {
		unitID = 1
	}
	if timeoutMS == 0 {
		timeoutMS = 2000
	}

	if host != "" {
		return ModbusTCPClient{
			Host:    host,
			Port:    port,
			UnitID:  byte(unitID),
			Timeout: time.Duration(timeoutMS) * time.Millisecond,
		}, resolvedModbusTarget{DeviceName: device}, nil
	}

	ws := strings.TrimSpace(t.Workspace)
	if ws == "" {
		return ModbusTCPClient{}, resolvedModbusTarget{}, fmt.Errorf("workspace is not configured; provide host/port/unit_id explicitly")
	}
	cfg, err := ModbusLoadConfig(ws)
	if err != nil {
		return ModbusTCPClient{}, resolvedModbusTarget{}, err
	}
	name, dc, ok := ModbusResolveDevice(cfg, device)
	if !ok {
		if name == "" {
			return ModbusTCPClient{}, resolvedModbusTarget{}, fmt.Errorf("no active device configured; use /modbus set or modbus_tcp config_upsert_device")
		}
		return ModbusTCPClient{}, resolvedModbusTarget{}, fmt.Errorf("device %q not found; use /modbus set or modbus_tcp config_upsert_device", name)
	}

	if strings.TrimSpace(dc.Host) == "" {
		return ModbusTCPClient{}, resolvedModbusTarget{}, fmt.Errorf("device %q has empty host", name)
	}
	if dc.Port == 0 {
		dc.Port = 502
	}
	if dc.UnitID == 0 {
		dc.UnitID = 1
	}
	if dc.TimeoutMS == 0 {
		dc.TimeoutMS = 2000
	}

	if port != 0 {
		dc.Port = port
	}
	if unitID != 0 {
		dc.UnitID = unitID
	}
	if timeoutMS != 0 {
		dc.TimeoutMS = timeoutMS
	}

	return ModbusTCPClient{
		Host:    dc.Host,
		Port:    dc.Port,
		UnitID:  byte(dc.UnitID),
		Timeout: time.Duration(dc.TimeoutMS) * time.Millisecond,
	}, resolvedModbusTarget{DeviceName: name}, nil
}

func (t *ModbusTCPTool) handleConfigGet() (string, error) {
	ws := strings.TrimSpace(t.Workspace)
	if ws == "" {
		return "", fmt.Errorf("workspace is not configured")
	}
	cfg, err := ModbusLoadConfig(ws)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b), nil
}

func (t *ModbusTCPTool) handleConfigSetActive(args map[string]any) (string, error) {
	ws := strings.TrimSpace(t.Workspace)
	if ws == "" {
		return "", fmt.Errorf("workspace is not configured")
	}
	device, _ := args["device"].(string)
	device = strings.TrimSpace(device)
	if device == "" {
		return "", fmt.Errorf("device is required")
	}
	cfg, err := ModbusLoadConfig(ws)
	if err != nil {
		return "", err
	}
	if _, ok := cfg.Devices[device]; !ok {
		return "", fmt.Errorf("device %q not found", device)
	}
	cfg.Active = device
	if err := ModbusSaveConfig(ws, cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Active Modbus device set to %q", device), nil
}

func (t *ModbusTCPTool) handleConfigUpsertDevice(args map[string]any) (string, error) {
	ws := strings.TrimSpace(t.Workspace)
	if ws == "" {
		return "", fmt.Errorf("workspace is not configured")
	}
	name, _ := args["device"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("device is required")
	}
	host, _ := args["host"].(string)
	host = strings.TrimSpace(host)
	port, _ := args["port"].(int)
	unitID, _ := args["unit_id"].(int)
	timeoutMS, _ := args["timeout_ms"].(int)
	desc, _ := args["description"].(string)
	desc = strings.TrimSpace(desc)

	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	if port == 0 {
		port = 502
	}
	if unitID == 0 {
		unitID = 1
	}

	cfg, err := ModbusLoadConfig(ws)
	if err != nil {
		return "", err
	}
	cfg.Devices[name] = ModbusDeviceConfig{
		Host:        host,
		Port:        port,
		UnitID:      unitID,
		TimeoutMS:   timeoutMS,
		Description: desc,
	}
	if cfg.Active == "" {
		cfg.Active = name
	}
	if err := ModbusSaveConfig(ws, cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Upserted Modbus device %q", name), nil
}

func (t *ModbusTCPTool) handleConfigDeleteDevice(args map[string]any) (string, error) {
	ws := strings.TrimSpace(t.Workspace)
	if ws == "" {
		return "", fmt.Errorf("workspace is not configured")
	}
	name, _ := args["device"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("device is required")
	}
	cfg, err := ModbusLoadConfig(ws)
	if err != nil {
		return "", err
	}
	if _, ok := cfg.Devices[name]; !ok {
		return fmt.Sprintf("Device %q not found", name), nil
	}
	delete(cfg.Devices, name)
	if cfg.Active == name {
		cfg.Active = ""
	}
	if err := ModbusSaveConfig(ws, cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Deleted Modbus device %q", name), nil
}

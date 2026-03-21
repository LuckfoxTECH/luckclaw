package command

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"luckclaw/internal/service"
)

// ServiceHandler is the service command handler
type ServiceHandler struct{}

// Execute executes the service command
func (h *ServiceHandler) Execute(input Input) (Output, error) {
	reg := service.GlobalRegistry()
	if err := reg.Load(); err != nil {
		return Output{Error: fmt.Errorf("failed to load service registry: %v", err), IsFinal: true}, nil
	}

	if len(input.Args) == 0 {
		return h.listServices(reg)
	}

	sub := strings.ToLower(strings.TrimSpace(input.Args[0]))
	args := input.Args[1:]

	switch sub {
	case "list", "status":
		return h.listServices(reg)
	case "info", "show":
		return h.showInfo(reg, args)
	case "start":
		return h.startService(reg, args)
	case "stop":
		return h.stopService(reg, args)
	case "add", "create":
		return h.createService(args, input.Flags)
	case "rm", "delete":
		return h.deleteService(reg, args)
	case "search":
		return h.searchServices(reg, args)
	default:
		return Output{Content: fmt.Sprintf("Error: unknown subcommand %q\n\n%s", sub, h.helpText()), IsFinal: true}, nil
	}
}

func (h *ServiceHandler) listServices(reg *service.Registry) (Output, error) {
	services := reg.List()

	var b strings.Builder
	if len(services) == 0 {
		b.WriteString("No services registered.\n")
		b.WriteString("\nUsage:\n")
		b.WriteString("  service list\n")
		b.WriteString("  service add web_design [--title <title>] [--port <port>]\n")
		b.WriteString("  service rm <id>\n")
		b.WriteString("  service info <id>\n")
		b.WriteString("  service start <id>\n")
		b.WriteString("  service stop <id>\n")
		b.WriteString("  service search [query]\n")
	} else {
		b.WriteString(fmt.Sprintf("Services (%d):\n\n", len(services)))
		for _, s := range services {
			status := "stopped"
			if s.Running {
				status = fmt.Sprintf("running (port: %d)", s.Port)
			}
			line := fmt.Sprintf("  - %s (%s) %s", s.ID, s.Type, status)
			if s.Name != "" && s.Name != s.ID {
				line += fmt.Sprintf(" - %s", s.Name)
			}
			b.WriteString(line + "\n")
			if s.Running && s.Port > 0 {
				b.WriteString(fmt.Sprintf("    URL: http://%s:%d/\n", s.Host, s.Port))
			}
		}
		b.WriteString("\nUsage:\n")
		b.WriteString("  service list\n")
		b.WriteString("  service add web_design [--title <title>] [--port <port>]\n")
		b.WriteString("  service rm <id>\n")
		b.WriteString("  service info <id>\n")
		b.WriteString("  service start <id>\n")
		b.WriteString("  service stop <id>\n")
		b.WriteString("  service search [query]\n")
	}

	return Output{
		Content: strings.TrimRight(b.String(), "\n"),
		IsFinal: true,
	}, nil
}

func (h *ServiceHandler) showInfo(reg *service.Registry, args []string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <id>\nUsage: service info <id>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: service info <id>", IsFinal: true}, nil
	}
	id := strings.TrimSpace(args[0])
	svc, ok := reg.Get(id)
	if !ok {
		return Output{Content: fmt.Sprintf("Error: service not found: %s", id), IsFinal: true}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Service: %s\n\n", svc.ID))
	b.WriteString(fmt.Sprintf("Type: %s\n", svc.Type))
	b.WriteString(fmt.Sprintf("Name: %s\n", svc.Name))
	if svc.Description != "" {
		b.WriteString(fmt.Sprintf("Description: %s\n", svc.Description))
	}
	b.WriteString(fmt.Sprintf("Directory: %s\n", svc.Dir))
	b.WriteString(fmt.Sprintf("Host: %s\n", svc.Host))
	b.WriteString(fmt.Sprintf("Port: %d\n", svc.Port))
	b.WriteString(fmt.Sprintf("Running: %v\n", svc.Running))
	b.WriteString(fmt.Sprintf("AutoStart: %v\n", svc.AutoStart))
	b.WriteString(fmt.Sprintf("Created: %s\n", svc.CreatedAt))
	if svc.Running && svc.Port > 0 {
		b.WriteString(fmt.Sprintf("\nURL: http://%s:%d/\n", svc.Host, svc.Port))
		b.WriteString(fmt.Sprintf("WS: ws://%s:%d/ws\n", svc.Host, svc.Port))
	}

	return Output{Content: b.String(), IsFinal: true}, nil
}

func (h *ServiceHandler) startService(reg *service.Registry, args []string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <id>\nUsage: service start <id>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: service start <id>", IsFinal: true}, nil
	}
	id := strings.TrimSpace(args[0])
	if _, ok := reg.Get(id); !ok {
		return Output{Content: fmt.Sprintf("Error: service not found: %s", id), IsFinal: true}, nil
	}

	if err := reg.StartService(context.Background(), id); err != nil {
		return Output{Content: fmt.Sprintf("Error: starting service: %v", err), IsFinal: true}, nil
	}
	return Output{Content: fmt.Sprintf("Service %s started.", id), IsFinal: true}, nil
}

func (h *ServiceHandler) stopService(reg *service.Registry, args []string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <id>\nUsage: service stop <id>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: service stop <id>", IsFinal: true}, nil
	}
	id := strings.TrimSpace(args[0])
	if _, ok := reg.Get(id); !ok {
		return Output{Content: fmt.Sprintf("Error: service not found: %s", id), IsFinal: true}, nil
	}

	if err := reg.StopService(id); err != nil {
		return Output{Content: fmt.Sprintf("Error: stopping service: %v", err), IsFinal: true}, nil
	}
	return Output{Content: fmt.Sprintf("Service %s stopped.", id), IsFinal: true}, nil
}

func (h *ServiceHandler) createService(args []string, flags map[string]string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <type>\nUsage: service add <type> [--title <title>] [--host <host>] [--port <port>]", IsFinal: true}, nil
	}

	svcType := strings.ToLower(args[0])
	if !service.GlobalTypeRegistry().HasType(svcType) {
		return Output{Content: fmt.Sprintf("Error: unsupported service type: %s (registered: %v)", svcType, service.GlobalTypeRegistry().RegisteredTypes()), IsFinal: true}, nil
	}

	title := flags["title"]
	if title == "" {
		title = "Web Design"
	}
	host := flags["host"]
	if host == "" {
		host = "127.0.0.1"
	}
	port := 0
	if p, ok := flags["port"]; ok {
		fmt.Sscanf(p, "%d", &port)
	}
	for i := 1; i < len(args); i++ {
		t := strings.TrimSpace(args[i])
		if t == "" {
			continue
		}
		if !strings.HasPrefix(t, "--") {
			return Output{Content: fmt.Sprintf("Error: unexpected argument %q\nUsage: service add %s [--title <title>] [--host <host>] [--port <port>]", t, svcType), IsFinal: true}, nil
		}
		key := strings.TrimPrefix(t, "--")
		if key == "" {
			return Output{Content: "Error: invalid flag\nUsage: service add " + svcType + " [--title <title>] [--host <host>] [--port <port>]", IsFinal: true}, nil
		}
		if i+1 >= len(args) {
			return Output{Content: fmt.Sprintf("Error: missing value for --%s\nUsage: service add %s [--title <title>] [--host <host>] [--port <port>]", key, svcType), IsFinal: true}, nil
		}
		val := strings.TrimSpace(args[i+1])
		switch strings.ToLower(key) {
		case "title":
			title = val
		case "host":
			host = val
		case "port":
			if val == "" {
				return Output{Content: "Error: invalid --port value\nUsage: service add " + svcType + " [--title <title>] [--host <host>] [--port <port>]", IsFinal: true}, nil
			}
			p, err := strconv.Atoi(val)
			if err != nil || p < 0 || p > 65535 {
				return Output{Content: "Error: invalid --port value\nUsage: service add " + svcType + " [--title <title>] [--host <host>] [--port <port>]", IsFinal: true}, nil
			}
			port = p
		default:
			return Output{Content: fmt.Sprintf("Error: unknown flag --%s\nUsage: service add %s [--title <title>] [--host <host>] [--port <port>]", key, svcType), IsFinal: true}, nil
		}
		i++
	}

	mgr := service.WebDesignManagerGlobal()
	s, err := mgr.Create("", title, "", host, port, true, nil)
	if err != nil {
		return Output{Content: fmt.Sprintf("Error: creating service: %v", err), IsFinal: true}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Service %s created.\n\n", s.ServiceID()))
	if s.IsRunning() && s.Port > 0 {
		b.WriteString("Status: running\n")
		b.WriteString(fmt.Sprintf("URL: http://%s:%d/\n", s.Host, s.Port))
		b.WriteString(fmt.Sprintf("WS: ws://%s:%d/ws\n", s.Host, s.Port))
	} else {
		b.WriteString("Status: stopped\n")
		b.WriteString(fmt.Sprintf("Directory: %s\n", s.Dir))
	}

	return Output{Content: b.String(), IsFinal: true}, nil
}

func (h *ServiceHandler) deleteService(reg *service.Registry, args []string) (Output, error) {
	if len(args) < 1 {
		return Output{Content: "Error: missing <id>\nUsage: service rm <id>", IsFinal: true}, nil
	}
	if len(args) > 1 {
		return Output{Content: "Error: too many arguments\nUsage: service rm <id>", IsFinal: true}, nil
	}
	id := strings.TrimSpace(args[0])
	svc, ok := reg.Get(id)
	if !ok {
		return Output{Content: fmt.Sprintf("Error: service %s not found", id), IsFinal: true}, nil
	}

	if svc.Running {
		_ = reg.StopService(id)
	}

	// Clean up type-specific resources
	if svc.Type == service.TypeWebDesign {
		mgr := service.WebDesignManagerGlobal()
		_ = mgr.Delete(id)
	}

	reg.Unregister(id)
	return Output{Content: fmt.Sprintf("Service removed: %s", id), IsFinal: true}, nil
}

func (h *ServiceHandler) searchServices(reg *service.Registry, args []string) (Output, error) {
	query := ""
	if len(args) > 0 {
		query = strings.TrimSpace(strings.Join(args, " "))
	}

	var services []*service.ServiceInfo
	if query == "" {
		services = reg.List()
	} else {
		services = reg.Search(query)
	}

	if len(services) == 0 {
		if query == "" {
			return Output{Content: "No services registered.", IsFinal: true}, nil
		}
		return Output{Content: fmt.Sprintf("No services matching: %s", query), IsFinal: true}, nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d service(s):\n\n", len(services)))
	for _, s := range services {
		status := "stopped"
		if s.Running {
			status = fmt.Sprintf("running (port: %d)", s.Port)
		}
		b.WriteString(fmt.Sprintf("  - %s (%s) %s - %s\n", s.ID, s.Type, status, s.Name))
	}

	return Output{Content: b.String(), IsFinal: true}, nil
}

func (h *ServiceHandler) helpText() string {
	return `Service commands:
  service list                List all registered services
  service info <id>           Show service details
  service start <id>          Start a service
  service stop <id>           Stop a service
  service search [query]      Search services by name/type/description
  service add web_design      Create a new web_design service
  service rm <id>             Remove a service`
}

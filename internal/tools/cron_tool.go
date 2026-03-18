package tools

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"luckclaw/internal/cron"
)

type CronTool struct {
	Service *cron.Service
}

func (t *CronTool) Name() string { return "cron" }

func (t *CronTool) Description() string {
	return "Manage scheduled tasks. Actions: add, list, remove, enable, disable."
}

func (t *CronTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "list", "remove", "enable", "disable"},
				"description": "The action to perform.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Name for the cron job (required for add).",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Message or prompt to execute (required for add).",
			},
			"cron_expr": map[string]any{
				"type":        "string",
				"description": "Cron expression (e.g. '0 9 * * *' for daily at 9am).",
			},
			"every_seconds": map[string]any{
				"type":        "number",
				"description": "Repeat every N seconds.",
			},
			"at": map[string]any{
				"type":        "string",
				"description": "ISO datetime for one-time schedule (e.g. '2025-01-15T09:00:00').",
			},
			"tz": map[string]any{
				"type":        "string",
				"description": "IANA timezone (e.g. 'Asia/Shanghai'). Only with cron_expr.",
			},
			"job_id": map[string]any{
				"type":        "string",
				"description": "Job ID (required for remove, enable, disable).",
			},
			"deliver": map[string]any{
				"type":        "boolean",
				"description": "Whether to deliver the result to a channel when the job runs.",
			},
			"reminder_only": map[string]any{
				"type":        "boolean",
				"description": "If true, only send the reminder message to the channel without invoking the agent. Use for simple reminders like 'remind me to drink water'. Default true for one-time reminders.",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Channel to deliver to (e.g. 'telegram', 'discord'). Defaults to the current channel.",
			},
			"to": map[string]any{
				"type":        "string",
				"description": "Chat ID to deliver to. Defaults to the current chat ID.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CronTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.Service == nil {
		return "", fmt.Errorf("cron (reminders) is only available in gateway mode. Start luckclaw with gateway to use cron")
	}

	action, _ := args["action"].(string)
	switch strings.ToLower(action) {
	case "add":
		return t.add(ctx, args)
	case "list":
		return t.list()
	case "remove":
		return t.remove(args)
	case "enable":
		return t.enable(args, true)
	case "disable":
		return t.enable(args, false)
	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (t *CronTool) add(ctx context.Context, args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	message, _ := args["message"].(string)
	if name == "" || message == "" {
		return "", fmt.Errorf("name and message are required for add")
	}

	deliver, _ := args["deliver"].(bool)
	reminderOnly, _ := args["reminder_only"].(bool)
	// One-time reminders (at) default reminderOnly=true: send the reminder only, do not invoke the agent.
	if _, has := args["reminder_only"]; !has && args["at"] != nil {
		reminderOnly = true
	}
	channel, _ := args["channel"].(string)
	to, _ := args["to"].(string)

	// When adding from a channel (Discord/Telegram, etc.), if channel/to is not specified, use the current chat as the default delivery target.
	if channel == "" || to == "" {
		if defCh, defTo := ChannelFromContext(ctx); defCh != "" && defTo != "" {
			if channel == "" {
				channel = defCh
			}
			if to == "" {
				to = defTo
			}
			deliver = true // Enable delivery by default when a valid target exists
			log.Printf("[cron] add: using context defaults channel=%q to=%q deliver=true", channel, to)
		} else {
			log.Printf("[cron] add: no channel context (defCh/defTo empty), deliver=%v channel=%q to=%q", deliver, channel, to)
		}
	}

	if atStr, ok := args["at"].(string); ok && atStr != "" {
		at, err := time.Parse(time.RFC3339, atStr)
		if err != nil {
			// If timezone is missing, parse in server local time to avoid treating "19:27" as UTC and scheduling incorrectly.
			at, err = time.ParseInLocation("2006-01-02T15:04:05", atStr, time.Local)
			if err != nil {
				return "", fmt.Errorf("invalid at datetime: %w", err)
			}
		}
		job, err := t.Service.AddAt(name, message, at, deliver, reminderOnly, channel, to)
		if err != nil {
			return "", err
		}
		log.Printf("[cron] add: one-time job id=%s at=%s nextRunMs=%d deliver=%v reminderOnly=%v ch=%q to=%q",
			job.ID, at.Format(time.RFC3339), job.State.NextRunAtMs, deliver, reminderOnly, channel, to)
		return fmt.Sprintf("One-time job added: %s (id: %s, at: %s, deliver: %v, reminderOnly: %v)", job.Name, job.ID, at.Format(time.RFC3339), deliver, reminderOnly), nil
	}

	if expr, ok := args["cron_expr"].(string); ok && expr != "" {
		job, err := t.Service.AddCron(name, message, expr, deliver, reminderOnly, channel, to)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Cron job added: %s (id: %s, expr: %s, deliver: %v)", job.Name, job.ID, expr, deliver), nil
	}

	if every, ok := args["every_seconds"].(float64); ok && every > 0 {
		job, err := t.Service.AddEvery(name, message, int(every), deliver, reminderOnly, channel, to)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Recurring job added: %s (id: %s, every: %ds, deliver: %v)", job.Name, job.ID, int(every), deliver), nil
	}

	return "", fmt.Errorf("one of cron_expr, every_seconds, or at is required")
}

func (t *CronTool) list() (string, error) {
	jobs, err := t.Service.List(true)
	if err != nil {
		return "", err
	}
	if len(jobs) == 0 {
		return "No scheduled jobs.", nil
	}
	var sb strings.Builder
	for _, j := range jobs {
		status := "enabled"
		if !j.Enabled {
			status = "disabled"
		}
		line := fmt.Sprintf("- %s (id: %s, %s, %s): %s",
			j.Name, j.ID, j.Schedule.Kind, status, j.Payload.Message)
		if j.Payload.Deliver {
			line += fmt.Sprintf(" [deliver → %s:%s]", j.Payload.Channel, j.Payload.To)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String(), nil
}

func (t *CronTool) remove(args map[string]any) (string, error) {
	jobID, _ := args["job_id"].(string)
	if jobID == "" {
		return "", fmt.Errorf("job_id is required for remove")
	}
	removed, err := t.Service.Remove(jobID)
	if err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("Job %s not found.", jobID), nil
	}
	return fmt.Sprintf("Job %s removed.", jobID), nil
}

func (t *CronTool) enable(args map[string]any, enabled bool) (string, error) {
	jobID, _ := args["job_id"].(string)
	if jobID == "" {
		return "", fmt.Errorf("job_id is required")
	}
	job, err := t.Service.Enable(jobID, enabled)
	if err != nil {
		return "", err
	}
	if job == nil {
		return fmt.Sprintf("Job %s not found.", jobID), nil
	}
	action := "enabled"
	if !enabled {
		action = "disabled"
	}
	return fmt.Sprintf("Job %s %s.", jobID, action), nil
}

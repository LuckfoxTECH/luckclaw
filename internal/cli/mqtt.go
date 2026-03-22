package cli

import (
	"context"
	"fmt"
	"strconv"

	"luckclaw/internal/command"

	"github.com/spf13/cobra"
)

func newMqttCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mqtt",
		Short: "Manage MQTT connections",
	}
	cmd.AddCommand(newMqttAddCmd())
	cmd.AddCommand(newMqttConnectCmd())
	cmd.AddCommand(newMqttDisconnectCmd())
	cmd.AddCommand(newMqttPublishCmd())
	cmd.AddCommand(newMqttSubscribeCmd())
	cmd.AddCommand(newMqttUnsubscribeCmd())
	cmd.AddCommand(newMqttStatusCmd())
	cmd.AddCommand(newMqttLogsCmd())
	cmd.AddCommand(newMqttSavedCmd())
	cmd.AddCommand(newMqttListCmd())
	cmd.AddCommand(newMqttRestoreCmd())
	cmd.AddCommand(newMqttRmCmd())
	cmd.AddCommand(newMqttClearCmd())
	cmd.AddCommand(newMqttClientsCmd())
	return cmd
}

func newMqttAddCmd() *cobra.Command {
	var clientID, broker, username, password string
	var clean bool

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add MQTT connection (save locally without connecting)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" || broker == "" {
				return exitf(cmd, "Error: --client-id and --broker are required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"add", clientID, broker},
				Flags:   map[string]string{"username": username, "password": password, "clean": strconv.FormatBool(clean)},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	cmd.Flags().StringVar(&broker, "broker", "", "Broker URL (required, e.g. tcp://localhost:1883)")
	cmd.Flags().StringVar(&username, "username", "", "Username (optional)")
	cmd.Flags().StringVar(&password, "password", "", "Password (optional)")
	cmd.Flags().BoolVar(&clean, "clean", true, "Clean session")
	return cmd
}

func newMqttConnectCmd() *cobra.Command {
	var clientID string

	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect to a saved MQTT connection",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" {
				return exitf(cmd, "Error: --client-id is required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"connect", clientID},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	return cmd
}

func newMqttDisconnectCmd() *cobra.Command {
	var clientID string

	cmd := &cobra.Command{
		Use:   "disconnect",
		Short: "Disconnect from MQTT broker",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" {
				return exitf(cmd, "Error: --client-id is required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"disconnect", clientID},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	return cmd
}

func newMqttPublishCmd() *cobra.Command {
	var clientID, topic, payload string
	var qos int

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish message to MQTT topic",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" || topic == "" {
				return exitf(cmd, "Error: --client-id and --topic are required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"publish", clientID, topic, payload},
				Flags:   map[string]string{"qos": strconv.Itoa(qos)},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	cmd.Flags().StringVar(&topic, "topic", "", "Topic (required)")
	cmd.Flags().StringVar(&payload, "payload", "", "Message payload")
	cmd.Flags().IntVar(&qos, "qos", 0, "QoS level (0, 1, or 2)")
	return cmd
}

func newMqttSubscribeCmd() *cobra.Command {
	var clientID, topic, alertChannel, alertTo string
	var qos, alertInterval int

	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Subscribe to MQTT topic",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" || topic == "" {
				return exitf(cmd, "Error: --client-id and --topic are required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"subscribe", clientID, topic},
				Flags:   map[string]string{"qos": strconv.Itoa(qos), "alert-channel": alertChannel, "alert-to": alertTo, "alert-interval": strconv.Itoa(alertInterval)},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	cmd.Flags().StringVar(&topic, "topic", "", "Topic (required)")
	cmd.Flags().IntVar(&qos, "qos", 0, "QoS level (0, 1, or 2)")
	cmd.Flags().StringVar(&alertChannel, "alert-channel", "", "Delivery channel for alerts (e.g. telegram, discord)")
	cmd.Flags().StringVar(&alertTo, "alert-to", "", "Target chat ID for alerts")
	cmd.Flags().IntVar(&alertInterval, "alert-interval", 0, "Minimum seconds between alerts (0 = no limit)")
	return cmd
}

func newMqttUnsubscribeCmd() *cobra.Command {
	var clientID, topic string

	cmd := &cobra.Command{
		Use:   "unsubscribe",
		Short: "Unsubscribe from MQTT topic",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" || topic == "" {
				return exitf(cmd, "Error: --client-id and --topic are required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"unsubscribe", clientID, topic},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	cmd.Flags().StringVar(&topic, "topic", "", "Topic (required)")
	return cmd
}

func newMqttStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show MQTT connection status",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"status"},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

func newMqttLogsCmd() *cobra.Command {
	var clientID, topic string
	var limit int

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show MQTT message logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clientID == "" {
				return exitf(cmd, "Error: --client-id is required")
			}
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"logs", clientID},
				Flags:   map[string]string{"topic": topic, "limit": strconv.Itoa(limit)},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Client ID (required)")
	cmd.Flags().StringVar(&topic, "topic", "", "Topic (optional, for specific topic logs)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Max number of log entries")
	return cmd
}

func newMqttSavedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "saved",
		Short: "Show saved MQTT connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"saved"},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

func newMqttListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List MQTT clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"clients"},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

func newMqttRestoreCmd() *cobra.Command {
	var clientID string
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore saved MQTT connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"restore", clientID},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client-id", "", "Restore only this client ID")
	return cmd
}

func newMqttRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove a saved MQTT connection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    append([]string{"rm"}, args...),
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

func newMqttClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear all saved MQTT connections",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"clear"},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

func newMqttClientsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clients",
		Short: "List all MQTT clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler := &command.MQTTHandler{}
			input := command.Input{
				Args:    []string{"clients"},
				Context: context.Background(),
				Writer:  cmd.OutOrStdout(),
			}
			output, err := handler.Execute(input)
			if err != nil {
				return exitf(cmd, "%v", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), output.Content)
			return nil
		},
	}
	return cmd
}

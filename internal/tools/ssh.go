package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SSHTool struct {
	TimeoutSeconds int
}

func (t *SSHTool) Name() string { return "ssh" }
func (t *SSHTool) Description() string {
	return "Run a command on a remote host via SSH. Provide host, command, and optional user/port/identity_file."
}
func (t *SSHTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host": map[string]any{
				"type":        "string",
				"description": "Remote host",
				"minLength":   1,
			},
			"user": map[string]any{
				"type":        "string",
				"description": "SSH username (optional)",
			},
			"port": map[string]any{
				"type":        "integer",
				"description": "SSH port (optional)",
				"minimum":     1,
				"maximum":     65535,
			},
			"identity_file": map[string]any{
				"type":        "string",
				"description": "Path to private key file (optional)",
			},
			"password": map[string]any{
				"type":        "string",
				"description": "SSH password (optional; prefer password_env to avoid persisting secrets)",
			},
			"password_env": map[string]any{
				"type":        "string",
				"description": "Environment variable name that contains the SSH password (optional)",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Command to run on remote host",
				"minLength":   1,
			},
			"batch_mode": map[string]any{
				"type":        "boolean",
				"description": "Use BatchMode to avoid interactive prompts (default true)",
			},
			"strict_host_key_checking": map[string]any{
				"type":        "boolean",
				"description": "Enable StrictHostKeyChecking (default false)",
			},
		},
		"required": []any{"host", "command"},
	}
}

func (t *SSHTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	host, _ := args["host"].(string)
	user, _ := args["user"].(string)
	identity, _ := args["identity_file"].(string)
	password, _ := args["password"].(string)
	passwordEnv, _ := args["password_env"].(string)
	cmdRemote, _ := args["command"].(string)
	port, _ := args["port"].(int)
	batchModeVal, _ := args["batch_mode"].(bool)
	strictHKCVal, _ := args["strict_host_key_checking"].(bool)

	host = strings.TrimSpace(host)
	user = strings.TrimSpace(user)
	identity = strings.TrimSpace(identity)
	password = strings.TrimSpace(password)
	passwordEnv = strings.TrimSpace(passwordEnv)
	cmdRemote = strings.TrimSpace(cmdRemote)

	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	if cmdRemote == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := 60 * time.Second
	if t.TimeoutSeconds > 0 {
		timeout = time.Duration(t.TimeoutSeconds) * time.Second
	}
	if strings.TrimSpace(passwordEnv) != "" && strings.TrimSpace(password) == "" {
		if v, ok := os.LookupEnv(passwordEnv); ok && strings.TrimSpace(v) != "" {
			password = strings.TrimSpace(v)
		}
	}
	conn := SSHConn{
		Host:                  host,
		User:                  user,
		Port:                  port,
		IdentityFile:          identity,
		Password:              password,
		PasswordEnv:           passwordEnv,
		BatchMode:             batchModeVal || args["batch_mode"] == nil,
		StrictHostKeyChecking: strictHKCVal,
	}
	return RunSSHCommand(ctx, conn, cmdRemote, timeout)
}

func sshTarget(c SSHConn) string {
	host := strings.TrimSpace(c.Host)
	user := strings.TrimSpace(c.User)
	if user == "" {
		return host
	}
	return user + "@" + host
}

func sshAddr(c SSHConn) (string, error) {
	host := strings.TrimSpace(c.Host)
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	port := c.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", port)), nil
}

func hostKeyCallback(c SSHConn) (ssh.HostKeyCallback, error) {
	if !c.StrictHostKeyChecking {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(home, ".ssh", "known_hosts")
	cb, err := knownhosts.New(p)
	if err != nil {
		return nil, err
	}
	return cb, nil
}

func authMethods(c SSHConn) ([]ssh.AuthMethod, error) {
	var out []ssh.AuthMethod
	if strings.TrimSpace(c.IdentityFile) != "" {
		keyPath := strings.TrimSpace(c.IdentityFile)
		b, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		var signer ssh.Signer
		signer, err = ssh.ParsePrivateKey(b)
		if err != nil && strings.TrimSpace(c.Password) != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(b, []byte(c.Password))
		}
		if err != nil {
			return nil, err
		}
		out = append(out, ssh.PublicKeys(signer))
	}

	if sock := strings.TrimSpace(os.Getenv("SSH_AUTH_SOCK")); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			ag := agent.NewClient(conn)
			signers, err := ag.Signers()
			_ = conn.Close()
			if err == nil && len(signers) > 0 {
				out = append(out, ssh.PublicKeys(signers...))
			}
		}
	}

	if strings.TrimSpace(c.Password) != "" {
		out = append(out, ssh.Password(c.Password))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no ssh auth method available (need identity_file, ssh-agent, or password)")
	}
	return out, nil
}

func dialSSH(ctx context.Context, c SSHConn, timeout time.Duration) (*ssh.Client, error) {
	addr, err := sshAddr(c)
	if err != nil {
		return nil, err
	}
	cb, err := hostKeyCallback(c)
	if err != nil {
		return nil, err
	}
	auth, err := authMethods(c)
	if err != nil {
		return nil, err
	}
	user := strings.TrimSpace(c.User)
	if user == "" {
		user = os.Getenv("USER")
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: cb,
		Timeout:         timeout,
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(cc, chans, reqs), nil
}

func RunSSHCommand(ctx context.Context, c SSHConn, remoteCommand string, timeout time.Duration) (string, error) {
	remoteCommand = strings.TrimSpace(remoteCommand)
	if remoteCommand == "" {
		return "", fmt.Errorf("command is required")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	client, err := dialSSH(ctx, c, timeout)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	err = sess.Run(remoteCommand)
	out := stdout.String()
	errOut := stderr.String()
	full := out
	if errOut != "" {
		if full != "" {
			full += "\n"
		}
		full += errOut
	}
	if len(full) > 10000 {
		full = full[:10000] + "\n...(truncated)"
	}
	if err != nil {
		return strings.TrimSpace(full), err
	}
	return full, nil
}

func UploadPath(ctx context.Context, c SSHConn, localPath string, remotePath string, recursive bool, timeout time.Duration) (string, error) {
	localPath = strings.TrimSpace(localPath)
	remotePath = strings.TrimSpace(remotePath)
	if localPath == "" || remotePath == "" {
		return "", fmt.Errorf("localPath and remotePath are required")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	client, err := dialSSH(ctx, c, timeout)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return "", err
	}
	defer sftpClient.Close()

	info, err := os.Stat(localPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if !recursive {
			return "", fmt.Errorf("localPath is a directory; set recursive=true")
		}
		return uploadDir(ctx, sftpClient, localPath, remotePath)
	}
	return uploadFile(ctx, sftpClient, localPath, remotePath)
}

func DownloadPath(ctx context.Context, c SSHConn, remotePath string, localPath string, recursive bool, timeout time.Duration) (string, error) {
	localPath = strings.TrimSpace(localPath)
	remotePath = strings.TrimSpace(remotePath)
	if localPath == "" || remotePath == "" {
		return "", fmt.Errorf("localPath and remotePath are required")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	client, err := dialSSH(ctx, c, timeout)
	if err != nil {
		return "", err
	}
	defer client.Close()
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return "", err
	}
	defer sftpClient.Close()

	st, err := sftpClient.Stat(remotePath)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		if !recursive {
			return "", fmt.Errorf("remotePath is a directory; set recursive=true")
		}
		return downloadDir(ctx, sftpClient, remotePath, localPath)
	}
	return downloadFile(ctx, sftpClient, remotePath, localPath)
}

func uploadDir(ctx context.Context, c *sftp.Client, localRoot string, remoteRoot string) (string, error) {
	remoteRoot = path.Clean(remoteRoot)
	_ = c.MkdirAll(remoteRoot)
	return "OK", filepath.WalkDir(localRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localRoot, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		rp := path.Join(remoteRoot, rel)
		if d.IsDir() {
			return c.MkdirAll(rp)
		}
		_, err = uploadFile(ctx, c, p, rp)
		return err
	})
}

func uploadFile(ctx context.Context, c *sftp.Client, localFile string, remoteFile string) (string, error) {
	remoteFile = path.Clean(remoteFile)
	_ = c.MkdirAll(path.Dir(remoteFile))
	src, err := os.Open(localFile)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := c.Create(remoteFile)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return "OK", nil
}

func downloadDir(ctx context.Context, c *sftp.Client, remoteRoot string, localRoot string) (string, error) {
	remoteRoot = path.Clean(remoteRoot)
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return "", err
	}
	w := c.Walk(remoteRoot)
	for w.Step() {
		if err := w.Err(); err != nil {
			return "", err
		}
		p := w.Path()
		rel := strings.TrimPrefix(p, remoteRoot)
		rel = strings.TrimPrefix(rel, "/")
		lp := filepath.Join(localRoot, filepath.FromSlash(rel))
		st := w.Stat()
		if st == nil {
			continue
		}
		if st.IsDir() {
			if err := os.MkdirAll(lp, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if _, err := downloadFile(ctx, c, p, lp); err != nil {
			return "", err
		}
	}
	return "OK", nil
}

func downloadFile(ctx context.Context, c *sftp.Client, remoteFile string, localFile string) (string, error) {
	remoteFile = path.Clean(remoteFile)
	if err := os.MkdirAll(filepath.Dir(localFile), 0o755); err != nil {
		return "", err
	}
	src, err := c.Open(remoteFile)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.Create(localFile)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return "OK", nil
}

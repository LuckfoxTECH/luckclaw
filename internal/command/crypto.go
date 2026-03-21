package command

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"luckclaw/internal/paths"
)

const terminalPasswordEncPrefix = "v1:"

var terminalPasswordAAD = []byte("luckclaw-terminal-password")

func EncryptTerminalPassword(plain string) (string, error) {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", nil
	}

	key, err := terminalPasswordKey(true)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plain), terminalPasswordAAD)
	out := append(append([]byte(nil), nonce...), ciphertext...)
	return terminalPasswordEncPrefix + base64.RawStdEncoding.EncodeToString(out), nil
}

func DecryptTerminalPassword(enc string) (string, error) {
	enc = strings.TrimSpace(enc)
	if enc == "" {
		return "", nil
	}
	if !strings.HasPrefix(enc, terminalPasswordEncPrefix) {
		return "", fmt.Errorf("unsupported terminal password encoding")
	}

	rawB64 := strings.TrimPrefix(enc, terminalPasswordEncPrefix)
	raw, err := base64.RawStdEncoding.DecodeString(rawB64)
	if err != nil {
		return "", fmt.Errorf("decode terminal password: %w", err)
	}

	seeds, err := terminalPasswordKeySeeds(false)
	if err != nil {
		return "", err
	}
	if len(seeds) == 0 {
		return "", fmt.Errorf("terminal password decryption unavailable (no key seed); set LUCKCLAW_TERMINAL_KEY_SEED or use password-env")
	}

	var lastErr error
	for _, seed := range seeds {
		key := terminalPasswordKeyFromSeed(seed)
		block, err := aes.NewCipher(key)
		if err != nil {
			lastErr = err
			continue
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			lastErr = err
			continue
		}
		if len(raw) < gcm.NonceSize()+1 {
			return "", fmt.Errorf("decode terminal password: invalid payload")
		}
		nonce := raw[:gcm.NonceSize()]
		ciphertext := raw[gcm.NonceSize():]
		plain, err := gcm.Open(nil, nonce, ciphertext, terminalPasswordAAD)
		if err != nil {
			lastErr = err
			continue
		}
		return string(plain), nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("decrypt terminal password: %w", lastErr)
	}
	return "", fmt.Errorf("decrypt terminal password: failed")
}

func terminalPasswordKey(createSeedFile bool) ([]byte, error) {
	seed, err := terminalPasswordKeySeed(createSeedFile)
	if err != nil {
		return nil, err
	}
	return terminalPasswordKeyFromSeed(seed), nil
}

func terminalPasswordKeyFromSeed(seed string) []byte {
	sum := sha256.Sum256([]byte("luckclaw-terminal:" + seed))
	return sum[:]
}

func terminalPasswordKeySeed(createSeedFile bool) (string, error) {
	seeds, err := terminalPasswordKeySeeds(createSeedFile)
	if err != nil {
		return "", err
	}
	if len(seeds) == 0 {
		return "", fmt.Errorf("terminal password encryption unavailable (no key seed); set LUCKCLAW_TERMINAL_KEY_SEED or use password-env")
	}
	return seeds[0], nil
}

func terminalPasswordKeySeeds(createSeedFile bool) ([]string, error) {
	if v := strings.TrimSpace(os.Getenv("LUCKCLAW_TERMINAL_KEY_SEED")); v != "" {
		return []string{v}, nil
	}

	if v := strings.TrimSpace(readSeedFile()); v != "" {
		return []string{v}, nil
	}

	if serial := strings.TrimSpace(cpuinfoSerial()); serial != "" {
		if createSeedFile {
			_ = writeSeedFile(serial)
		}
		return []string{serial}, nil
	}
	if mid := strings.TrimSpace(readFirstLine("/etc/machine-id")); mid != "" {
		if createSeedFile {
			_ = writeSeedFile(mid)
		}
		return []string{mid}, nil
	}
	if mid := strings.TrimSpace(readFirstLine("/var/lib/dbus/machine-id")); mid != "" {
		if createSeedFile {
			_ = writeSeedFile(mid)
		}
		return []string{mid}, nil
	}

	if createSeedFile {
		seed, err := generateSeed()
		if err != nil {
			return nil, err
		}
		if err := writeSeedFile(seed); err != nil {
			return nil, err
		}
		return []string{seed}, nil
	}
	return nil, nil
}

func cpuinfoSerial() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(strings.ToLower(line), "serial") {
			continue
		}
		if colon := strings.Index(line, ":"); colon >= 0 {
			key := strings.TrimSpace(line[:colon])
			if strings.EqualFold(key, "serial") {
				return strings.TrimSpace(line[colon+1:])
			}
		}
	}
	return ""
}

func readFirstLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return ""
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		return strings.TrimSpace(s[:nl])
	}
	return s
}

func seedFilePath() (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "terminal_key_seed"), nil
}

func readSeedFile() string {
	p, err := seedFilePath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writeSeedFile(seed string) error {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return fmt.Errorf("terminal seed is empty")
	}
	p, err := seedFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(seed+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func generateSeed() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b), nil
}

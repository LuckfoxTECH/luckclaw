package transcription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const groqTranscribeURL = "https://api.groq.com/openai/v1/audio/transcriptions"

// GroqTranscription transcribes audio using Groq's Whisper API.
type GroqTranscription struct {
	APIKey string
	HTTP   *http.Client
}

// Transcribe converts an audio file to text. Returns empty string on failure.
func (t *GroqTranscription) Transcribe(filePath string) (string, error) {
	apiKey := strings.TrimSpace(t.APIKey)
	if apiKey == "" {
		apiKey = os.Getenv("GROQ_API_KEY")
	}
	if apiKey == "" {
		return "", nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)

	part, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", err
	}
	if err := w.WriteField("model", "whisper-large-v3"); err != nil {
		return "", err
	}
	contentType := w.FormDataContentType()
	if err := w.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, groqTranscribeURL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	client := t.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq transcription: status=%d body=%s", resp.StatusCode, string(b))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

package vlm

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// defaultTimeout is the fallback HTTP timeout for a single VLM request.
	// Dense scanned-PDF OCR (full-page text + layout extraction) can take well
	// over a minute on slow endpoints, so this is intentionally generous and
	// can be raised further via VLM_HTTP_TIMEOUT_SECONDS.
	defaultTimeout = 180 * time.Second
	defaultMaxToks = 5000
	defaultTemp    = float32(0.1)
)

// vlmHTTPTimeout returns the HTTP client timeout for VLM requests, read from
// the VLM_HTTP_TIMEOUT_SECONDS env var when set (and positive), falling back to
// defaultTimeout otherwise. Shared by all OpenAI-compatible VLM backends.
func vlmHTTPTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("VLM_HTTP_TIMEOUT_SECONDS")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultTimeout
}

// RemoteAPIVLM implements VLM via an OpenAI-compatible chat completions API.
type RemoteAPIVLM struct {
	modelName      string
	modelID        string
	client         *openai.Client
	fallbackClient *openai.Client // built from VLM_FALLBACK_API_KEY, nil if not set
	baseURL        string
}

// NewRemoteAPIVLM creates a remote-API backed VLM instance.
func NewRemoteAPIVLM(config *Config) (*RemoteAPIVLM, error) {
	providerName := provider.ProviderName(config.Provider)
	if providerName == "" {
		providerName = provider.DetectProvider(config.BaseURL)
	}

	var apiCfg openai.ClientConfig
	if providerName == provider.ProviderAzureOpenAI {
		apiCfg = openai.DefaultAzureConfig(config.APIKey, config.BaseURL)
		apiCfg.AzureModelMapperFunc = func(model string) string {
			return model
		}
		if config.Extra != nil {
			if v, ok := config.Extra["api_version"]; ok {
				if vs, ok := v.(string); ok && vs != "" {
					apiCfg.APIVersion = vs
				}
			}
		}
	} else {
		apiCfg = openai.DefaultConfig(config.APIKey)
		if config.BaseURL != "" {
			apiCfg.BaseURL = config.BaseURL
		}
	}
	// 与 chat/remote_api.go 保持一致：尊重 WEKNORA_LLM_INSECURE_SKIP_VERIFY，
	// 用于企业内网私有 CA 签发证书的场景。
	tlsCfg := &tls.Config{
		InsecureSkipVerify: strings.EqualFold(os.Getenv("WEKNORA_LLM_INSECURE_SKIP_VERIFY"), "true"), //nolint:gosec — operator opt-in
	}
	httpClient := &http.Client{
		Timeout: vlmHTTPTimeout(),
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}

	// 注入用户自定义 HTTP header（类似 OpenAI Python SDK 的 extra_headers）
	if len(config.CustomHeaders) > 0 {
		apiCfg.HTTPClient = secutils.WrapHTTPClientWithHeaders(httpClient, config.CustomHeaders)
	} else {
		apiCfg.HTTPClient = httpClient
	}

	v := &RemoteAPIVLM{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		client:    openai.NewClientWithConfig(apiCfg),
		baseURL:   config.BaseURL,
	}

	// Build fallback client from VLM_FALLBACK_API_KEY if set and different from primary.
	if fbKey := os.Getenv("VLM_FALLBACK_API_KEY"); fbKey != "" && fbKey != config.APIKey {
		fbCfg := openai.DefaultConfig(fbKey)
		if config.BaseURL != "" {
			fbCfg.BaseURL = config.BaseURL
		}
		fbCfg.HTTPClient = httpClient
		v.fallbackClient = openai.NewClientWithConfig(fbCfg)
	}

	return v, nil
}

// Predict sends an image with a text prompt to the OpenAI-compatible API.
func (v *RemoteAPIVLM) Predict(ctx context.Context, imgBytesList [][]byte, prompt string) (string, error) {
	var parts []openai.ChatMessagePart

	// Add text prompt first
	parts = append(parts, openai.ChatMessagePart{
		Type: openai.ChatMessagePartTypeText,
		Text: prompt,
	})

	// Add images
	for _, imgBytes := range imgBytesList {
		if len(imgBytes) > 0 {
			mimeType := detectImageMIME(imgBytes)
			b64 := base64.StdEncoding.EncodeToString(imgBytes)
			dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
			parts = append(parts, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL:    dataURI,
					Detail: openai.ImageURLDetailAuto,
				},
			})
		}
	}

	req := openai.ChatCompletionRequest{
		Model: v.modelName,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:         openai.ChatMessageRoleUser,
				MultiContent: parts,
			},
		},
		MaxTokens:   defaultMaxToks,
		Temperature: defaultTemp,
	}

	totalImageSize := 0
	for _, img := range imgBytesList {
		totalImageSize += len(img)
	}
	logger.Infof(ctx, "[VLM] Calling OpenAI-compatible API, model=%s, baseURL=%s, numImages=%d, totalImageSize=%d",
		v.modelName, v.baseURL, len(imgBytesList), totalImageSize)

	resp, err := v.client.CreateChatCompletion(ctx, req)
	if err != nil {
		// On 429, try fallback key once if configured.
		if v.fallbackClient != nil && strings.Contains(err.Error(), "429") {
			logger.Warnf(ctx, "[VLM] primary key got 429, switching to fallback key")
			resp, err = v.fallbackClient.CreateChatCompletion(ctx, req)
			if err != nil {
				return "", fmt.Errorf("OpenAI VLM request (fallback): %w", err)
			}
			logger.Infof(ctx, "[VLM] fallback key succeeded")
		} else {
			return "", fmt.Errorf("OpenAI VLM request: %w", err)
		}
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("OpenAI VLM returned no choices")
	}

	content := resp.Choices[0].Message.Content
	logger.Infof(ctx, "[VLM] OpenAI response received, len=%d", len(content))
	return content, nil
}

func (v *RemoteAPIVLM) GetModelName() string { return v.modelName }
func (v *RemoteAPIVLM) GetModelID() string   { return v.modelID }

// detectImageMIME returns the MIME type for the given image bytes.
func detectImageMIME(data []byte) string {
	ct := http.DetectContentType(data)
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	return "image/png"
}

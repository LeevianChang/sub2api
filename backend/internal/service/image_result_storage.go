package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const imageResultStorageService = "s3"

var uploadImageResultBytesFunc = uploadImageResultBytes

func (s *OpenAIGatewayService) rewriteOpenAIImageResultsToStorage(ctx context.Context, body []byte) []byte {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.ImageResultStorage.Enabled || !gjson.ValidBytes(body) {
		return body
	}
	cfg := s.cfg.Gateway.ImageResultStorage
	result := gjson.GetBytes(body, "data")
	if !result.IsArray() {
		return body
	}
	rewritten := body
	result.ForEach(func(key, value gjson.Result) bool {
		idx := key.Int()
		path := fmt.Sprintf("data.%d", idx)
		storedURL := ""
		contentType := ""
		if b64 := strings.TrimSpace(value.Get("b64_json").String()); b64 != "" {
			data, mimeType, err := decodeImageResultBase64(b64)
			if err == nil {
				contentType = mimeType
				storedURL, _ = uploadImageResultBytesFunc(ctx, cfg, data, contentType)
			}
		} else if rawURL := strings.TrimSpace(value.Get("url").String()); rawURL != "" && !strings.HasPrefix(rawURL, strings.TrimRight(cfg.PublicBaseURL, "/")+"/") {
			data, mimeType, err := downloadImageResult(ctx, rawURL)
			if err == nil {
				contentType = mimeType
				storedURL, _ = uploadImageResultBytesFunc(ctx, cfg, data, contentType)
			}
		}
		if storedURL == "" {
			return true
		}
		rewritten, _ = sjson.SetBytes(rewritten, path+".url", storedURL)
		rewritten, _ = sjson.DeleteBytes(rewritten, path+".b64_json")
		if expiresAt := imageResultExpiresAt(cfg); expiresAt > 0 {
			rewritten, _ = sjson.SetBytes(rewritten, path+".expires_at", expiresAt)
		}
		if contentType != "" {
			rewritten, _ = sjson.SetBytes(rewritten, path+".mime_type", contentType)
		}
		return true
	})
	return rewritten
}

func decodeImageResultBase64(raw string) ([]byte, string, error) {
	contentType := ""
	if strings.HasPrefix(raw, "data:") {
		parts := strings.SplitN(raw, ",", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid data url")
		}
		contentType = strings.TrimPrefix(strings.TrimSuffix(parts[0], ";base64"), "data:")
		raw = parts[1]
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, "", err
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

func downloadImageResult(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download image result failed: status=%d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, openAIImageMaxDownloadBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > openAIImageMaxDownloadBytes {
		return nil, "", fmt.Errorf("downloaded image result exceeds limit")
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, nil
}

func uploadImageResultBytes(ctx context.Context, cfg config.ImageResultStorageConfig, data []byte, contentType string) (string, error) {
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	ext := imageResultExtension(contentType)
	key := imageResultObjectKey(cfg.ObjectPrefix, ext)
	payloadHash := imageResultSHA256Hex(data)
	escapedKey := escapeImageResultObjectKey(key)
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com/%s/%s", strings.TrimSpace(cfg.AccountID), strings.TrimSpace(cfg.Bucket), escapedKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signImageResultStorageRequest(req, cfg, payloadHash)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("upload image result failed: status=%d body=%s", resp.StatusCode, string(message))
	}
	return strings.TrimRight(cfg.PublicBaseURL, "/") + "/" + escapedKey, nil
}

func imageResultObjectKey(prefix string, ext string) string {
	if ext == "" {
		ext = ".bin"
	}
	random := imageResultSHA256Hex([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))[:24]
	key := fmt.Sprintf("%s/%s%s", time.Now().UTC().Format("2006/01/02"), random, ext)
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix != "" {
		key = prefix + "/" + key
	}
	return key
}

func imageResultExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return filepath.Ext(contentType)
	}
}

func imageResultExpiresAt(cfg config.ImageResultStorageConfig) int64 {
	if cfg.ExpireSeconds <= 0 {
		return 0
	}
	return time.Now().Unix() + int64(cfg.ExpireSeconds)
}

func signImageResultStorageRequest(req *http.Request, cfg config.ImageResultStorageConfig, payloadHash string) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	credentialScope := dateStamp + "/auto/" + imageResultStorageService + "/aws4_request"
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "content-type:" + req.Header.Get("Content-Type") + "\n" +
		"host:" + req.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	canonicalRequest := strings.Join([]string{req.Method, req.URL.EscapedPath(), "", canonicalHeaders, signedHeaders, payloadHash}, "\n")
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, credentialScope, imageResultSHA256Hex([]byte(canonicalRequest))}, "\n")
	signature := hex.EncodeToString(imageResultHMACSHA256(imageResultSigningKey(cfg.SecretAccessKey, dateStamp), []byte(stringToSign)))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", cfg.AccessKeyID, credentialScope, signedHeaders, signature))
}

func imageResultSigningKey(secret, dateStamp string) []byte {
	kDate := imageResultHMACSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := imageResultHMACSHA256(kDate, []byte("auto"))
	kService := imageResultHMACSHA256(kRegion, []byte(imageResultStorageService))
	return imageResultHMACSHA256(kService, []byte("aws4_request"))
}

func escapeImageResultObjectKey(key string) string {
	parts := strings.Split(key, "/")
	for idx := range parts {
		parts[idx] = url.PathEscape(parts[idx])
	}
	return strings.Join(parts, "/")
}

func imageResultSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func imageResultHMACSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// Package imagehost uploads images to Backblaze B2 through its S3-compatible
// API and returns the public URLs that Telegraph pages embed.
package imagehost

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"DojinGo/internal/config"
	"DojinGo/internal/httpclient"
)

const (
	cacheControl  = "public, max-age=31536000, immutable"
	signAlgorithm = "AWS4-HMAC-SHA256"
	signService   = "s3"
)

// B2 stores images in a Backblaze B2 bucket. Objects are content-addressed
// (named by their SHA-256), so uploading the same image twice is idempotent.
type B2 struct {
	httpClient *httpclient.Client
	endpoint   *url.URL
	region     string
	bucket     string
	keyID      string
	appKey     string
	publicURL  string
	prefix     string
	now        func() time.Time
}

func NewB2(client *httpclient.Client, cfg config.B2Config) (*B2, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Host == "" {
		return nil, fmt.Errorf("invalid b2 endpoint %q", cfg.Endpoint)
	}
	return &B2{
		httpClient: client,
		endpoint:   endpoint,
		region:     cfg.Region,
		bucket:     cfg.Bucket,
		keyID:      cfg.KeyID,
		appKey:     cfg.ApplicationKey,
		publicURL:  strings.TrimRight(cfg.PublicURL, "/"),
		prefix:     strings.Trim(cfg.PathPrefix, "/"),
		now:        time.Now,
	}, nil
}

// Upload stores one image and returns its public URL.
func (b *B2) Upload(ctx context.Context, data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("refusing to upload empty image")
	}
	sum := sha256.Sum256(data)
	payloadHash := hex.EncodeToString(sum[:])
	contentType := http.DetectContentType(data)
	key := payloadHash + extensionFor(contentType)
	if b.prefix != "" {
		key = b.prefix + "/" + key
	}

	objectURL := *b.endpoint
	objectURL.Path = "/" + b.bucket + "/" + key
	req, err := b.httpClient.NewRequest(ctx, http.MethodPut, objectURL.String(), bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Cache-Control", cacheControl)
	b.sign(req, payloadHash)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("b2 upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("b2 upload returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return b.publicURL + "/" + key, nil
}

// sign applies AWS Signature Version 4 to req.
func (b *B2) sign(req *http.Request, payloadHash string) {
	now := b.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")

	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	signed := []string{"cache-control", "content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	sort.Strings(signed)
	var canonicalHeaders strings.Builder
	for _, name := range signed {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(strings.TrimSpace(req.Header.Get(name)))
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(signed, ";")

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := date + "/" + b.region + "/" + signService + "/aws4_request"
	stringToSign := strings.Join([]string{
		signAlgorithm,
		amzDate,
		scope,
		hashHex([]byte(canonicalRequest)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+b.appKey), date)
	key = hmacSHA256(key, b.region)
	key = hmacSHA256(key, signService)
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		signAlgorithm, b.keyID, scope, signedHeaders, signature,
	))
}

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func extensionFor(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

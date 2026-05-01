package telegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"DojinGo/internal/httpclient"
)

const MaxSingleFileSize = 5 * 1024 * 1024

type Client struct {
	httpClient     *httpclient.Client
	tokens         []string
	catboxUserHash string
}

func New(client *httpclient.Client, tokens []string, catboxUserHash string) *Client {
	return &Client{
		httpClient:     client,
		tokens:         append([]string(nil), tokens...),
		catboxUserHash: strings.TrimSpace(catboxUserHash),
	}
}

func (c *Client) CreatePage(ctx context.Context, page PageCreate) (*Page, error) {
	title := truncateRunes(page.Title, 200)
	content, err := json.Marshal(page.Content)
	if err != nil {
		return nil, fmt.Errorf("marshal telegraph content: %w", err)
	}
	values := url.Values{}
	values.Set("access_token", c.randomToken())
	values.Set("title", title)
	values.Set("content", string(content))
	if page.AuthorName != "" {
		values.Set("author_name", page.AuthorName)
	}
	if page.AuthorURL != "" {
		values.Set("author_url", page.AuthorURL)
	}

	req, err := c.httpClient.NewRequest(ctx, http.MethodPost, "https://api.telegra.ph/createPage", strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegraph createPage returned %s", resp.Status)
	}

	var result apiResult[Page]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode telegraph createPage response: %w", err)
	}
	return result.unwrap()
}

func (c *Client) Upload(ctx context.Context, files [][]byte) ([]MediaInfo, error) {
	results := make([]MediaInfo, 0, len(files))
	for _, file := range files {
		if len(file) >= MaxSingleFileSize {
			return nil, fmt.Errorf("file too large for upload: %d bytes", len(file))
		}

		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("reqtype", "fileupload")
		if c.catboxUserHash != "" {
			_ = writer.WriteField("userhash", c.catboxUserHash)
		}
		contentType := http.DetectContentType(file)
		filename := catboxFilenameForContentType(contentType)
		part, err := writer.CreatePart(catboxFilePartHeader(filename, contentType))
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, bytes.NewReader(file)); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}

		req, err := c.httpClient.NewRequest(ctx, http.MethodPost, "https://catbox.moe/user/api.php", &body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", writer.FormDataContentType())

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		payload, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		rawURL := catboxURLFromResponse(resp, payload)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			logCatboxFailure(resp, payload, 2048)
			if rawURL == "" {
				return nil, fmt.Errorf("catbox upload returned %s (%s)", resp.Status, catboxResponseInfo(resp))
			}
			return nil, fmt.Errorf("catbox upload returned %s: %s", resp.Status, summarizePayload(rawURL, 512))
		}
		if rawURL == "" {
			logCatboxFailure(resp, payload, 2048)
			return nil, fmt.Errorf("catbox returned empty payload (%s)", catboxResponseInfo(resp))
		}
		if !strings.HasPrefix(rawURL, "https://files.catbox.moe/") {
			return nil, fmt.Errorf("catbox returned unexpected payload %q", summarizePayload(rawURL, 512))
		}
		results = append(results, MediaInfo{Src: rawURL})
	}
	return results, nil
}

func summarizePayload(payload string, limit int) string {
	if limit <= 0 || len(payload) <= limit {
		return payload
	}
	return payload[:limit] + "..."
}

func catboxURLFromResponse(resp *http.Response, payload []byte) string {
	rawURL := strings.TrimSpace(string(payload))
	if rawURL != "" {
		return rawURL
	}
	if resp == nil {
		return ""
	}
	for _, header := range []string{
		"Location",
		"Content-Location",
		"X-Content-Location",
		"X-URL",
		"X-Download-Url",
		"X-Redirect-Location",
	} {
		if value := strings.TrimSpace(resp.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

func catboxResponseInfo(resp *http.Response) string {
	return fmt.Sprintf(
		"content-type=%q content-length=%q server=%q",
		resp.Header.Get("Content-Type"),
		resp.Header.Get("Content-Length"),
		resp.Header.Get("Server"),
	)
}

func logCatboxFailure(resp *http.Response, payload []byte, limit int) {
	if resp == nil {
		return
	}
	headers := resp.Header.Clone()
	snippet := summarizePayload(string(payload), limit)
	log.Printf("catbox failure status=%s headers=%v body=%q", resp.Status, headers, snippet)
}

func catboxFilenameForContentType(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "image.jpg"
	case "image/png":
		return "image.png"
	case "image/gif":
		return "image.gif"
	case "image/webp":
		return "image.webp"
	default:
		return "image.bin"
	}
}

func catboxFilePartHeader(filename, contentType string) textproto.MIMEHeader {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="fileToUpload"; filename="%s"`, filename))
	header.Set("Content-Type", contentType)
	return header
}

func (c *Client) randomToken() string {
	if len(c.tokens) == 1 {
		return c.tokens[0]
	}
	return c.tokens[randInt(len(c.tokens))]
}

type apiResult[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Error       string `json:"error"`
	ErrorDetail string `json:"error_details"`
}

func (r apiResult[T]) unwrap() (*T, error) {
	if !r.OK {
		if r.ErrorDetail != "" {
			return nil, fmt.Errorf("telegraph API error: %s (%s)", r.Error, r.ErrorDetail)
		}
		return nil, fmt.Errorf("telegraph API error: %s", r.Error)
	}
	return &r.Result, nil
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

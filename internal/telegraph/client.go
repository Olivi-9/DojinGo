package telegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"DojinGo/internal/httpclient"
)

type Client struct {
	httpClient *httpclient.Client
	tokens     []string
}

func New(client *httpclient.Client, tokens []string) *Client {
	return &Client{
		httpClient: client,
		tokens:     append([]string(nil), tokens...),
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

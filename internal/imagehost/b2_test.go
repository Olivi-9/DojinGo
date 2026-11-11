package imagehost

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"DojinGo/internal/config"
	"DojinGo/internal/httpclient"
)

func TestB2UploadPutsContentAddressedObject(t *testing.T) {
	t.Parallel()

	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32))

	var gotPath, gotAuth, gotType, gotSHA string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		gotSHA = r.Header.Get("X-Amz-Content-Sha256")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := httpclient.New(&config.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewB2(client, config.B2Config{
		Endpoint:       server.URL,
		Region:         "us-west-004",
		Bucket:         "bucket",
		KeyID:          "key-id",
		ApplicationKey: "secret",
		PublicURL:      "https://img.example.com/",
		PathPrefix:     "/dojingo/",
	})
	if err != nil {
		t.Fatal(err)
	}

	src, err := host.Upload(context.Background(), png)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	key := strings.TrimPrefix(gotPath, "/bucket/")
	if !strings.HasPrefix(key, "dojingo/") || !strings.HasSuffix(key, ".png") {
		t.Fatalf("object key = %q, want dojingo/<sha256>.png", key)
	}
	if src != "https://img.example.com/"+key {
		t.Fatalf("Upload() url = %q, want public url for %q", src, key)
	}
	if strings.TrimSuffix(strings.TrimPrefix(key, "dojingo/"), ".png") != gotSHA {
		t.Fatalf("object name should be the payload sha256 %q, got key %q", gotSHA, key)
	}
	if gotType != "image/png" {
		t.Fatalf("Content-Type = %q", gotType)
	}
	if string(gotBody) != string(png) {
		t.Fatal("uploaded body does not match input")
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=key-id/") ||
		!strings.Contains(gotAuth, "/us-west-004/s3/aws4_request") ||
		!strings.Contains(gotAuth, "SignedHeaders=cache-control;content-type;host;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("Authorization header = %q", gotAuth)
	}
}

func TestB2UploadReportsErrorStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
	}))
	defer server.Close()

	client, _ := httpclient.New(&config.Config{}, nil)
	host, _ := NewB2(client, config.B2Config{Endpoint: server.URL, Region: "r", Bucket: "b", KeyID: "k", ApplicationKey: "s", PublicURL: "https://x"})
	_, err := host.Upload(context.Background(), []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("Upload() error = %v, want AccessDenied", err)
	}
}

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigFile = "config.yaml"
	DefaultCacheTTL   = 45 * 24 * time.Hour
)

type Config struct {
	Bot        BotConfig        `yaml:"bot"`
	Telegraph  TelegraphConfig  `yaml:"telegraph"`
	IPv6       IPv6Config       `yaml:"ipv6"`
	Storage    StorageConfig    `yaml:"storage"`
	Proxy      ProxyConfig      `yaml:"proxy"`
	Collectors CollectorsConfig `yaml:"collectors"`
	Whitelist  WhitelistConfig  `yaml:"whitelist"`
}

type BotConfig struct {
	Token  string  `yaml:"token"`
	Admins []int64 `yaml:"admins"`
}

type TelegraphConfig struct {
	Tokens     []string `yaml:"tokens"`
	AuthorName string   `yaml:"author_name"`
	AuthorURL  string   `yaml:"author_url"`
}

type IPv6Config struct {
	Prefix string `yaml:"prefix"`
}

type StorageConfig struct {
	Type       string `yaml:"type"`
	Path       string `yaml:"path"`
	TTLSeconds int    `yaml:"ttl"`
	MaxEntries int    `yaml:"max_entries"`
}

type ProxyConfig struct {
	Listen             ProxyListenConfig `yaml:"listen"`
	Auth               ProxyAuthConfig   `yaml:"auth"`
	RateLimitPerMinute int               `yaml:"rate_limit_per_minute"`
}

type ProxyListenConfig struct {
	HTTP   string `yaml:"http"`
	SOCKS5 string `yaml:"socks5"`
}

type ProxyAuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type CollectorsConfig struct {
	Exhentai ExhentaiConfig `yaml:"exhentai"`
	Pixiv    PixivConfig    `yaml:"pixiv"`
}

type ExhentaiConfig struct {
	IPBPassHash string `yaml:"ipb_pass_hash"`
	IPBMemberID string `yaml:"ipb_member_id"`
	Igneous     string `yaml:"igneous"`
}

type PixivConfig struct {
	Session string `yaml:"session"`
}

type WhitelistConfig struct {
	Enabled bool    `yaml:"enabled"`
	IDs     []int64 `yaml:"ids"`
}

type LegacyConfig struct {
	Base     LegacyBaseConfig     `yaml:"base"`
	Proxy    LegacyProxyConfig    `yaml:"proxy"`
	HTTP     LegacyHTTPConfig     `yaml:"http"`
	Exhentai ExhentaiConfig       `yaml:"exhentai"`
	WorkerKV LegacyWorkerKVConfig `yaml:"worker_kv"`
}

type LegacyBaseConfig struct {
	BotToken  string          `yaml:"bot_token"`
	Admins    []int64         `yaml:"admins"`
	Telegraph TelegraphConfig `yaml:"telegraph"`
}

type LegacyProxyConfig struct {
	Endpoint      string `yaml:"endpoint"`
	Authorization string `yaml:"authorization"`
}

type LegacyHTTPConfig struct {
	IPv6Prefix string `yaml:"ipv6_prefix"`
}

type LegacyWorkerKVConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Token     string `yaml:"token"`
	CacheSize int    `yaml:"cache_size"`
	ExpireSec int    `yaml:"expire_sec"`
}

func Load(path string) (*Config, error) {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv("CONFIG_FILE")
	}
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigFile
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	var legacy LegacyConfig
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("parse legacy config %q: %w", path, err)
	}

	cfg.Normalize(filepath.Dir(path), legacy)
	if err := cfg.Validate(legacy); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Normalize(configDir string, legacy LegacyConfig) {
	if c.Bot.Token == "" {
		c.Bot.Token = legacy.Base.BotToken
	}
	if len(c.Bot.Admins) == 0 && len(legacy.Base.Admins) > 0 {
		c.Bot.Admins = append([]int64(nil), legacy.Base.Admins...)
	}

	if len(c.Telegraph.Tokens) == 0 && len(legacy.Base.Telegraph.Tokens) > 0 {
		c.Telegraph = legacy.Base.Telegraph
	}

	if c.IPv6.Prefix == "" {
		c.IPv6.Prefix = legacy.HTTP.IPv6Prefix
	}

	if c.Collectors.Exhentai.IPBPassHash == "" && legacy.Exhentai.IPBPassHash != "" {
		c.Collectors.Exhentai = legacy.Exhentai
	}

	if c.Storage.Type == "" {
		c.Storage.Type = "memory"
	}
	if c.Storage.Path == "" {
		c.Storage.Path = filepath.Join(configDir, "cache")
	}
	if c.Storage.TTLSeconds <= 0 {
		if legacy.WorkerKV.ExpireSec > 0 {
			c.Storage.TTLSeconds = legacy.WorkerKV.ExpireSec
		} else {
			c.Storage.TTLSeconds = int(DefaultCacheTTL / time.Second)
		}
	}
	if c.Storage.MaxEntries <= 0 {
		c.Storage.MaxEntries = 1024
	}
	if c.Proxy.RateLimitPerMinute <= 0 {
		c.Proxy.RateLimitPerMinute = 120
	}
}

func (c *Config) Validate(legacy LegacyConfig) error {
	var errs []error

	if c.Bot.Token == "" {
		errs = append(errs, errors.New("bot.token is required"))
	}
	if len(c.Telegraph.Tokens) == 0 {
		errs = append(errs, errors.New("telegraph.tokens must contain at least one token"))
	}

	switch c.Storage.Type {
	case "memory", "file":
	default:
		errs = append(errs, fmt.Errorf("storage.type must be \"memory\" or \"file\", got %q", c.Storage.Type))
	}

	if c.Proxy.Auth.Enabled {
		if c.Proxy.Auth.Username == "" || c.Proxy.Auth.Password == "" {
			errs = append(errs, errors.New("proxy.auth username and password are required when auth is enabled"))
		}
	}

	if legacy.Proxy.Endpoint != "" || legacy.Proxy.Authorization != "" {
		errs = append(errs, errors.New("legacy Cloudflare proxy settings are no longer supported; remove proxy.endpoint and proxy.authorization"))
	}
	if legacy.WorkerKV.Endpoint != "" || legacy.WorkerKV.Token != "" {
		errs = append(errs, errors.New("legacy worker_kv settings are no longer supported; use local storage instead"))
	}

	return errors.Join(errs...)
}

func (c *Config) StorageTTL() time.Duration {
	return time.Duration(c.Storage.TTLSeconds) * time.Second
}

func (c *Config) IsAllowedUser(id int64) bool {
	if len(c.Bot.Admins) > 0 {
		for _, admin := range c.Bot.Admins {
			if admin == id {
				return true
			}
		}
	}
	if !c.Whitelist.Enabled {
		return true
	}
	for _, allowed := range c.Whitelist.IDs {
		if allowed == id {
			return true
		}
	}
	return false
}

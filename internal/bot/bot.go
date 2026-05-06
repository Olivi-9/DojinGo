package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"DojinGo/internal/config"
	"DojinGo/internal/httpclient"
	syncsvc "DojinGo/internal/sync"
	"DojinGo/internal/version"
)

type Service struct {
	cfg      *config.Config
	bot      *bot.Bot
	syncer   *syncsvc.Synchronizer
	logger   *log.Logger
	admins   map[int64]struct{}
	activeMu sync.Mutex
	active   map[int64][]activeSync
}

type activeSync struct {
	url    string
	cancel context.CancelFunc
}

func New(cfg *config.Config, syncer *syncsvc.Synchronizer, logger *log.Logger) (*Service, error) {
	if logger == nil {
		logger = log.Default()
	}
	tgClient, err := httpclient.New(cfg, nil)
	if err != nil {
		return nil, fmt.Errorf("create telegram http client: %w", err)
	}

	admins := make(map[int64]struct{}, len(cfg.Bot.Admins))
	for _, admin := range cfg.Bot.Admins {
		admins[admin] = struct{}{}
	}

	svc := &Service{
		cfg:    cfg,
		syncer: syncer,
		logger: logger,
		admins: admins,
		active: map[int64][]activeSync{},
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(svc.handleUpdate),
		bot.WithHTTPClient(30*time.Second, tgClient.HTTPClient()),
	}
	botAPI, err := bot.New(cfg.Bot.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	svc.bot = botAPI

	return svc, nil
}

var caseTitle = cases.Title(language.English)

func (s *Service) Start(ctx context.Context) error {
	s.logBotIdentity(ctx)
	s.bot.Start(ctx)
	return nil
}

func (s *Service) logBotIdentity(ctx context.Context) {
	meCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	me, err := s.bot.GetMe(meCtx)
	if err != nil {
		s.logger.Printf("bot running (getMe failed: %v)", err)
		return
	}
	if strings.TrimSpace(me.Username) == "" {
		s.logger.Printf("bot running with id %d", me.ID)
		return
	}
	s.logger.Printf("bot running with username @%s", me.Username)
}

func (s *Service) handleUpdate(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		return
	}
	s.handleMessage(ctx, update.Message)
}

func (s *Service) handleMessage(ctx context.Context, message *models.Message) {
	if message == nil {
		return
	}
	if command, args, ok := parseCommand(message.Text); ok {
		s.handleCommand(ctx, message, command, args)
		return
	}
	if strings.TrimSpace(message.Text) != "" {
		if url := syncsvc.MatchURLFromText(message.Text); url != "" {
			s.startSync(ctx, message, url)
		}
		return
	}
	if strings.TrimSpace(message.Caption) != "" {
		if url := syncsvc.MatchURLFromText(message.Caption); url != "" {
			s.startSync(ctx, message, url)
		}
	}
}

func (s *Service) handleCommand(ctx context.Context, message *models.Message, command, args string) {
	switch command {
	case "start":
		s.reply(ctx, message.Chat.ID, "Dojingo is ready.\nUse /sync <url> to download an ehentai / exhentai / nhentai / pixiv gallery into Telegraph.\nSend /help for more commands.")
	case "help":
		s.reply(ctx, message.Chat.ID, strings.Join([]string{
			"/start - bot introduction",
			"/help - show command list",
			"/sync <url> - synchronize a gallery, you can also send link directly without command",
			"/id - show your Telegram chat ID",
			"/version - show bot version",
			"/cancel - cancel your ongoing sync jobs",
			"/delete <cache-key> - admin only, remove a cache entry",
		}, "\n"))
	case "id":
		s.reply(ctx, message.Chat.ID, fmt.Sprintf("Current chat id is %d", message.Chat.ID))
	case "version":
		s.reply(ctx, message.Chat.ID, version.Version)
	case "sync":
		if args == "" {
			s.reply(ctx, message.Chat.ID, "Usage: /sync <gallery-url>")
			return
		}
		s.startSync(ctx, message, args)
	case "cancel":
		cancelled := s.cancelAll(message.Chat.ID)
		if cancelled == 0 {
			s.reply(ctx, message.Chat.ID, "No active sync jobs.")
			return
		}
		s.reply(ctx, message.Chat.ID, fmt.Sprintf("Cancelled %d sync job(s).", cancelled))
	case "delete":
		if !s.isAdmin(message.Chat.ID) {
			s.reply(ctx, message.Chat.ID, "Admin permission required.")
			return
		}
		if args == "" {
			s.reply(ctx, message.Chat.ID, "Usage: /delete <cache-key>")
			return
		}
		if err := s.syncer.DeleteCache(ctx, args); err != nil {
			s.reply(ctx, message.Chat.ID, fmt.Sprintf("Delete failed: %v", err))
			return
		}
		s.reply(ctx, message.Chat.ID, "Cache entry deleted.")
	}
}

func (s *Service) startSync(ctx context.Context, message *models.Message, rawURL string) {
	if !s.cfg.IsAllowedUser(message.Chat.ID) {
		s.reply(ctx, message.Chat.ID, "User not authorized.")
		return
	}
	if url := syncsvc.MatchURLFromURL(rawURL); url != "" {
		rawURL = url
	}

	s.logger.Printf("sync request chat=%d url=%s", message.Chat.ID, rawURL)

	statusMessage, err := s.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: message.Chat.ID,
		Text:   "Sync queued...",
	})
	if err != nil {
		s.logger.Printf("send status message failed: %v", err)
		return
	}

	jobCtx, cancel := context.WithCancel(ctx)
	s.registerSync(message.Chat.ID, rawURL, cancel)

	go func() {
		defer s.unregisterSync(message.Chat.ID, rawURL, cancel)

		lastUpdate := time.Time{}
		updateStatus := func(text string, force bool) {
			if !force && time.Since(lastUpdate) < 1500*time.Millisecond {
				return
			}
			lastUpdate = time.Now()
			_, err := s.bot.EditMessageText(jobCtx, &bot.EditMessageTextParams{
				ChatID:    message.Chat.ID,
				MessageID: statusMessage.ID,
				Text:      text,
			})
			if err != nil {
				s.logger.Printf("edit status message failed: %v", err)
			}
		}

		updateStatus("Starting synchronization...", true)
		finalURL, err := s.syncer.Sync(jobCtx, rawURL, func(stage string, done, total int) {
			label := stage
			if total > 0 {
				updateStatus(fmt.Sprintf("%s: %d/%d", caseTitle.String(label), done, total), false)
				return
			}
			updateStatus(caseTitle.String(label), false)
		})

		_, delErr := s.bot.DeleteMessage(jobCtx, &bot.DeleteMessageParams{
			ChatID:    message.Chat.ID,
			MessageID: statusMessage.ID,
		})
		if delErr != nil {
			s.logger.Printf("delete status message failed: %v", delErr)
		}

		if err != nil {
			s.logger.Printf("sync failed chat=%d url=%s err=%v", message.Chat.ID, rawURL, err)
			s.reply(jobCtx, message.Chat.ID, fmt.Sprintf("Sync failed: %v", err))
			return
		}
		s.logger.Printf("sync done chat=%d url=%s", message.Chat.ID, rawURL)
		s.reply(jobCtx, message.Chat.ID, finalURL)
	}()
}

func (s *Service) registerSync(chatID int64, url string, cancel context.CancelFunc) {
	s.activeMu.Lock()
	s.active[chatID] = append(s.active[chatID], activeSync{url: url, cancel: cancel})
	s.activeMu.Unlock()
}

func (s *Service) unregisterSync(chatID int64, url string, cancel context.CancelFunc) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()

	jobs := s.active[chatID]
	filtered := jobs[:0]
	for _, job := range jobs {
		if job.url == url && fmt.Sprintf("%p", job.cancel) == fmt.Sprintf("%p", cancel) {
			continue
		}
		filtered = append(filtered, job)
	}
	if len(filtered) == 0 {
		delete(s.active, chatID)
		return
	}
	s.active[chatID] = filtered
}

func (s *Service) cancelAll(chatID int64) int {
	s.activeMu.Lock()
	jobs := s.active[chatID]
	delete(s.active, chatID)
	s.activeMu.Unlock()

	for _, job := range jobs {
		job.cancel()
	}
	return len(jobs)
}

func (s *Service) reply(ctx context.Context, chatID int64, text string) {
	if _, err := s.bot.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text}); err != nil {
		s.logger.Printf("send message failed: %v", err)
	}
}

func (s *Service) isAdmin(chatID int64) bool {
	_, ok := s.admins[chatID]
	return ok
}

func parseCommand(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", "", false
	}
	commandToken := fields[0]
	command := strings.TrimPrefix(commandToken, "/")
	if command == "" {
		return "", "", false
	}
	if idx := strings.Index(command, "@"); idx >= 0 {
		command = command[:idx]
	}
	args := strings.TrimSpace(strings.TrimPrefix(trimmed, commandToken))
	return strings.ToLower(command), args, true
}

package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"DojinGo/internal/config"
	syncsvc "DojinGo/internal/sync"
	"DojinGo/internal/version"
)

type Service struct {
	cfg      *config.Config
	bot      *tgbotapi.BotAPI
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
	botAPI, err := tgbotapi.NewBotAPI(cfg.Bot.Token)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}

	admins := make(map[int64]struct{}, len(cfg.Bot.Admins))
	for _, admin := range cfg.Bot.Admins {
		admins[admin] = struct{}{}
	}

	return &Service{
		cfg:    cfg,
		bot:    botAPI,
		syncer: syncer,
		logger: logger,
		admins: admins,
		active: map[int64][]activeSync{},
	}, nil
}

func (s *Service) Start(ctx context.Context) error {
	s.logger.Print("bot start")
	updateConfig := tgbotapi.NewUpdate(0)
	updateConfig.Timeout = 30

	updates := s.bot.GetUpdatesChan(updateConfig)
	defer s.bot.StopReceivingUpdates()

	for {
		select {
		case <-ctx.Done():
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			if update.Message == nil {
				continue
			}
			s.handleMessage(ctx, update.Message)
		}
	}
}

func (s *Service) handleMessage(ctx context.Context, message *tgbotapi.Message) {
	switch {
	case message.IsCommand():
		s.handleCommand(ctx, message)
	case strings.TrimSpace(message.Text) != "":
		if url := syncsvc.MatchURLFromText(message.Text); url != "" {
			s.startSync(ctx, message, url)
		}
	case strings.TrimSpace(message.Caption) != "":
		if url := syncsvc.MatchURLFromText(message.Caption); url != "" {
			s.startSync(ctx, message, url)
		}
	}
}

func (s *Service) handleCommand(ctx context.Context, message *tgbotapi.Message) {
	command := strings.ToLower(message.Command())
	args := strings.TrimSpace(message.CommandArguments())

	switch command {
	case "start":
		s.reply(message.Chat.ID, "eh2telegraph is ready.\nUse /sync <url> to mirror an EH/EX/NH gallery into Telegraph.")
	case "help":
		s.reply(message.Chat.ID, strings.Join([]string{
			"/start - bot introduction",
			"/help - show command help",
			"/sync <url> - synchronize a gallery",
			"/id - show your Telegram chat ID",
			"/version - show bot version",
			"/cancel - cancel your ongoing sync jobs",
			"/delete <cache-key> - admin only, remove a cache entry",
		}, "\n"))
	case "id":
		s.reply(message.Chat.ID, fmt.Sprintf("Current chat id is %d", message.Chat.ID))
	case "version":
		s.reply(message.Chat.ID, version.Version)
	case "sync":
		if args == "" {
			s.reply(message.Chat.ID, "Usage: /sync <gallery-url>")
			return
		}
		s.startSync(ctx, message, args)
	case "cancel":
		cancelled := s.cancelAll(message.Chat.ID)
		if cancelled == 0 {
			s.reply(message.Chat.ID, "No active sync jobs.")
			return
		}
		s.reply(message.Chat.ID, fmt.Sprintf("Cancelled %d sync job(s).", cancelled))
	case "delete":
		if !s.isAdmin(message.Chat.ID) {
			s.reply(message.Chat.ID, "Admin permission required.")
			return
		}
		if args == "" {
			s.reply(message.Chat.ID, "Usage: /delete <cache-key>")
			return
		}
		if err := s.syncer.DeleteCache(ctx, args); err != nil {
			s.reply(message.Chat.ID, fmt.Sprintf("Delete failed: %v", err))
			return
		}
		s.reply(message.Chat.ID, "Cache entry deleted.")
	}
}

func (s *Service) startSync(ctx context.Context, message *tgbotapi.Message, rawURL string) {
	if !s.cfg.IsAllowedUser(message.Chat.ID) {
		s.reply(message.Chat.ID, "User not authorized.")
		return
	}
	if url := syncsvc.MatchURLFromURL(rawURL); url != "" {
		rawURL = url
	}

	s.logger.Printf("sync request chat=%d url=%s", message.Chat.ID, rawURL)

	statusMessage, err := s.bot.Send(tgbotapi.NewMessage(message.Chat.ID, "Sync queued..."))
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
			edit := tgbotapi.NewEditMessageText(message.Chat.ID, statusMessage.MessageID, text)
			if _, err := s.bot.Send(edit); err != nil {
				s.logger.Printf("edit status message failed: %v", err)
			}
		}

		updateStatus("Starting synchronization...", true)
		finalURL, err := s.syncer.Sync(jobCtx, rawURL, func(stage string, done, total int) {
			label := stage
			if total > 0 {
				updateStatus(fmt.Sprintf("%s: %d/%d", strings.Title(label), done, total), false)
				return
			}
			updateStatus(strings.Title(label), false)
		})

		deleteConfig := tgbotapi.NewDeleteMessage(message.Chat.ID, statusMessage.MessageID)
		if _, err := s.bot.Request(deleteConfig); err != nil {
			s.logger.Printf("delete status message failed: %v", err)
		}

		if err != nil {
			s.logger.Printf("sync failed chat=%d url=%s err=%v", message.Chat.ID, rawURL, err)
			s.reply(message.Chat.ID, fmt.Sprintf("Sync failed: %v", err))
			return
		}
		s.logger.Printf("sync done chat=%d url=%s", message.Chat.ID, rawURL)
		s.reply(message.Chat.ID, finalURL)
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

func (s *Service) reply(chatID int64, text string) {
	if _, err := s.bot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		s.logger.Printf("send message failed: %v", err)
	}
}

func (s *Service) isAdmin(chatID int64) bool {
	_, ok := s.admins[chatID]
	return ok
}

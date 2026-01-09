package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"telegram-bot/agent"
	"telegram-bot/config"
	"telegram-bot/tools"
)

func main() {
	cfg := config.Load()

	if cfg.TelegramToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is required")
	}

	// Set up context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
	}()

	// Set up tool registry
	registry := tools.NewRegistry()
	registry.Register(&tools.TimeTool{})

	// Set up Python and Bash tools (share the same workspace)
	pythonTool := tools.NewPythonTool(cfg.PythonWorkspace)
	if err := pythonTool.Init(); err != nil {
		log.Printf("Workspace warning: %v", err)
	} else {
		log.Printf("Workspace: %s", cfg.PythonWorkspace)
	}
	registry.Register(pythonTool)
	registry.Register(tools.NewBashTool(cfg.PythonWorkspace))

	// Set up scrape tool (uses Ollama for summarization)
	registry.Register(tools.NewScrapeTool(cfg.OllamaURL, cfg.OllamaModel))

	// Set up OCI registry tool
	registry.Register(tools.NewOCITool())

	// Set up calendar tool
	calendarTool := tools.NewCalendarTool(
		cfg.GoogleClientID,
		cfg.GoogleSecret,
		cfg.GoogleRedirectURL,
		cfg.GoogleTokenFile,
	)
	if authURL, err := calendarTool.Init(ctx); err != nil {
		log.Printf("Calendar init warning: %v", err)
	} else if authURL != "" {
		log.Printf("Calendar needs authentication. Use /auth command in the bot.")
	} else {
		log.Printf("Calendar authenticated successfully")
	}
	registry.Register(calendarTool)

	// Create agent
	chatAgent := agent.New(cfg.OllamaModel, cfg.OllamaURL, registry)

	// Create Telegram bot
	bot, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)
	log.Printf("Registered tools: %d", len(registry.All()))

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			log.Println("Bot stopped")
			return
		case update := <-updates:
			if update.Message == nil {
				continue
			}

			go handleMessage(ctx, bot, chatAgent, calendarTool, cfg, update.Message)
		}
	}
}

func handleMessage(
	ctx context.Context,
	bot *tgbotapi.BotAPI,
	chatAgent *agent.Agent,
	calendarTool *tools.CalendarTool,
	cfg *config.Config,
	message *tgbotapi.Message,
) {
	log.Printf("[%s] %s", message.From.UserName, message.Text)

	var reply string

	switch message.Command() {
	case "start":
		reply = "👋 Hello! I'm an AI assistant powered by " + cfg.OllamaModel + ".\n\n" +
			"I can:\n• Tell you the time\n• Check your Google Calendar\n• Write and execute Python/Bash code\n• Scrape and summarize websites\n• Interact with container registries (OCI)\n\n" +
			"Use /auth to connect your Google Calendar."

	case "help":
		reply = "Available commands:\n" +
			"/start - Start the bot\n" +
			"/help - Show this help message\n" +
			"/auth - Connect Google Calendar\n" +
			"/authcode <code> - Complete Google auth\n\n" +
			"Or just ask me things like:\n" +
			"• \"What's on my calendar today?\"\n" +
			"• \"What tools do I have available?\"\n" +
			"• \"Write a Python script to calculate pi\"\n" +
			"• \"Summarize https://example.com\""

	case "auth":
		authURL, err := calendarTool.Init(ctx)
		if err != nil {
			reply = "⚠️ " + err.Error()
		} else if authURL == "" {
			reply = "✅ Google Calendar is already connected!"
		} else {
			reply = "🔐 To connect Google Calendar:\n\n" +
				"1. Click this link:\n" + authURL + "\n\n" +
				"2. Sign in and authorize access\n\n" +
				"3. Copy the code you receive\n\n" +
				"4. Send: /authcode YOUR_CODE"
		}

	case "authcode":
		code := strings.TrimSpace(message.CommandArguments())
		if code == "" {
			reply = "Please provide the authorization code: /authcode YOUR_CODE"
		} else {
			if err := calendarTool.CompleteAuth(ctx, code); err != nil {
				reply = "❌ Authentication failed: " + err.Error()
			} else {
				reply = "✅ Google Calendar connected! Try asking \"What's on my calendar?\""
			}
		}

	case "":
		// Not a command, send to agent
		response, err := chatAgent.Chat(ctx, message.Text)
		if err != nil {
			log.Printf("Agent error: %v", err)
			reply = "Sorry, I couldn't process that. Make sure Ollama is running."
		} else {
			reply = response
		}

	default:
		reply = "Unknown command. Try /help"
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, reply)
	msg.ReplyToMessageID = message.MessageID

	if _, err := bot.Send(msg); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

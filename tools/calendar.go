package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// CalendarTool provides access to Google Calendar.
type CalendarTool struct {
	config    *oauth2.Config
	tokenFile string

	mu      sync.RWMutex
	service *calendar.Service
}

// NewCalendarTool creates a new calendar tool with OAuth credentials.
func NewCalendarTool(clientID, clientSecret, redirectURL, tokenFile string) *CalendarTool {
	return &CalendarTool{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{calendar.CalendarReadonlyScope},
			Endpoint:     google.Endpoint,
		},
		tokenFile: tokenFile,
	}
}

// Init initializes the Google Calendar service.
// Returns an auth URL if user needs to authenticate, empty string if already authenticated.
func (c *CalendarTool) Init(ctx context.Context) (authURL string, err error) {
	if c.config.ClientID == "" || c.config.ClientSecret == "" {
		return "", fmt.Errorf("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required")
	}

	token, err := c.tokenFromFile()
	if err != nil {
		// No token, need to authenticate
		return c.config.AuthCodeURL("state-token", oauth2.AccessTypeOffline), nil
	}

	client := c.config.Client(ctx, token)
	service, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("creating calendar service: %w", err)
	}

	c.mu.Lock()
	c.service = service
	c.mu.Unlock()

	return "", nil
}

// CompleteAuth finishes the OAuth flow with the authorization code.
func (c *CalendarTool) CompleteAuth(ctx context.Context, authCode string) error {
	token, err := c.config.Exchange(ctx, authCode)
	if err != nil {
		return fmt.Errorf("exchanging auth code: %w", err)
	}

	if err := c.saveToken(token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}

	client := c.config.Client(ctx, token)
	service, err := calendar.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("creating calendar service: %w", err)
	}

	c.mu.Lock()
	c.service = service
	c.mu.Unlock()

	return nil
}

func (c *CalendarTool) Name() string {
	return "get_calendar_events"
}

func (c *CalendarTool) Description() string {
	return "Get upcoming events from the user's Google Calendar. Can specify how many events to retrieve (default 10) and how many days ahead to look (default 7)."
}

func (c *CalendarTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of events to return (default 10, max 50)",
			},
			"days_ahead": map[string]any{
				"type":        "integer",
				"description": "How many days ahead to look for events (default 7)",
			},
		},
		"required": []string{},
	}
}

func (c *CalendarTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	c.mu.RLock()
	service := c.service
	c.mu.RUnlock()

	if service == nil {
		return "Calendar not authenticated. Please use /auth to connect your Google Calendar.", nil
	}

	maxResults := int64(10)
	if v, ok := args["max_results"].(float64); ok {
		maxResults = int64(v)
		if maxResults > 50 {
			maxResults = 50
		}
	}

	daysAhead := 7
	if v, ok := args["days_ahead"].(float64); ok {
		daysAhead = int(v)
	}

	now := time.Now()
	timeMin := now.Format(time.RFC3339)
	timeMax := now.AddDate(0, 0, daysAhead).Format(time.RFC3339)

	events, err := service.Events.List("primary").
		Context(ctx).
		ShowDeleted(false).
		SingleEvents(true).
		TimeMin(timeMin).
		TimeMax(timeMax).
		MaxResults(maxResults).
		OrderBy("startTime").
		Do()
	if err != nil {
		return "", fmt.Errorf("retrieving events: %w", err)
	}

	if len(events.Items) == 0 {
		return "No upcoming events found.", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d upcoming events:\n\n", len(events.Items)))

	for _, item := range events.Items {
		start := item.Start.DateTime
		if start == "" {
			start = item.Start.Date // All-day event
		}

		var timeStr string
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			timeStr = t.Format("Mon Jan 2, 3:04 PM")
		} else {
			timeStr = start
		}

		result.WriteString(fmt.Sprintf("• %s - %s\n", timeStr, item.Summary))
		if item.Location != "" {
			result.WriteString(fmt.Sprintf("  📍 %s\n", item.Location))
		}
	}

	return result.String(), nil
}

func (c *CalendarTool) tokenFromFile() (*oauth2.Token, error) {
	f, err := os.Open(c.tokenFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)
	return token, err
}

func (c *CalendarTool) saveToken(token *oauth2.Token) error {
	f, err := os.Create(c.tokenFile)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(token)
}

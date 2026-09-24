package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultFeedURL = "https://feeds.everbridge.net/feeds/453003085617722/rss/rss.xml"
	defaultLog     = "/log/log.txt"
	pollInterval   = 5 * time.Minute
)

var logFile = defaultLog
var feedURL = defaultFeedURL

func init() {
	if v := os.Getenv("LOG_FILE"); v != "" {
		logFile = v
	}
}

type RSS struct {
	Channel struct {
		Items []Item `xml:"item"`
	} `xml:"channel"`
}

type Item struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Author      string `xml:"author"`
}

func main() {
	matrixURL := os.Getenv("MATRIX_URL")
	token := os.Getenv("MATRIX_TOKEN")
	roomID := os.Getenv("MATRIX_ROOM")
	slackWebhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	if matrixURL == "" || token == "" || roomID == "" {
		log.Fatal("MATRIX_URL, MATRIX_TOKEN, and MATRIX_ROOM environment variables are required")
	}

	for {
		if err := poll(matrixURL, token, roomID, slackWebhookURL); err != nil {
			log.Printf("poll error: %v", err)
		}
		time.Sleep(pollInterval)
	}
}

func poll(matrixURL, token, roomID, slackWebhookURL string) error {
	resp, err := http.Get(feedURL)
	if err != nil {
		return fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read feed: %w", err)
	}

	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil {
		return fmt.Errorf("parse feed: %w", err)
	}

	seen := loadSeen()

	for _, item := range rss.Channel.Items {
		if !strings.Contains(item.Author, "[English]") {
			continue
		}
		if seen[item.PubDate] {
			continue
		}

		msg := item.Title + "\n" + truncateMessage(item.Description)
		if slackWebhookURL != "" {
			if err := sendSlack(slackWebhookURL, msg); err != nil {
				return fmt.Errorf("send slack: %w", err)
			}
			if err := sendMatrix(matrixURL, token, roomID, msg); err != nil {
				log.Printf("send matrix: %v", err)
			}
		} else if err := sendMatrix(matrixURL, token, roomID, msg); err != nil {
			return fmt.Errorf("send matrix: %w", err)
		}
		log.Printf("sent: %s", item.Title)

		if err := appendSeen(item.PubDate); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
	}

	return nil
}

func sendSlack(webhookURL, text string) error {
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Slack API %d", resp.StatusCode)
	}
	return nil
}

func sendMatrix(matrixURL, token, roomID, text string) error {
	txnID := fmt.Sprintf("%d", time.Now().UnixNano())
	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		strings.TrimRight(matrixURL, "/"), roomID, txnID)

	payload, _ := json.Marshal(map[string]string{
		"msgtype": "m.text",
		"body":    text,
	})

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("matrix API %d: %s", resp.StatusCode, body)
	}
	return nil
}

func truncateMessage(s string) string {
	if i := strings.Index(s, "To view this message"); i != -1 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func loadSeen() map[string]bool {
	seen := make(map[string]bool)
	f, err := os.Open(logFile)
	if err != nil {
		return seen
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			seen[line] = true
		}
	}
	return seen
}

func appendSeen(pubDate string) error {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, pubDate)
	return err
}

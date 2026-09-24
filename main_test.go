package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPollSendsMatrixAndSlack(t *testing.T) {
	var matrixCalls, slackCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/feed"):
			fmt.Fprint(w, testFeed())
		case strings.Contains(r.URL.Path, "/send/"):
			atomic.AddInt32(&matrixCalls, 1)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/slack":
			atomic.AddInt32(&slackCalls, 1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestLog(t)
	oldFeedURL := feedURL
	feedURL = server.URL + "/feed"
	t.Cleanup(func() { feedURL = oldFeedURL })

	if err := poll(server.URL, "token", "room", server.URL+"/slack"); err != nil {
		t.Fatal(err)
	}
	if matrixCalls != 1 || slackCalls != 1 {
		t.Fatalf("matrix calls = %d, slack calls = %d", matrixCalls, slackCalls)
	}
}

func TestPollMatrixOnlyWhenSlackUnconfigured(t *testing.T) {
	var matrixCalls, slackCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/feed":
			fmt.Fprint(w, testFeed())
		case strings.Contains(r.URL.Path, "/send/"):
			atomic.AddInt32(&matrixCalls, 1)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/slack":
			atomic.AddInt32(&slackCalls, 1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestLog(t)
	oldFeedURL := feedURL
	feedURL = server.URL + "/feed"
	t.Cleanup(func() { feedURL = oldFeedURL })

	if err := poll(server.URL, "token", "room", ""); err != nil {
		t.Fatal(err)
	}
	if matrixCalls != 1 || slackCalls != 0 {
		t.Fatalf("matrix calls = %d, slack calls = %d", matrixCalls, slackCalls)
	}
}

func TestPollSlackFailureDoesNotPreventMatrixOrLeakWebhook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/feed":
			fmt.Fprint(w, testFeed())
		case strings.Contains(r.URL.Path, "/send/"):
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/slack":
			w.WriteHeader(http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestLog(t)
	oldFeedURL := feedURL
	feedURL = server.URL + "/feed"
	t.Cleanup(func() { feedURL = oldFeedURL })

	if err := poll(server.URL, "token", "room", server.URL+"/slack"); err != nil {
		t.Fatal(err)
	}
	if err := sendSlack(server.URL+"/slack", "test"); err == nil || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("unexpected Slack error: %v", err)
	}
}

func withTestLog(t *testing.T) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "seen-")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	oldLogFile := logFile
	logFile = file.Name()
	t.Cleanup(func() { logFile = oldLogFile })
}

func testFeed() string {
	return `<rss><channel><item><title>Test alert</title><description>Details</description><pubDate>Thu, 24 Sep 2026 12:00:00 GMT</pubDate><author>NotifyNYC [English]</author></item></channel></rss>`
}

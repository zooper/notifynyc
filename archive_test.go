package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestArchive(t *testing.T) *Archive {
	t.Helper()
	a, err := openArchive(filepath.Join(t.TempDir(), "private", "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	return a
}
func archiveItems(t *testing.T, a *Archive, items ...Item) {
	t.Helper()
	if err := a.ingest(context.Background(), items, time.Now()); err != nil {
		t.Fatal(err)
	}
}
func TestArchiveRestartIdempotenceRevisionsAndFullBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "history.sqlite")
	a, err := openArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	item := Item{GUID: "id-1", Title: "Flood", Description: "<p>Danger</p> To view this message: https://example.org/full", RawXML: "<description><![CDATA[full original]]></description>", Author: "NYC [English]", PubDate: "Fri, 30 Oct 2026 12:00:00 GMT", Link: "https://example.org/1"}
	archiveItems(t, a, item, item)
	a.db.Close()
	a, err = openArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	archiveItems(t, a, item)
	ns, err := a.search(context.Background(), "To view this message", 0, 10)
	if err != nil || len(ns) != 1 {
		t.Fatalf("restart: %v %v", ns, err)
	}
	if ns[0].Body != item.Description || ns[0].RawXML != item.RawXML || ns[0].GUID != item.GUID || ns[0].Revision != 1 {
		t.Fatalf("full preservation: %+v", ns[0])
	}
	item.Description = "Revised warning"
	archiveItems(t, a, item)
	item.Description = "<p>Danger</p> To view this message: https://example.org/full"
	archiveItems(t, a, item)
	ns, err = a.search(context.Background(), "", 0, 10)
	if err != nil || len(ns) != 3 || ns[2].Revision != 3 || ns[0].Hash != ns[2].Hash {
		t.Fatalf("A-B-A revisions: %+v %v", ns, err)
	}
	status, err := a.status(context.Background())
	if err != nil || status["activated_at"] == "" || status["last_successful_poll"] == "" {
		t.Fatalf("status %+v %v", status, err)
	}
}
func TestArchiveFallbackAndLanguage(t *testing.T) {
	a := newTestArchive(t)
	item := Item{Title: "Alert", Author: "NYC [English]", PubDate: "same-time"}
	other := item
	other.Title = "Distinct"
	archiveItems(t, a, item, other, Item{Author: "NYC [Spanish]"})
	ns, err := a.search(context.Background(), "", 0, 10)
	if err != nil || len(ns) != 2 {
		t.Fatalf("%+v %v", ns, err)
	}
	if ns[0].IdentityKind != "publication-author-title" || ns[0].SourceID == ns[1].SourceID {
		t.Fatal("fallback identity")
	}
}
func TestArchiveFailureAtomicAndRecoverable(t *testing.T) {
	a := newTestArchive(t)
	item := Item{GUID: "first", Author: "[English]"}
	archiveItems(t, a, item)
	_, err := a.db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON notices WHEN NEW.guid='bad' BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	err = a.ingest(context.Background(), []Item{{GUID: "second", Author: "[English]"}, {GUID: "bad", Author: "[English]"}}, time.Now())
	if err == nil {
		t.Fatal("expected failure")
	}
	ns, _ := a.search(context.Background(), "", 0, 10)
	if len(ns) != 1 {
		t.Fatal("partial batch committed")
	}
	st, _ := a.status(context.Background())
	if st["last_failure"] != "archive_write_failed" {
		t.Fatal(st)
	}
	archiveItems(t, a, Item{GUID: "second", Author: "[English]"})
	st, _ = a.status(context.Background())
	if st["last_failure"] != "" {
		t.Fatal(st)
	}
}
func setupArchivePoll(t *testing.T, a *Archive, feed string, slackCode int) (string, *int32) {
	t.Helper()
	var deliveries int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed" {
			fmt.Fprint(w, feed)
			return
		}
		atomic.AddInt32(&deliveries, 1)
		if r.URL.Path == "/slack" {
			w.WriteHeader(slackCode)
		}
	}))
	t.Cleanup(server.Close)
	withTestLog(t)
	oldFeed, oldHistory, oldOnly := feedURL, history, archiveOnly
	feedURL = server.URL + "/feed"
	history = a
	archiveOnly = false
	t.Cleanup(func() { feedURL = oldFeed; history = oldHistory; archiveOnly = oldOnly })
	return server.URL, &deliveries
}
func TestPollArchivesSeenWithoutReplayAndArchiveOnlyNeverDelivers(t *testing.T) {
	a := newTestArchive(t)
	u, calls := setupArchivePoll(t, a, testFeed(), 200)
	if err := appendSeen("Thu, 24 Sep 2026 12:00:00 GMT"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(logFile)
	if err := poll(u, "token", "room", u+"/slack"); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Fatal("historical delivery replay")
	}
	ns, _ := a.search(context.Background(), "Details", 0, 10)
	if len(ns) != 1 {
		t.Fatal(ns)
	}
	archiveOnly = true
	os.WriteFile(logFile, nil, 0600)
	if err := poll(u, "token", "room", u+"/slack"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(logFile)
	if *calls != 0 || len(after) != 0 || len(before) == 0 {
		t.Fatal("archive-only touched delivery")
	}
}
func TestPollArchiveFailureDoesNotSuppressDelivery(t *testing.T) {
	a := newTestArchive(t)
	a.db.Close()
	u, calls := setupArchivePoll(t, a, testFeed(), 200)
	if err := poll(u, "token", "room", u+"/slack"); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("deliveries %d", *calls)
	}
	if !loadSeen()["Thu, 24 Sep 2026 12:00:00 GMT"] {
		t.Fatal("delivery state missing")
	}
}
func TestPollDeliveryFailureDoesNotSuppressRemainingArchiveItems(t *testing.T) {
	a := newTestArchive(t)
	feed := strings.Replace(testFeed(), "</channel>", `<item><title>Second</title><guid>second</guid><author>[English]</author><description>Full To view this message retained</description></item></channel>`, 1)
	u, _ := setupArchivePoll(t, a, feed, 502)
	if err := poll(u, "token", "room", u+"/slack"); err == nil {
		t.Fatal("expected Slack failure")
	}
	ns, _ := a.search(context.Background(), "", 0, 10)
	if len(ns) != 2 {
		t.Fatalf("missing later item: %+v", ns)
	}
	if len(loadSeen()) != 0 {
		t.Fatal("failed delivery marked sent")
	}
}
func TestAPIReadOnlyLimitsPaginationAndNoLeak(t *testing.T) {
	a := newTestArchive(t)
	archiveItems(t, a, Item{GUID: "one", Title: "flood", Description: "Full message", Author: "[English]"}, Item{GUID: "two", Title: "flood", Author: "[English]"})
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/v1/notices?limit=1", 200}, {"GET", "/v1/notices?after=-1", 400}, {"GET", "/v1/notices?limit=101", 400}, {"GET", "/v1/notices?limit=0", 400}, {"GET", "/v1/notices?q=" + strings.Repeat("a", 257), 400}, {"GET", "/v1/notices?q=%zz", 400}, {"GET", "/v1/notices?url=https://example.org", 400}, {"GET", "/v1/notices?limit=1&limit=2", 400}, {"POST", "/v1/notices", 405}, {"GET", "/v1/notices/999", 404}, {"GET", "/v1/notices/1", 200}, {"GET", "/v1/status", 200}, {"GET", "/v1/status?x=1", 400}} {
		w := httptest.NewRecorder()
		a.handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/notices?limit=1", nil))
	var first struct {
		Next    int64    `json:"next_after"`
		More    bool     `json:"has_more"`
		Notices []Notice `json:"notices"`
	}
	json.Unmarshal(w.Body.Bytes(), &first)
	if !first.More || first.Next != 1 || len(first.Notices) != 1 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/v1/notices?after=%d&limit=1", first.Next), nil))
	if !strings.Contains(w.Body.String(), `"has_more":false`) {
		t.Fatal(w.Body.String())
	}
	a.db.Close()
	w = httptest.NewRecorder()
	a.handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/status", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "sqlite") {
		t.Fatal(w.Body.String())
	}
}
func TestSocketRequiresPrivateDirectoryAndDoesNotReplaceFiles(t *testing.T) {
	a := newTestArchive(t)
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	path := filepath.Join(dir, "api.sock")
	if _, _, err := a.serveSocket(path); err == nil {
		t.Fatal("accepted public directory")
	}
	os.Chmod(dir, 0700)
	os.WriteFile(path, []byte("keep"), 0600)
	if _, _, err := a.serveSocket(path); err == nil {
		t.Fatal("replaced existing file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep" {
		t.Fatal("file changed")
	}
}

func TestEmptyFeedSuccessAndBadFeedFailureVisible(t *testing.T) {
	a := newTestArchive(t)
	u, _ := setupArchivePoll(t, a, "<rss><channel></channel></rss>", 200)
	archiveOnly = true
	if err := poll(u, "", "", ""); err != nil {
		t.Fatal(err)
	}
	st, _ := a.status(context.Background())
	last := st["last_successful_poll"]
	if last == "" || st["revision_count"] != int64(0) {
		t.Fatal(st)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>not RSS</html>") }))
	defer bad.Close()
	feedURL = bad.URL
	if err := poll(u, "", "", ""); err == nil {
		t.Fatal("accepted non-RSS response")
	}
	st, _ = a.status(context.Background())
	if st["last_failure"] != "feed_fetch_failed" || st["last_successful_poll"] != last {
		t.Fatal(st)
	}
}
func TestListProjectionAndLiteralSearch(t *testing.T) {
	a := newTestArchive(t)
	body := strings.Repeat("x", 4000) + " 100% ' OR 1=1 --"
	archiveItems(t, a, Item{GUID: "one", Title: "Test", Description: body, RawXML: body, Author: "[English]"})
	ns, err := a.searchSummaries(context.Background(), "100% ' OR 1=1 --", 0, 10)
	if err != nil || len(ns) != 1 || len(ns[0].Excerpt) != 2000 {
		t.Fatalf("projection: %v %v", ns, err)
	}
	ns, err = a.searchSummaries(context.Background(), "%missing%", 0, 10)
	if err != nil || len(ns) != 0 {
		t.Fatal("search must be literal")
	}
}
func TestMatrixFailureDoesNotLeakResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); fmt.Fprint(w, "sensitive-response") }))
	defer server.Close()
	err := sendMatrix(server.URL, "test-token", "room", "msg")
	if err == nil || strings.Contains(err.Error(), "sensitive-response") || strings.Contains(err.Error(), server.URL) {
		t.Fatal(err)
	}
}

func TestPrivateSocketRoundTrip(t *testing.T) {
	a := newTestArchive(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "api.sock")
	server, listener, err := a.serveSocket(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go server.Serve(listener)
	info, err := os.Stat(socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	resp, err := client.Get("http://localhost/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
}
func TestConsistentBackupRestores(t *testing.T) {
	a := newTestArchive(t)
	archiveItems(t, a, Item{GUID: "backup", Description: "complete message", Author: "[English]"})
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path := filepath.Join(dir, "backup.sqlite")
	if _, err := a.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	restored, err := openArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	var integrity string
	if err = restored.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity %s %v", integrity, err)
	}
	ns, err := restored.search(context.Background(), "complete message", 0, 10)
	if err != nil || len(ns) != 1 {
		t.Fatalf("backup %v %v", ns, err)
	}
}

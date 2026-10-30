package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (a *Archive) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(code int, msg string) {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			fail(405, "read-only API")
			return
		}
		if len(r.URL.RawQuery) > 2048 {
			fail(400, "query too long")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		var out any
		var err error
		switch {
		case r.URL.Path == "/v1/status":
			if r.URL.RawQuery != "" {
				fail(400, "unexpected query")
				return
			}
			out, err = a.status(ctx)
		case r.URL.Path == "/v1/notices":
			params, e := url.ParseQuery(r.URL.RawQuery)
			if e != nil {
				fail(400, "invalid query")
				return
			}
			for k, v := range params {
				if (k != "q" && k != "after" && k != "limit") || len(v) != 1 {
					fail(400, "invalid parameter")
					return
				}
			}
			q := params.Get("q")
			if len(q) > 256 {
				fail(400, "q exceeds 256 bytes")
				return
			}
			after := int64(0)
			limit := 50
			if s := params.Get("after"); s != "" {
				after, err = strconv.ParseInt(s, 10, 64)
				if err != nil || after < 0 {
					fail(400, "invalid after")
					return
				}
			}
			if s := params.Get("limit"); s != "" {
				limit, err = strconv.Atoi(s)
				if err != nil || limit < 1 || limit > 100 {
					fail(400, "limit must be 1..100")
					return
				}
			}
			var ns []NoticeSummary
			ns, err = a.searchSummaries(ctx, q, after, limit+1)
			if err == nil {
				more := len(ns) > limit
				if more {
					ns = ns[:limit]
				}
				next := int64(0)
				if more {
					next = ns[len(ns)-1].ID
				}
				out = map[string]any{"notices": ns, "next_after": next, "has_more": more, "feed_url": defaultFeedURL}
			}
		case strings.HasPrefix(r.URL.Path, "/v1/notices/"):
			if r.URL.RawQuery != "" {
				fail(400, "unexpected query")
				return
			}
			id, e := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/notices/"), 10, 64)
			if e != nil || id < 1 {
				fail(400, "invalid id")
				return
			}
			out, err = scanNotice(a.db.QueryRowContext(ctx, `SELECT `+noticeColumns+` FROM notices WHERE id=?`, id))
		default:
			fail(404, "not found")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			fail(404, "not found")
			return
		}
		if err != nil {
			fail(503, "archive unavailable")
			return
		}
		json.NewEncoder(w).Encode(out)
	})
}

// No TCP listener or bearer secret: authorization is Unix filesystem permissions.
// Refuse to unlink an existing socket; an operator must verify/remove stale sockets.
func (a *Archive) serveSocket(path string) (*http.Server, net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("socket path must be absolute")
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, nil, errors.New("socket directory must be private (0700)")
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, nil, err
	}
	server := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	return server, listener, nil
}

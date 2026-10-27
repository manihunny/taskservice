package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"taskservice/internal/domain"
)

type ctxKey struct{}

func userID(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKey{}).(int64)
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (h *Handler) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				h.log.ErrorContext(r.Context(), "panic", "panic", p, "path", r.URL.Path)
				writeError(w, nil)
				sw.status = http.StatusInternalServerError
			}
			h.log.InfoContext(r.Context(), "http request",
				"method", r.Method, "path", r.URL.Path, "status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(), "remote", r.RemoteAddr)
		}()
		next.ServeHTTP(sw, r)
	})
}

func (h *Handler) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, domain.ErrUnauthorized)
			return
		}
		id, err := h.svc.Authenticate(token)
		if err != nil {
			writeError(w, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// ipLimiter - token bucket на IP. Хранится в памяти процесса: для нескольких реплик нужен Redis.
type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	rps      rate.Limit
	burst    int
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

func newIPLimiter(ctx context.Context, rps float64, burst int) *ipLimiter {
	l := &ipLimiter{limiters: map[string]*limiterEntry{}, rps: rate.Limit(rps), burst: burst}
	go l.cleanup(ctx)
	return l
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.limiters[ip]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(l.rps, l.burst)}
		l.limiters[ip] = e
	}
	e.seen = time.Now()
	return e.lim.Allow()
}

func (l *ipLimiter) cleanup(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.mu.Lock()
			for ip, e := range l.limiters {
				if time.Since(e.seen) > 3*time.Minute {
					delete(l.limiters, ip)
				}
			}
			l.mu.Unlock()
		}
	}
}

func (l *ipLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		if !l.allow(ip) {
			w.Header().Set("Retry-After", "1")
			writeError(w, domain.ErrTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

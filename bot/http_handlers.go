package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "time"

    "github.com/prometheus/client_golang/prometheus/promhttp"
    zlog "github.com/rs/zerolog/log"
    "github.com/slack-go/slack"
)

// HTTP handlers and middleware extracted from main.go for clarity.

func newGivenHandler(store Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user := r.URL.Query().Get("user")
        if user == "" {
            http.Error(w, "user required", http.StatusBadRequest)
            return
        }
        start, end, err := parseDateRangeFromParams(r)
        if err != nil {
            http.Error(w, "invalid or missing date range: "+err.Error(), http.StatusBadRequest)
            return
        }
        c, err := store.CountGivenInDateRange(user, start, end)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(fmt.Sprintf(`{"user":"%s","start":"%s","end":"%s","given":%d}`,
            user, start.Format("2006-01-02"), end.Format("2006-01-02"), c)))
    }
}

func newReceivedHandler(store Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        user := r.URL.Query().Get("user")
        if user == "" {
            http.Error(w, "user required", http.StatusBadRequest)
            return
        }
        start, end, err := parseDateRangeFromParams(r)
        if err != nil {
            http.Error(w, "invalid or missing date range: "+err.Error(), http.StatusBadRequest)
            return
        }
        c, err := store.CountReceivedInDateRange(user, start, end)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(fmt.Sprintf(`{"user":"%s","start":"%s","end":"%s","received":%d}`,
            user, start.Format("2006-01-02"), end.Format("2006-01-02"), c)))
    }
}

func newUserHandler(client *slack.Client) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        userID := r.URL.Query().Get("user")
        if userID == "" {
            http.Error(w, "user required", http.StatusBadRequest)
            return
        }
        user, err := client.GetUserInfo(userID)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        response := map[string]string{
            "real_name":     user.RealName,
            "profile_image": user.Profile.Image192,
        }
        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(response); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
        }
    }
}

func newGiversHandler(store Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        list, err := store.GetAllGivers()
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(list); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
        }
    }
}

func newRecipientsHandler(store Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        list, err := store.GetAllRecipients()
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        if err := json.NewEncoder(w).Encode(list); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
        }
    }
}

func newHealthHandler(slackManager *SlackConnectionManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")

        health := map[string]interface{}{
            "status":          "healthy",
            "service":         "beerbot-backend",
            "slack_connected": slackManager.IsConnected(),
            "timestamp":       time.Now().UTC().Format(time.RFC3339),
        }

        if r.URL.Query().Get("check_slack") == "true" {
            if err := slackManager.TestConnection(r.Context()); err != nil {
                health["slack_connection_error"] = err.Error()
                health["status"] = "degraded"
            }
        }

        statusCode := http.StatusOK
        if health["status"] == "degraded" {
            statusCode = http.StatusServiceUnavailable
        }

        w.WriteHeader(statusCode)
        if err := json.NewEncoder(w).Encode(health); err != nil {
            zlog.Error().Err(err).Msg("health check write error")
        }
    }
}

func newMux(apiToken string, client *slack.Client, store Store, slackManager *SlackConnectionManager) *http.ServeMux {
    mux := http.NewServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
        ObserveHTTPRequest("/healthz", r.Method, http.StatusOK, start)
    })
    mux.Handle("/metrics", promhttp.Handler())
    mux.Handle("/api/given", wrapWithMetrics("/api/given", authMiddleware(apiToken, newGivenHandler(store))))
    mux.Handle("/api/received", wrapWithMetrics("/api/received", authMiddleware(apiToken, newReceivedHandler(store))))
    mux.Handle("/api/user", wrapWithMetrics("/api/user", authMiddleware(apiToken, newUserHandler(client))))
    mux.Handle("/api/givers", wrapWithMetrics("/api/givers", newGiversHandler(store)))
    mux.Handle("/api/recipients", wrapWithMetrics("/api/recipients", newRecipientsHandler(store)))
    mux.Handle("/api/health", wrapWithMetrics("/api/health", newHealthHandler(slackManager)))
    return mux
}

// wrapWithMetrics records request count and duration for a handler.
func wrapWithMetrics(path string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
        next.ServeHTTP(sr, r)
        ObserveHTTPRequest(path, r.Method, sr.status, start)
    })
}

func authMiddleware(apiToken string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        authHeader := r.Header.Get("Authorization")
        if authHeader == "" {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }

        bearerToken := strings.Split(authHeader, " ")
        if len(bearerToken) != 2 || bearerToken[0] != "Bearer" {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }

        if bearerToken[1] != apiToken {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }

        next.ServeHTTP(w, r)
    })
}

func parseDateRangeFromParams(r *http.Request) (time.Time, time.Time, error) {
    day := r.URL.Query().Get("day")
    startStr := r.URL.Query().Get("start")
    endStr := r.URL.Query().Get("end")

    layout := "2006-01-02"
    if day != "" {
        t, err := time.Parse(layout, day)
        if err != nil {
            return time.Time{}, time.Time{}, err
        }
        return t, t, nil
    }
    if startStr != "" && endStr != "" {
        start, err1 := time.Parse(layout, startStr)
        end, err2 := time.Parse(layout, endStr)
        if err1 != nil || err2 != nil {
            return time.Time{}, time.Time{}, fmt.Errorf("invalid start or end date")
        }
        return start, end, nil
    }
    return time.Time{}, time.Time{}, fmt.Errorf("must provide either day=YYYY-MM-DD or start=YYYY-MM-DD&end=YYYY-MM-DD")
}

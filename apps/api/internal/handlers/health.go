package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthHandler struct {
	DB *pgxpool.Pool
}

type healthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{DB: pool}
}

func (h *HealthHandler) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{
			Status:    "db_unavailable",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		})
		return
	}

	if err := h.DB.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{
			Status:    "db_not_ready",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		})
		return
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:    "ready",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

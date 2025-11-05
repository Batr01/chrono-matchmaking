package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"chrono-matchmaking/models"
	"chrono-matchmaking/service"
	"go.uber.org/zap"
)

// QueueHandler обрабатывает HTTP запросы для матчмейкинга
type QueueHandler struct {
	matcher *service.MatcherService
	logger  *zap.Logger
}

// NewQueueHandler создает новый обработчик очереди
func NewQueueHandler(matcher *service.MatcherService, logger *zap.Logger) *QueueHandler {
	return &QueueHandler{
		matcher: matcher,
		logger:  logger,
	}
}

// JoinQueue обрабатывает запрос на вход в очередь
func (h *QueueHandler) JoinQueue(w http.ResponseWriter, r *http.Request) {
	var req models.MatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Создаем игрока
	player := models.NewPlayer(req.Rating, req.Region, req.GameMode, req.PlayerLevel)

	// Добавляем игрока в очередь
	if err := h.matcher.AddPlayerToQueue(r.Context(), player); err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to add player to queue", err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"player_id": player.ID,
		"status":    "queued",
		"message":   "Player added to queue",
	})

	h.logger.Info("Player joined queue",
		zap.String("player_id", player.ID),
		zap.String("region", player.Region),
		zap.String("game_mode", player.GameMode),
		zap.Int("rating", player.Rating),
	)
}

// LeaveQueue обрабатывает запрос на выход из очереди
func (h *QueueHandler) LeaveQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	playerID := vars["player_id"]

	if playerID == "" {
		h.respondError(w, http.StatusBadRequest, "Player ID is required", nil)
		return
	}

	// Удаляем игрока из очереди
	if err := h.matcher.RemovePlayerFromQueue(r.Context(), playerID); err != nil {
		h.respondError(w, http.StatusNotFound, "Failed to remove player from queue", err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"player_id": playerID,
		"status":    "removed",
		"message":   "Player removed from queue",
	})

	h.logger.Info("Player left queue",
		zap.String("player_id", playerID),
	)
}

// FindMatch обрабатывает запрос на поиск матча
func (h *QueueHandler) FindMatch(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	playerID := vars["player_id"]

	if playerID == "" {
		h.respondError(w, http.StatusBadRequest, "Player ID is required", nil)
		return
	}

	// Ищем матч
	match, err := h.matcher.FindMatch(r.Context(), playerID)
	if err != nil {
		h.respondError(w, http.StatusNotFound, "Match not found", err)
		return
	}

	h.respondJSON(w, http.StatusOK, match)

	h.logger.Info("Match found",
		zap.String("match_id", match.MatchID),
		zap.String("player_id", playerID),
	)
}

// GetQueueStatus возвращает статус очереди
func (h *QueueHandler) GetQueueStatus(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	gameMode := r.URL.Query().Get("game_mode")

	if region == "" || gameMode == "" {
		h.respondError(w, http.StatusBadRequest, "Region and game_mode are required", nil)
		return
	}

	// Получаем размер очереди
	queueSize, err := h.matcher.GetQueueSize(r.Context(), region, gameMode)
	if err != nil {
		h.respondError(w, http.StatusInternalServerError, "Failed to get queue size", err)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"region":     region,
		"game_mode":  gameMode,
		"queue_size": queueSize,
		"timestamp":  time.Now().Unix(),
	})
}

// respondJSON отправляет JSON ответ
func (h *QueueHandler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("Failed to encode JSON response", zap.Error(err))
	}
}

// respondError отправляет ошибку в формате JSON
func (h *QueueHandler) respondError(w http.ResponseWriter, status int, message string, err error) {
	h.logger.Warn("Request error",
		zap.Int("status", status),
		zap.String("message", message),
		zap.Error(err),
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	
	errorResp := map[string]interface{}{
		"error": message,
	}
	if err != nil {
		errorResp["details"] = err.Error()
	}
	json.NewEncoder(w).Encode(errorResp)
}


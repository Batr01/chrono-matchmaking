package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"chrono-matchmaking/models"
	"go.uber.org/zap"
)

// RedisStorage управляет очередью игроков в Redis
type RedisStorage struct {
	client *redis.Client
	logger *zap.Logger
}

// NewRedisStorage создает новое хранилище Redis
func NewRedisStorage(addr string, password string, db int, logger *zap.Logger) (*RedisStorage, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisStorage{
		client: client,
		logger: logger,
	}, nil
}

// Close закрывает соединение с Redis
func (s *RedisStorage) Close() error {
	return s.client.Close()
}

// AddPlayerToQueue добавляет игрока в очередь
func (s *RedisStorage) AddPlayerToQueue(ctx context.Context, player *models.Player) error {
	key := s.queueKey(player.Region, player.GameMode)
	
	playerJSON, err := json.Marshal(player)
	if err != nil {
		return fmt.Errorf("failed to marshal player: %w", err)
	}

	// Добавляем игрока в отсортированный набор (sorted set) по рейтингу
	score := float64(player.Rating)
	err = s.client.ZAdd(ctx, key, &redis.Z{
		Score:  score,
		Member: playerJSON,
	}).Err()
	
	if err != nil {
		return fmt.Errorf("failed to add player to queue: %w", err)
	}

	// Устанавливаем TTL для игрока (30 минут)
	playerKey := s.playerKey(player.ID)
	err = s.client.Set(ctx, playerKey, playerJSON, 30*time.Minute).Err()
	if err != nil {
		return fmt.Errorf("failed to set player TTL: %w", err)
	}

	s.logger.Info("Player added to queue",
		zap.String("player_id", player.ID),
		zap.String("region", player.Region),
		zap.String("game_mode", player.GameMode),
		zap.Int("rating", player.Rating),
	)

	return nil
}

// RemovePlayerFromQueue удаляет игрока из очереди
func (s *RedisStorage) RemovePlayerFromQueue(ctx context.Context, playerID string) error {
	playerKey := s.playerKey(playerID)
	
	// Получаем данные игрока
	playerJSON, err := s.client.Get(ctx, playerKey).Result()
	if err == redis.Nil {
		return fmt.Errorf("player not found")
	}
	if err != nil {
		return fmt.Errorf("failed to get player: %w", err)
	}

	var player models.Player
	if err := json.Unmarshal([]byte(playerJSON), &player); err != nil {
		return fmt.Errorf("failed to unmarshal player: %w", err)
	}

	// Удаляем из очереди
	key := s.queueKey(player.Region, player.GameMode)
	err = s.client.ZRem(ctx, key, playerJSON).Err()
	if err != nil {
		return fmt.Errorf("failed to remove player from queue: %w", err)
	}

	// Удаляем ключ игрока
	err = s.client.Del(ctx, playerKey).Err()
	if err != nil {
		return fmt.Errorf("failed to delete player key: %w", err)
	}

	s.logger.Info("Player removed from queue",
		zap.String("player_id", playerID),
	)

	return nil
}

// GetPlayersInRange возвращает игроков в диапазоне рейтинга
func (s *RedisStorage) GetPlayersInRange(ctx context.Context, region, gameMode string, minRating, maxRating int, limit int64) ([]*models.Player, error) {
	key := s.queueKey(region, gameMode)
	
	minScore := fmt.Sprintf("%d", minRating)
	maxScore := fmt.Sprintf("%d", maxRating)

	// Получаем игроков в диапазоне рейтинга
	results, err := s.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min:   minScore,
		Max:   maxScore,
		Count: limit,
	}).Result()
	
	if err != nil {
		return nil, fmt.Errorf("failed to get players in range: %w", err)
	}

	players := make([]*models.Player, 0, len(results))
	for _, result := range results {
		var player models.Player
		if err := json.Unmarshal([]byte(result), &player); err != nil {
			s.logger.Warn("Failed to unmarshal player",
				zap.Error(err),
				zap.String("data", result),
			)
			continue
		}
		players = append(players, &player)
	}

	return players, nil
}

// GetPlayerByID возвращает игрока по ID
func (s *RedisStorage) GetPlayerByID(ctx context.Context, playerID string) (*models.Player, error) {
	playerKey := s.playerKey(playerID)
	
	playerJSON, err := s.client.Get(ctx, playerKey).Result()
	if err == redis.Nil {
		return nil, fmt.Errorf("player not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get player: %w", err)
	}

	var player models.Player
	if err := json.Unmarshal([]byte(playerJSON), &player); err != nil {
		return nil, fmt.Errorf("failed to unmarshal player: %w", err)
	}

	return &player, nil
}

// GetQueueSize возвращает размер очереди
func (s *RedisStorage) GetQueueSize(ctx context.Context, region, gameMode string) (int64, error) {
	key := s.queueKey(region, gameMode)
	return s.client.ZCard(ctx, key).Result()
}

// queueKey возвращает ключ для очереди
func (s *RedisStorage) queueKey(region, gameMode string) string {
	return fmt.Sprintf("queue:%s:%s", region, gameMode)
}

// playerKey возвращает ключ для игрока
func (s *RedisStorage) playerKey(playerID string) string {
	return fmt.Sprintf("player:%s", playerID)
}


package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"chrono-matchmaking/models"
	"chrono-matchmaking/storage"
	"go.uber.org/zap"
)

// MatcherService управляет логикой поиска матчей
type MatcherService struct {
	storage *storage.RedisStorage
	logger  *zap.Logger
	config  *MatcherConfig
}

// MatcherConfig конфигурация матчмейкера
type MatcherConfig struct {
	MaxRatingDiff      int           // Максимальная разница рейтинга
	MaxSearchTime      time.Duration // Максимальное время поиска матча
	RatingExpansionRate int          // Скорость расширения диапазона рейтинга (в секундах)
	PlayersPerMatch    int           // Количество игроков в матче (6 для 3x3)
}

// DefaultMatcherConfig возвращает конфигурацию по умолчанию
func DefaultMatcherConfig() *MatcherConfig {
	return &MatcherConfig{
		MaxRatingDiff:      200,           // Начальная разница рейтинга
		MaxSearchTime:      5 * time.Minute, // Максимальное время поиска
		RatingExpansionRate: 50,           // +50 рейтинга каждые 30 секунд
		PlayersPerMatch:    6,             // 3x3 матч (6 игроков) - используется как значение по умолчанию
	}
}

// GetPlayersPerMatch возвращает количество игроков для режима игры
func GetPlayersPerMatch(gameMode string) int {
	switch gameMode {
	case "1v1":
		return 2 // 1 игрок против 1 игрока
	case "3v3":
		return 6 // 3 игрока против 3 игроков
	default:
		return 6 // По умолчанию 3v3
	}
}

// NewMatcherService создает новый сервис матчмейкинга
func NewMatcherService(storage *storage.RedisStorage, logger *zap.Logger, config *MatcherConfig) *MatcherService {
	if config == nil {
		config = DefaultMatcherConfig()
	}
	return &MatcherService{
		storage: storage,
		logger:  logger,
		config:  config,
	}
}

// FindMatch пытается найти матч для игрока
func (s *MatcherService) FindMatch(ctx context.Context, playerID string) (*models.Match, error) {
	// Сначала проверяем, есть ли уже сохраненный матч для этого игрока
	savedMatch, err := s.storage.GetMatchByPlayerID(ctx, playerID)
	if err == nil && savedMatch != nil {
		// Матч уже найден и сохранен
		s.logger.Info("Returning saved match",
			zap.String("match_id", savedMatch.MatchID),
			zap.String("player_id", playerID),
		)
		return savedMatch, nil
	}

	// Получаем игрока по ID
	currentPlayer, err := s.storage.GetPlayerByID(ctx, playerID)
	if err != nil {
		return nil, fmt.Errorf("player not found in queue: %w", err)
	}

	// Определяем количество игроков для данного режима
	playersPerMatch := GetPlayersPerMatch(currentPlayer.GameMode)

	// Вычисляем динамический диапазон рейтинга на основе времени ожидания
	waitTime := time.Since(currentPlayer.JoinedAt)
	ratingRange := s.calculateRatingRange(waitTime)

	// Ищем подходящих игроков (нужно больше кандидатов, так как будем фильтровать)
	candidates, err := s.storage.GetPlayersInRange(
		ctx,
		currentPlayer.Region,
		currentPlayer.GameMode,
		currentPlayer.Rating-ratingRange,
		currentPlayer.Rating+ratingRange,
		int64(playersPerMatch*2), // Берем больше кандидатов для фильтрации
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get candidates: %w", err)
	}

	// Фильтруем кандидатов (исключаем самого игрока и проверяем совместимость)
	matchPlayers := make([]models.Player, 0, playersPerMatch)
	matchPlayers = append(matchPlayers, *currentPlayer)

	for _, candidate := range candidates {
		if candidate.ID == playerID {
			continue // Пропускаем самого игрока
		}

		if s.isCompatible(currentPlayer, candidate) {
			matchPlayers = append(matchPlayers, *candidate)
			if len(matchPlayers) >= playersPerMatch {
				break
			}
		}
	}

	// Если нашли достаточно игроков, создаем матч
	if len(matchPlayers) >= playersPerMatch {
		match := &models.Match{
			MatchID:   fmt.Sprintf("match_%d", time.Now().UnixNano()),
			Players:   matchPlayers,
			CreatedAt: time.Now(),
		}

		// Сохраняем матч для всех игроков ПЕРЕД удалением из очереди
		if err := s.storage.SaveMatch(ctx, match); err != nil {
			s.logger.Warn("Failed to save match",
				zap.String("match_id", match.MatchID),
				zap.Error(err),
			)
		}

		// Удаляем игроков из очереди
		for _, p := range matchPlayers {
			if err := s.storage.RemovePlayerFromQueue(ctx, p.ID); err != nil {
				s.logger.Warn("Failed to remove player from queue",
					zap.String("player_id", p.ID),
					zap.Error(err),
				)
			}
		}

		s.logger.Info("Match found",
			zap.String("match_id", match.MatchID),
			zap.Int("players_count", len(matchPlayers)),
		)

		return match, nil
	}

	return nil, fmt.Errorf("no suitable match found")
}

// calculateRatingRange вычисляет динамический диапазон рейтинга на основе времени ожидания
func (s *MatcherService) calculateRatingRange(waitTime time.Duration) int {
	if waitTime > s.config.MaxSearchTime {
		return 1000 // Максимальный диапазон после максимального времени ожидания
	}

	// Расширяем диапазон каждые 30 секунд
	expansionCount := int(waitTime.Seconds()) / 30
	return s.config.MaxRatingDiff + (expansionCount * s.config.RatingExpansionRate)
}

// isCompatible проверяет совместимость двух игроков
func (s *MatcherService) isCompatible(p1, p2 *models.Player) bool {
	// Проверяем регион
	if p1.Region != p2.Region {
		return false
	}

	// Проверяем режим игры
	if p1.GameMode != p2.GameMode {
		return false
	}

	// Проверяем разницу рейтинга
	ratingDiff := int(math.Abs(float64(p1.Rating - p2.Rating)))
	return ratingDiff <= s.config.MaxRatingDiff
}

// AddPlayerToQueue добавляет игрока в очередь
func (s *MatcherService) AddPlayerToQueue(ctx context.Context, player *models.Player) error {
	return s.storage.AddPlayerToQueue(ctx, player)
}

// RemovePlayerFromQueue удаляет игрока из очереди
func (s *MatcherService) RemovePlayerFromQueue(ctx context.Context, playerID string) error {
	return s.storage.RemovePlayerFromQueue(ctx, playerID)
}

// GetQueueSize возвращает размер очереди
func (s *MatcherService) GetQueueSize(ctx context.Context, region, gameMode string) (int64, error) {
	return s.storage.GetQueueSize(ctx, region, gameMode)
}

// ProcessQueue обрабатывает очередь и пытается найти матчи
func (s *MatcherService) ProcessQueue(ctx context.Context, region, gameMode string) error {
	// Определяем количество игроков для данного режима
	playersPerMatch := GetPlayersPerMatch(gameMode)

	// Получаем всех игроков в очереди для данного региона и режима
	players, err := s.storage.GetPlayersInRange(ctx, region, gameMode, 0, math.MaxInt, 100)
	if err != nil {
		return fmt.Errorf("failed to get players: %w", err)
	}

	if len(players) < playersPerMatch {
		return nil // Недостаточно игроков для создания матча
	}

	// Используем алгоритм жадного поиска для формирования групп
	used := make(map[string]bool) // Отслеживаем использованных игроков

	for i := 0; i < len(players); i++ {
		if used[players[i].ID] {
			continue
		}

		// Начинаем формировать группу с текущего игрока
		group := []*models.Player{players[i]}
		used[players[i].ID] = true

		// Ищем совместимых игроков для группы
		for j := 0; j < len(players) && len(group) < playersPerMatch; j++ {
			if used[players[j].ID] {
				continue
			}

			// Проверяем совместимость с первым игроком группы
			if s.isCompatible(group[0], players[j]) {
				group = append(group, players[j])
				used[players[j].ID] = true
			}
		}

		// Если собрали группу из нужного количества игроков, создаем матч
		if len(group) >= playersPerMatch {
			matchPlayers := make([]models.Player, 0, len(group))
			for _, p := range group {
				matchPlayers = append(matchPlayers, *p)
			}

			match := &models.Match{
				MatchID:   fmt.Sprintf("match_%d", time.Now().UnixNano()),
				Players:   matchPlayers,
				CreatedAt: time.Now(),
			}

			// Сохраняем матч для всех игроков ПЕРЕД удалением из очереди
			if err := s.storage.SaveMatch(ctx, match); err != nil {
				s.logger.Warn("Failed to save match",
					zap.String("match_id", match.MatchID),
					zap.Error(err),
				)
			}

			// Удаляем игроков из очереди
			for _, p := range group {
				if err := s.storage.RemovePlayerFromQueue(ctx, p.ID); err != nil {
					s.logger.Warn("Failed to remove player from queue",
						zap.String("player_id", p.ID),
						zap.Error(err),
					)
				}
			}

			s.logger.Info("Match created from queue processing",
				zap.String("match_id", match.MatchID),
				zap.Int("players_count", len(matchPlayers)),
				zap.String("region", region),
				zap.String("game_mode", gameMode),
			)

			// Продолжаем поиск для остальных игроков
			continue
		}

		// Если не собрали группу, освобождаем игроков (кроме первого)
		for k := 1; k < len(group); k++ {
			delete(used, group[k].ID)
		}
		delete(used, group[0].ID) // Освобождаем и первого, чтобы попробовать другие комбинации
	}

	return nil
}


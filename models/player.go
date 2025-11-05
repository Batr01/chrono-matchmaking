package models

import (
	"time"

	"github.com/google/uuid"
)

// Player представляет игрока в системе матчмейкинга
type Player struct {
	ID         string    `json:"id"`           // Уникальный идентификатор игрока
	Rating     int       `json:"rating"`       // Рейтинг игрока (MMR)
	Region     string    `json:"region"`       // Регион игрока (например, "EU", "US", "ASIA")
	GameMode   string    `json:"game_mode"`    // Режим игры (например, "ranked", "casual")
	JoinedAt   time.Time `json:"joined_at"`   // Время входа в очередь
	PlayerLevel int      `json:"player_level"` // Уровень игрока
}

// NewPlayer создает нового игрока
func NewPlayer(rating int, region, gameMode string, playerLevel int) *Player {
	return &Player{
		ID:          uuid.New().String(),
		Rating:      rating,
		Region:      region,
		GameMode:    gameMode,
		JoinedAt:    time.Now(),
		PlayerLevel: playerLevel,
	}
}

// MatchRequest представляет запрос на поиск матча
type MatchRequest struct {
	PlayerID   string `json:"player_id"`
	Rating     int    `json:"rating"`
	Region     string `json:"region"`
	GameMode   string `json:"game_mode"`
	PlayerLevel int   `json:"player_level"`
}

// Match представляет найденный матч
type Match struct {
	MatchID   string   `json:"match_id"`
	Players   []Player `json:"players"`
	CreatedAt time.Time `json:"created_at"`
}


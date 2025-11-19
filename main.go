package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"chrono-matchmaking/handler"
	"chrono-matchmaking/service"
	"chrono-matchmaking/storage"
	"go.uber.org/zap"
)

const (
	defaultRedisAddr     = "localhost:6379"
	defaultRedisPassword = "0000" // Пароль из docker-compose.yml
	defaultRedisDB       = 0
	serverPort           = ":8080"
)

// getEnv получает значение переменной окружения или возвращает значение по умолчанию
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	// Инициализация логгера
	logger, err := zap.NewProduction()
	if err != nil {
		panic(fmt.Sprintf("Failed to initialize logger: %v", err))
	}
	defer logger.Sync()

	logger.Info("Starting Chrono Matchmaking Service")

	// Получаем настройки Redis из переменных окружения или используем значения по умолчанию
	redisAddr := getEnv("REDIS_ADDR", defaultRedisAddr)
	redisPassword := getEnv("REDIS_PASSWORD", defaultRedisPassword)
	redisDB := defaultRedisDB
	if db := os.Getenv("REDIS_DB"); db != "" {
		fmt.Sscanf(db, "%d", &redisDB)
	}

	// Инициализация Redis хранилища
	redisStorage, err := storage.NewRedisStorage(redisAddr, redisPassword, redisDB, logger)
	if err != nil {
		logger.Fatal("Failed to initialize Redis storage", zap.Error(err))
	}
	defer redisStorage.Close()

	logger.Info("Connected to Redis", zap.String("addr", redisAddr))

	// Инициализация сервиса матчмейкинга
	matcherConfig := service.DefaultMatcherConfig()
	matcherService := service.NewMatcherService(redisStorage, logger, matcherConfig)

	// Инициализация HTTP handlers
	queueHandler := handler.NewQueueHandler(matcherService, logger)

	// Настройка маршрутов
	router := mux.NewRouter()
	api := router.PathPrefix("/api/v1").Subrouter()

	// Эндпоинты матчмейкинга
	api.HandleFunc("/queue/join", queueHandler.JoinQueue).Methods("POST")
	api.HandleFunc("/queue/leave/{player_id}", queueHandler.LeaveQueue).Methods("DELETE")
	api.HandleFunc("/queue/match/{player_id}", queueHandler.FindMatch).Methods("GET")
	api.HandleFunc("/queue/status", queueHandler.GetQueueStatus).Methods("GET")

	// Health check
	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}).Methods("GET")

	// Настройка HTTP сервера
	srv := &http.Server{
		Addr:         serverPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Запуск сервера в горутине
	go func() {
		logger.Info("Starting HTTP server", zap.String("port", serverPort))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Failed to start server", zap.Error(err))
		}
	}()

	// Запуск обработчика очереди в фоне
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(10 * time.Second) // Проверяем очередь каждые 10 секунд
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Обрабатываем очереди для разных регионов и режимов
				regions := []string{"EU", "US", "ASIA"}
				gameModes := []string{"1v1", "3v3"}

				for _, region := range regions {
					for _, gameMode := range gameModes {
						if err := matcherService.ProcessQueue(ctx, region, gameMode); err != nil {
							logger.Warn("Failed to process queue",
								zap.String("region", region),
								zap.String("game_mode", gameMode),
								zap.Error(err),
							)
						}
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Ожидание сигнала для graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	cancel() // Останавливаем обработчик очереди

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
	}

	logger.Info("Server exited")
}


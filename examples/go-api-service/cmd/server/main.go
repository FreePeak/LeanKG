package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-api-service/internal/api"
	"go-api-service/internal/middleware"
	"go-api-service/internal/repository"
	"go-api-service/internal/services"
	"go-api-service/pkg/logger"
)

func main() {
	cfg := loadConfig()
	
	appLogger := logger.New(cfg.LogLevel)
	appLogger.Info("Starting Go API Service")
	
	userRepo := repository.NewUserRepository(cfg.DatabaseURL)
	orderRepo := repository.NewOrderRepository(cfg.DatabaseURL)
	
	userSvc := services.NewUserService(userRepo, appLogger)
	orderSvc := services.NewOrderService(orderRepo, userSvc, appLogger)
	
	handler := api.NewHandler(userSvc, orderSvc, appLogger)
	
	router := api.SetupRouter(handler, cfg)
	
	server := &http.Server{
		Addr:         cfg.ServerAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	
	go func() {
		appLogger.Info("Server listening on " + cfg.ServerAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appLogger.Fatal("Server failed: " + err.Error())
		}
	}()
	
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	
	appLogger.Info("Shutting down server...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	if err := server.Shutdown(ctx); err != nil {
		appLogger.Fatal("Server forced shutdown: " + err.Error())
	}
	
	appLogger.Info("Server exited gracefully")
}

func loadConfig() *Config {
	return &Config{
		ServerAddr:  getEnv("SERVER_ADDR", ":8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://localhost:5432/mydb"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

type Config struct {
	ServerAddr  string
	DatabaseURL string
	LogLevel    string
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

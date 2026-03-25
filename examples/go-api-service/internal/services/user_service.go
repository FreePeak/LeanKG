package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go-api-service/internal/models"
	"go-api-service/internal/repository"
	"go-api-service/pkg/logger"
)

var (
	ErrUserNotFound    = errors.New("user not found")
	ErrUserExists      = errors.New("user already exists")
	ErrInvalidInput    = errors.New("invalid input")
	ErrUnauthorized   = errors.New("unauthorized")
)

type UserService struct {
	repo   *repository.UserRepository
	logger logger.Logger
}

func NewUserService(repo *repository.UserRepository, log logger.Logger) *UserService {
	return &UserService{
		repo:   repo,
		logger: log,
	}
}

func (s *UserService) ListUsers(ctx context.Context, limit, offset int) ([]*models.User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	
	users, err := s.repo.List(ctx, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list users: " + err.Error())
		return nil, err
	}
	
	s.logger.Debug("Listed users", "count", len(users))
	return users, nil
}

func (s *UserService) GetUser(ctx context.Context, id string) (*models.User, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		s.logger.Warn("User not found: " + id)
		return nil, ErrUserNotFound
	}
	
	return user, nil
}

func (s *UserService) CreateUser(ctx context.Context, name, email, password string) (*models.User, error) {
	if name == "" || email == "" || password == "" {
		return nil, ErrInvalidInput
	}
	
	existing, err := s.repo.GetByEmail(ctx, email)
	if err == nil && existing != nil {
		return nil, ErrUserExists
	}
	
	hashedPassword := hashPassword(password)
	
	user := &models.User{
		ID:        generateID(),
		Name:      name,
		Email:     email,
		Password:  hashedPassword,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	
	if err := user.Validate(); err != nil {
		return nil, err
	}
	
	if err := s.repo.Create(ctx, user); err != nil {
		s.logger.Error("Failed to create user: " + err.Error())
		return nil, err
	}
	
	s.logger.Info("User created", "id", user.ID, "email", user.Email)
	return user, nil
}

func (s *UserService) UpdateUser(ctx context.Context, id, name, email string) (*models.User, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	
	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	
	if name != "" {
		user.Name = name
	}
	if email != "" {
		user.Email = email
	}
	user.UpdatedAt = time.Now()
	
	if err := user.Validate(); err != nil {
		return nil, err
	}
	
	if err := s.repo.Update(ctx, user); err != nil {
		s.logger.Error("Failed to update user: " + err.Error())
		return nil, err
	}
	
	s.logger.Info("User updated", "id", user.ID)
	return user, nil
}

func (s *UserService) DeleteUser(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidInput
	}
	
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return ErrUserNotFound
	}
	
	if err := s.repo.Delete(ctx, id); err != nil {
		s.logger.Error("Failed to delete user: " + err.Error())
		return err
	}
	
	s.logger.Info("User deleted", "id", id)
	return nil
}

func hashPassword(password string) string {
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

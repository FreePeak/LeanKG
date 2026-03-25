package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-api-service/internal/models"
	"go-api-service/internal/repository"
	"go-api-service/pkg/logger"
)

var (
	ErrOrderNotFound    = errors.New("order not found")
	ErrInvalidOrderData = errors.New("invalid order data")
	ErrUserRequired     = errors.New("user is required for order")
)

type OrderService struct {
	repo      *repository.OrderRepository
	userSvc   *UserService
	logger    logger.Logger
}

func NewOrderService(repo *repository.OrderRepository, userSvc *UserService, log logger.Logger) *OrderService {
	return &OrderService{
		repo:    repo,
		userSvc: userSvc,
		logger:  log,
	}
}

func (s *OrderService) ListOrders(ctx context.Context, userID string, limit, offset int) ([]*models.Order, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	
	orders, err := s.repo.List(ctx, userID, limit, offset)
	if err != nil {
		s.logger.Error("Failed to list orders: " + err.Error())
		return nil, err
	}
	
	s.logger.Debug("Listed orders", "count", len(orders), "user_id", userID)
	return orders, nil
}

func (s *OrderService) GetOrder(ctx context.Context, id string) (*models.Order, error) {
	if id == "" {
		return nil, ErrInvalidInput
	}
	
	order, err := s.repo.GetByID(ctx, id)
	if err != nil {
		s.logger.Warn("Order not found: " + id)
		return nil, ErrOrderNotFound
	}
	
	return order, nil
}

func (s *OrderService) CreateOrder(ctx context.Context, userID, product string, quantity int, totalPrice float64) (*models.Order, error) {
	if userID == "" {
		return nil, ErrUserRequired
	}
	if product == "" {
		return nil, ErrInvalidOrderData
	}
	if quantity <= 0 {
		return nil, ErrInvalidOrderData
	}
	if totalPrice <= 0 {
		return nil, ErrInvalidOrderData
	}
	
	user, err := s.userSvc.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	
	order := &models.Order{
		ID:         generateOrderID(),
		UserID:     user.ID,
		Product:    product,
		Quantity:   quantity,
		TotalPrice: totalPrice,
		Status:     models.OrderStatusPending,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	
	if err := order.Validate(); err != nil {
		return nil, err
	}
	
	if err := s.repo.Create(ctx, order); err != nil {
		s.logger.Error("Failed to create order: " + err.Error())
		return nil, err
	}
	
	s.logger.Info("Order created", "id", order.ID, "user_id", userID, "product", product)
	return order, nil
}

func (s *OrderService) UpdateOrderStatus(ctx context.Context, id, status string) (*models.Order, error) {
	order, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	
	order.Status = status
	order.UpdatedAt = time.Now()
	
	if err := s.repo.Update(ctx, order); err != nil {
		s.logger.Error("Failed to update order status: " + err.Error())
		return nil, err
	}
	
	s.logger.Info("Order status updated", "id", order.ID, "status", status)
	return order, nil
}

func (s *OrderService) CancelOrder(ctx context.Context, id string) error {
	order, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return ErrOrderNotFound
	}
	
	if err := order.Cancel(); err != nil {
		return err
	}
	
	if err := s.repo.Update(ctx, order); err != nil {
		s.logger.Error("Failed to cancel order: " + err.Error())
		return err
	}
	
	s.logger.Info("Order cancelled", "id", order.ID)
	return nil
}

func (s *OrderService) GetUserOrders(ctx context.Context, userID string) ([]*models.Order, error) {
	if userID == "" {
		return nil, ErrInvalidInput
	}
	
	orders, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		s.logger.Error("Failed to get user orders: " + err.Error())
		return nil, err
	}
	
	return orders, nil
}

func generateOrderID() string {
	return fmt.Sprintf("ORD-%d", time.Now().UnixNano())
}

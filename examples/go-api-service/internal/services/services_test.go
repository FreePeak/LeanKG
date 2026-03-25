package services

import (
	"context"
	"testing"
	"time"

	"go-api-service/internal/models"
	"go-api-service/pkg/logger"
)

type mockUserRepo struct {
	users map[string]*models.User
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{users: make(map[string]*models.User)}
}

func (m *mockUserRepo) List(ctx context.Context, limit, offset int) ([]*models.User, error) {
	var result []*models.User
	for _, user := range m.users {
		result = append(result, user)
	}
	return result, nil
}

func (m *mockUserRepo) GetByID(ctx context.Context, id string) (*models.User, error) {
	if user, ok := m.users[id]; ok {
		return user, nil
	}
	return nil, ErrUserNotFound
}

func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	for _, user := range m.users {
		if user.Email == email {
			return user, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *mockUserRepo) Create(ctx context.Context, user *models.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) Update(ctx context.Context, user *models.User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockUserRepo) Delete(ctx context.Context, id string) error {
	delete(m.users, id)
	return nil
}

type mockUserRepoWrapper struct {
	*mockUserRepo
}

func TestCreateUser(t *testing.T) {
	repo := newMockUserRepo()
	log := logger.New("error")
	svc := &UserService{repo: repo, logger: log}
	
	ctx := context.Background()
	
	user, err := svc.CreateUser(ctx, "John Doe", "john@example.com", "password123")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	if user.Name != "John Doe" {
		t.Errorf("Expected name 'John Doe', got '%s'", user.Name)
	}
	
	if user.Email != "john@example.com" {
		t.Errorf("Expected email 'john@example.com', got '%s'", user.Email)
	}
}

func TestGetUser(t *testing.T) {
	repo := newMockUserRepo()
	log := logger.New("error")
	svc := &UserService{repo: repo, logger: log}
	
	ctx := context.Background()
	
	created, _ := svc.CreateUser(ctx, "Jane Doe", "jane@example.com", "password123")
	
	fetched, err := svc.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	if fetched.ID != created.ID {
		t.Errorf("Expected ID '%s', got '%s'", created.ID, fetched.ID)
	}
}

func TestDeleteUser(t *testing.T) {
	repo := newMockUserRepo()
	log := logger.New("error")
	svc := &UserService{repo: repo, logger: log}
	
	ctx := context.Background()
	
	user, _ := svc.CreateUser(ctx, "Test User", "test@example.com", "password123")
	
	err := svc.DeleteUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	_, err = svc.GetUser(ctx, user.ID)
	if err != ErrUserNotFound {
		t.Errorf("Expected ErrUserNotFound, got %v", err)
	}
}

func TestCreateOrder(t *testing.T) {
	orderRepo := newMockOrderRepo()
	userRepo := newMockUserRepo()
	userLog := logger.New("error")
	userSvc := &UserService{repo: userRepo, logger: userLog}
	orderLog := logger.New("error")
	svc := NewOrderService(orderRepo, userSvc, orderLog)
	
	ctx := context.Background()
	
	user, _ := userSvc.CreateUser(ctx, "Buyer", "buyer@example.com", "password123")
	
	order, err := svc.CreateOrder(ctx, user.ID, "Widget", 2, 29.99)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	if order.Product != "Widget" {
		t.Errorf("Expected product 'Widget', got '%s'", order.Product)
	}
	
	if order.Status != models.OrderStatusPending {
		t.Errorf("Expected status 'pending', got '%s'", order.Status)
	}
}

func TestCancelOrder(t *testing.T) {
	orderRepo := newMockOrderRepo()
	userRepo := newMockUserRepo()
	userLog := logger.New("error")
	userSvc := &UserService{repo: userRepo, logger: userLog}
	orderLog := logger.New("error")
	svc := NewOrderService(orderRepo, userSvc, orderLog)
	
	ctx := context.Background()
	
	user, _ := userSvc.CreateUser(ctx, "Buyer", "buyer@example.com", "password123")
	order, _ := svc.CreateOrder(ctx, user.ID, "Widget", 1, 19.99)
	
	err := svc.CancelOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	
	updated, _ := svc.GetOrder(ctx, order.ID)
	if updated.Status != models.OrderStatusCancelled {
		t.Errorf("Expected status 'cancelled', got '%s'", updated.Status)
	}
}

type mockOrderRepo struct {
	orders map[string]*models.Order
}

func newMockOrderRepo() *mockOrderRepo {
	return &mockOrderRepo{orders: make(map[string]*models.Order)}
}

func (m *mockOrderRepo) List(ctx context.Context, userID string, limit, offset int) ([]*models.Order, error) {
	var result []*models.Order
	for _, order := range m.orders {
		if userID == "" || order.UserID == userID {
			result = append(result, order)
		}
	}
	return result, nil
}

func (m *mockOrderRepo) GetByID(ctx context.Context, id string) (*models.Order, error) {
	if order, ok := m.orders[id]; ok {
		return order, nil
	}
	return nil, ErrOrderNotFound
}

func (m *mockOrderRepo) GetByUserID(ctx context.Context, userID string) ([]*models.Order, error) {
	var result []*models.Order
	for _, order := range m.orders {
		if order.UserID == userID {
			result = append(result, order)
		}
	}
	return result, nil
}

func (m *mockOrderRepo) Create(ctx context.Context, order *models.Order) error {
	m.orders[order.ID] = order
	return nil
}

func (m *mockOrderRepo) Update(ctx context.Context, order *models.Order) error {
	m.orders[order.ID] = order
	return nil
}

func (m *mockOrderRepo) Delete(ctx context.Context, id string) error {
	delete(m.orders, id)
	return nil
}

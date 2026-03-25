package models

import (
	"errors"
	"time"
)

type Order struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Product    string    `json:"product"`
	Quantity   int       `json:"quantity"`
	TotalPrice float64   `json:"total_price"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateOrderRequest struct {
	UserID     string  `json:"user_id"`
	Product    string  `json:"product"`
	Quantity   int     `json:"quantity"`
	TotalPrice float64 `json:"total_price"`
}

type OrderRepository interface {
	List(userID string, limit, offset int) ([]*Order, error)
	GetByID(id string) (*Order, error)
	GetByUserID(userID string) ([]*Order, error)
	Create(order *Order) error
	Update(order *Order) error
	Delete(id string) error
}

const (
	OrderStatusPending   = "pending"
	OrderStatusConfirmed = "confirmed"
	OrderStatusShipped   = "shipped"
	OrderStatusDelivered = "delivered"
	OrderStatusCancelled = "cancelled"
)

func (o *Order) Validate() error {
	if o.UserID == "" {
		return errors.New("user_id is required")
	}
	if o.Product == "" {
		return errors.New("product is required")
	}
	if o.Quantity <= 0 {
		return errors.New("quantity must be positive")
	}
	if o.TotalPrice <= 0 {
		return errors.New("total_price must be positive")
	}
	if !isValidOrderStatus(o.Status) {
		return errors.New("invalid order status")
	}
	return nil
}

func isValidOrderStatus(status string) bool {
	validStatuses := []string{
		OrderStatusPending,
		OrderStatusConfirmed,
		OrderStatusShipped,
		OrderStatusDelivered,
		OrderStatusCancelled,
	}
	
	for _, s := range validStatuses {
		if s == status {
			return true
		}
	}
	return false
}

func (o *Order) CalculateTotal() float64 {
	return o.TotalPrice
}

func (o *Order) CanCancel() bool {
	return o.Status == OrderStatusPending || o.Status == OrderStatusConfirmed
}

func (o *Order) Cancel() error {
	if !o.CanCancel() {
		return errors.New("order cannot be cancelled in current status")
	}
	o.Status = OrderStatusCancelled
	o.UpdatedAt = time.Now()
	return nil
}

package models

import (
	"testing"
)

func TestUserValidation(t *testing.T) {
	tests := []struct {
		name    string
		user    User
		wantErr bool
	}{
		{
			name: "valid user",
			user: User{
				Name:     "John Doe",
				Email:    "john@example.com",
				Password: "password123",
			},
			wantErr: false,
		},
		{
			name: "empty name",
			user: User{
				Name:     "",
				Email:    "john@example.com",
				Password: "password123",
			},
			wantErr: true,
		},
		{
			name: "empty email",
			user: User{
				Name:     "John Doe",
				Email:    "",
				Password: "password123",
			},
			wantErr: true,
		},
		{
			name: "invalid email",
			user: User{
				Name:     "John Doe",
				Email:    "invalid-email",
				Password: "password123",
			},
			wantErr: true,
		},
		{
			name: "short password",
			user: User{
				Name:     "John Doe",
				Email:    "john@example.com",
				Password: "short",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.user.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOrderValidation(t *testing.T) {
	tests := []struct {
		name    string
		order   Order
		wantErr bool
	}{
		{
			name: "valid order",
			order: Order{
				UserID:     "user123",
				Product:    "Widget",
				Quantity:   2,
				TotalPrice: 29.99,
				Status:     OrderStatusPending,
			},
			wantErr: false,
		},
		{
			name: "empty user_id",
			order: Order{
				UserID:     "",
				Product:    "Widget",
				Quantity:   2,
				TotalPrice: 29.99,
				Status:     OrderStatusPending,
			},
			wantErr: true,
		},
		{
			name: "zero quantity",
			order: Order{
				UserID:     "user123",
				Product:    "Widget",
				Quantity:   0,
				TotalPrice: 29.99,
				Status:     OrderStatusPending,
			},
			wantErr: true,
		},
		{
			name: "negative price",
			order: Order{
				UserID:     "user123",
				Product:    "Widget",
				Quantity:   2,
				TotalPrice: -5.00,
				Status:     OrderStatusPending,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.order.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOrderCancel(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		canCancel  bool
	}{
		{"pending can cancel", OrderStatusPending, true},
		{"confirmed can cancel", OrderStatusConfirmed, true},
		{"shipped cannot cancel", OrderStatusShipped, false},
		{"delivered cannot cancel", OrderStatusDelivered, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &Order{Status: tt.status}
			if order.CanCancel() != tt.canCancel {
				t.Errorf("CanCancel() = %v, want %v", order.CanCancel(), tt.canCancel)
			}
		})
	}
}

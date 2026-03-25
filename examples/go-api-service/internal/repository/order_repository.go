package repository

import (
	"context"
	"database/sql"
	"fmt"

	"go-api-service/internal/models"
)

type OrderRepository struct {
	db *sql.DB
}

func NewOrderRepository(dbURL string) *OrderRepository {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}
	return &OrderRepository{db: db}
}

func (r *OrderRepository) List(ctx context.Context, userID string, limit, offset int) ([]*models.Order, error) {
	var query string
	var args []interface{}
	
	if userID != "" {
		query = `SELECT id, user_id, product, quantity, total_price, status, created_at, updated_at 
				 FROM orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = []interface{}{userID, limit, offset}
	} else {
		query = `SELECT id, user_id, product, quantity, total_price, status, created_at, updated_at 
				 FROM orders ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = []interface{}{limit, offset}
	}
	
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var orders []*models.Order
	for rows.Next() {
		order := &models.Order{}
		err := rows.Scan(
			&order.ID, &order.UserID, &order.Product, &order.Quantity,
			&order.TotalPrice, &order.Status, &order.CreatedAt, &order.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	
	return orders, rows.Err()
}

func (r *OrderRepository) GetByID(ctx context.Context, id string) (*models.Order, error) {
	query := `SELECT id, user_id, product, quantity, total_price, status, created_at, updated_at 
			  FROM orders WHERE id = $1`
	
	order := &models.Order{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&order.ID, &order.UserID, &order.Product, &order.Quantity,
		&order.TotalPrice, &order.Status, &order.CreatedAt, &order.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("order not found")
		}
		return nil, err
	}
	
	return order, nil
}

func (r *OrderRepository) GetByUserID(ctx context.Context, userID string) ([]*models.Order, error) {
	query := `SELECT id, user_id, product, quantity, total_price, status, created_at, updated_at 
			  FROM orders WHERE user_id = $1 ORDER BY created_at DESC`
	
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var orders []*models.Order
	for rows.Next() {
		order := &models.Order{}
		err := rows.Scan(
			&order.ID, &order.UserID, &order.Product, &order.Quantity,
			&order.TotalPrice, &order.Status, &order.CreatedAt, &order.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	
	return orders, rows.Err()
}

func (r *OrderRepository) Create(ctx context.Context, order *models.Order) error {
	query := `INSERT INTO orders (id, user_id, product, quantity, total_price, status, created_at, updated_at) 
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	
	_, err := r.db.ExecContext(ctx, query,
		order.ID, order.UserID, order.Product, order.Quantity,
		order.TotalPrice, order.Status, order.CreatedAt, order.UpdatedAt,
	)
	return err
}

func (r *OrderRepository) Update(ctx context.Context, order *models.Order) error {
	query := `UPDATE orders SET product = $1, quantity = $2, total_price = $3, status = $4, updated_at = $5 
			  WHERE id = $6`
	
	_, err := r.db.ExecContext(ctx, query,
		order.Product, order.Quantity, order.TotalPrice, order.Status, order.UpdatedAt, order.ID,
	)
	return err
}

func (r *OrderRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM orders WHERE id = $1`
	
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

func (r *OrderRepository) Close() error {
	return r.db.Close()
}

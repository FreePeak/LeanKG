package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go-api-service/internal/models"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(dbURL string) *UserRepository {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}
	return &UserRepository{db: db}
}

func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]*models.User, error) {
	query := `SELECT id, name, email, password, created_at, updated_at 
			  FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var users []*models.User
	for rows.Next() {
		user := &models.User{}
		err := rows.Scan(&user.ID, &user.Name, &user.Email, &user.Password, &user.CreatedAt, &user.UpdatedAt)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	
	return users, rows.Err()
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*models.User, error) {
	query := `SELECT id, name, email, password, created_at, updated_at 
			  FROM users WHERE id = $1`
	
	user := &models.User{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Name, &user.Email, &user.Password, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	
	return user, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	query := `SELECT id, name, email, password, created_at, updated_at 
			  FROM users WHERE email = $1`
	
	user := &models.User{}
	err := r.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.Name, &user.Email, &user.Password, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, err
	}
	
	return user, nil
}

func (r *UserRepository) Create(ctx context.Context, user *models.User) error {
	query := `INSERT INTO users (id, name, email, password, created_at, updated_at) 
			  VALUES ($1, $2, $3, $4, $5, $6)`
	
	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.Name, user.Email, user.Password, user.CreatedAt, user.UpdatedAt,
	)
	return err
}

func (r *UserRepository) Update(ctx context.Context, user *models.User) error {
	query := `UPDATE users SET name = $1, email = $2, updated_at = $3 WHERE id = $4`
	
	_, err := r.db.ExecContext(ctx, query, user.Name, user.Email, user.UpdatedAt, user.ID)
	return err
}

func (r *UserRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = $1`
	
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

func (r *UserRepository) Close() error {
	return r.db.Close()
}

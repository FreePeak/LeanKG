package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"go-api-service/internal/middleware"
	"go-api-service/internal/services"
	"go-api-service/pkg/logger"
)

type Handler struct {
	userService  *services.UserService
	orderService *services.OrderService
	logger       logger.Logger
}

func NewHandler(userSvc *services.UserService, orderSvc *services.OrderService, log logger.Logger) *Handler {
	return &Handler{
		userService:  userSvc,
		orderService: orderSvc,
		logger:       log,
	}
}

func SetupRouter(h *Handler, cfg interface{}) *http.ServeMux {
	mux := http.NewServeMux()
	
	h.registerRoutes(mux)
	
	return mux
}

func (h *Handler) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.healthCheck)
	mux.HandleFunc("GET /api/v1/users", h.listUsers)
	mux.HandleFunc("GET /api/v1/users/{id}", h.getUser)
	mux.HandleFunc("POST /api/v1/users", h.createUser)
	mux.HandleFunc("PUT /api/v1/users/{id}", h.updateUser)
	mux.HandleFunc("DELETE /api/v1/users/{id}", h.deleteUser)
	mux.HandleFunc("GET /api/v1/orders", h.listOrders)
	mux.HandleFunc("GET /api/v1/orders/{id}", h.getOrder)
	mux.HandleFunc("POST /api/v1/orders", h.createOrder)
}

func (h *Handler) healthCheck(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "go-api-service",
	}
	
	h.writeJSON(w, http.StatusOK, response)
}

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	limit := h.parseIntParam(r.URL.Query().Get("limit"), 50)
	offset := h.parseIntParam(r.URL.Query().Get("offset"), 0)
	
	users, err := h.userService.ListUsers(ctx, limit, offset)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":   users,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	id := h.getPathParam(r.URL.Path, "id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing user id")
		return
	}
	
	user, err := h.userService.GetUser(ctx, id)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusOK, user)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	var input struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	user, err := h.userService.CreateUser(ctx, input.Name, input.Email, input.Password)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	id := h.getPathParam(r.URL.Path, "id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing user id")
		return
	}
	
	var input struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	user, err := h.userService.UpdateUser(ctx, id, input.Name, input.Email)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusOK, user)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	id := h.getPathParam(r.URL.Path, "id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing user id")
		return
	}
	
	if err := h.userService.DeleteUser(ctx, id); err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusNoContent, nil)
}

func (h *Handler) listOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	userID := r.URL.Query().Get("user_id")
	limit := h.parseIntParam(r.URL.Query().Get("limit"), 50)
	offset := h.parseIntParam(r.URL.Query().Get("offset"), 0)
	
	orders, err := h.orderService.ListOrders(ctx, userID, limit, offset)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":   orders,
		"limit":  limit,
		"offset": offset,
	})
}

func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	id := h.getPathParam(r.URL.Path, "id")
	if id == "" {
		h.writeError(w, http.StatusBadRequest, "missing order id")
		return
	}
	
	order, err := h.orderService.GetOrder(ctx, id)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusOK, order)
}

func (h *Handler) createOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	var input struct {
		UserID    string  `json:"user_id"`
		Product   string  `json:"product"`
		Quantity  int     `json:"quantity"`
		TotalPrice float64 `json:"total_price"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	order, err := h.orderService.CreateOrder(ctx, input.UserID, input.Product, input.Quantity, input.TotalPrice)
	if err != nil {
		h.handleError(w, r, err)
		return
	}
	
	h.writeJSON(w, http.StatusCreated, order)
}

func (h *Handler) parseIntParam(value string, defaultValue int) int {
	if value == "" {
		return defaultValue
	}
	i, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return i
}

func (h *Handler) getPathParam(path, key string) string {
	return ""
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) handleError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("Request error: " + err.Error())
	
	status := http.StatusInternalServerError
	if err == middleware.ErrUnauthorized {
		status = http.StatusUnauthorized
	} else if err == middleware.ErrForbidden {
		status = http.StatusForbidden
	}
	
	h.writeError(w, status, err.Error())
}

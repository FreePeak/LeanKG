package com.example.controller;

import com.example.model.Order;
import com.example.model.User;
import com.example.service.OrderService;
import com.example.service.UserService;
import java.util.List;
import java.util.Optional;

/**
 * API controller handling HTTP-like request routing.
 * Demonstrates controller → service → model call chain.
 */
public class ApiController {

    private final UserService userService;
    private final OrderService orderService;

    public ApiController(UserService userService, OrderService orderService) {
        this.userService = userService;
        this.orderService = orderService;
    }

    public String handleHealthCheck() {
        return "{\"status\": \"ok\"}";
    }

    public User handleCreateUser(String name, String email) {
        return userService.createUser(name, email);
    }

    public Optional<User> handleGetUser(String id) {
        return userService.findById(id);
    }

    public Order handleCreateOrder(String userId, double amount) {
        return orderService.createOrder(userId, amount);
    }

    public List<Order> handleGetUserOrders(String userId) {
        return orderService.getOrdersByUser(userId);
    }

    public void handleConfirmOrder(String orderId) {
        orderService.confirmOrder(orderId);
    }

    public void handleCancelOrder(String orderId) {
        orderService.cancelOrder(orderId);
    }
}

package com.example.service;

import com.example.model.Order;
import com.example.model.User;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;

/**
 * Service for managing orders.
 * Depends on UserService for user validation.
 */
public class OrderService {

    private final UserService userService;
    private final List<Order> orders = new ArrayList<>();

    public OrderService(UserService userService) {
        this.userService = userService;
    }

    public Order createOrder(String userId, double amount) {
        Optional<User> user = userService.findById(userId);
        if (user.isEmpty()) {
            throw new IllegalArgumentException("User not found: " + userId);
        }

        Order order = new Order(userId, amount);
        orders.add(order);
        return order;
    }

    public void confirmOrder(String orderId) {
        Order order = findOrderById(orderId);
        order.confirm();
    }

    public void cancelOrder(String orderId) {
        Order order = findOrderById(orderId);
        order.cancel();
    }

    public List<Order> getOrdersByUser(String userId) {
        List<Order> result = new ArrayList<>();
        for (Order order : orders) {
            if (order.getUserId().equals(userId)) {
                result.add(order);
            }
        }
        return result;
    }

    private Order findOrderById(String orderId) {
        for (Order order : orders) {
            if (order.getId().equals(orderId)) {
                return order;
            }
        }
        throw new IllegalArgumentException("Order not found: " + orderId);
    }
}

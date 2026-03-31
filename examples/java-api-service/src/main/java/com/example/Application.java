package com.example;

import com.example.controller.ApiController;
import com.example.service.UserService;
import com.example.service.OrderService;

/**
 * Main application entry point.
 * Bootstraps the API service with dependency injection.
 */
public class Application {

    private final ApiController controller;

    public Application() {
        UserService userService = new UserService();
        OrderService orderService = new OrderService(userService);
        this.controller = new ApiController(userService, orderService);
    }

    public void start() {
        System.out.println("Starting Java API Service...");
        controller.handleHealthCheck();
    }

    public static void main(String[] args) {
        Application app = new Application();
        app.start();
    }
}

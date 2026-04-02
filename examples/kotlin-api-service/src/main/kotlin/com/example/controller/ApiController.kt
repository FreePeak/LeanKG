package com.example.controller

import com.example.service.UserService
import com.example.service.OrderService

class ApiController {
    private val userService = UserService()
    private val orderService = OrderService(userService)

    fun handleGetUser(userId: String): String {
        val user = userService.findById(userId)
        return user?.displayName() ?: "User not found"
    }

    fun handleCreateOrder(userId: String, itemId: String): String {
        val order = orderService.createOrder(userId, itemId)
        return order?.let { "Order ${it.id} created" } ?: "Failed to create order"
    }

    fun handleListOrders(userId: String): List<String> {
        return orderService.findByUser(userId).map { "${it.id}: ${it.status}" }
    }
}

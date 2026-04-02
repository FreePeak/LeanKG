package com.example.service

import com.example.model.Order
import com.example.model.OrderStatus

class OrderService(private val userService: UserService) {

    private val orders = mutableListOf<Order>()

    fun createOrder(userId: String, itemId: String): Order? {
        val user = userService.findById(userId) ?: return null
        val order = Order.create(userId, itemId)
        orders.add(order)
        println("Order created for ${user.displayName()}")
        return order
    }

    fun completeOrder(orderId: String): Order? {
        val index = orders.indexOfFirst { it.id == orderId }
        if (index < 0) return null
        val updated = orders[index].complete()
        orders[index] = updated
        return updated
    }

    fun findByUser(userId: String): List<Order> {
        return orders.filter { it.userId == userId }
    }

    fun pendingCount(): Int {
        return orders.count { it.isPending() }
    }
}

package com.example.model

enum class OrderStatus {
    PENDING,
    PROCESSING,
    COMPLETED,
    CANCELLED;

    fun isTerminal(): Boolean = this == COMPLETED || this == CANCELLED
}

package com.example.model

data class Order(
    val id: String,
    val userId: String,
    val itemId: String,
    val status: OrderStatus = OrderStatus.PENDING
) {
    constructor(id: String, userId: String) : this(id, userId, "", OrderStatus.PENDING)

    fun isPending(): Boolean = status == OrderStatus.PENDING

    fun complete(): Order = copy(status = OrderStatus.COMPLETED)

    companion object Factory {
        fun create(userId: String, itemId: String): Order {
            val id = "order-${System.currentTimeMillis()}"
            return Order(id, userId, itemId)
        }
    }
}

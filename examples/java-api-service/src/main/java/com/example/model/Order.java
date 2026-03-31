package com.example.model;

/**
 * Order model representing a customer order.
 */
public class Order {

    private String id;
    private String userId;
    private double amount;
    private OrderStatus status;

    public Order(String userId, double amount) {
        this.id = "order-" + System.currentTimeMillis();
        this.userId = userId;
        this.amount = amount;
        this.status = OrderStatus.PENDING;
    }

    public String getId() {
        return id;
    }

    public String getUserId() {
        return userId;
    }

    public double getAmount() {
        return amount;
    }

    public OrderStatus getStatus() {
        return status;
    }

    public void setStatus(OrderStatus status) {
        this.status = status;
    }

    public boolean canCancel() {
        return status == OrderStatus.PENDING;
    }

    public void confirm() {
        if (status != OrderStatus.PENDING) {
            throw new IllegalStateException("Can only confirm pending orders");
        }
        this.status = OrderStatus.CONFIRMED;
    }

    public void cancel() {
        if (!canCancel()) {
            throw new IllegalStateException("Cannot cancel non-pending orders");
        }
        this.status = OrderStatus.CANCELLED;
    }

    @Override
    public String toString() {
        return "Order{id='" + id + "', userId='" + userId
            + "', amount=" + amount + ", status=" + status + "}";
    }
}

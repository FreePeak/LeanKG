package com.example.util

object Validator {
    fun validateEmail(email: String) {
        require(email.contains("@")) { "Invalid email: $email" }
    }

    fun validateNotBlank(value: String, fieldName: String) {
        require(value.isNotBlank()) { "$fieldName must not be blank" }
    }

    fun validateId(id: String) {
        require(id.startsWith("user-") || id.startsWith("order-")) {
            "Invalid ID format: $id"
        }
    }
}

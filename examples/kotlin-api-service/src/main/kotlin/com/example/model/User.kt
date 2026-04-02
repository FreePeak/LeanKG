package com.example.model

data class User(
    val id: String,
    val name: String,
    val email: String
) {
    fun displayName(): String {
        return "$name <$email>"
    }

    fun isValid(): Boolean {
        return name.isNotBlank() && email.contains("@")
    }
}

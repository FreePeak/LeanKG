package com.example.service

import com.example.model.User
import com.example.util.Validator

class UserService {
    private val users = mutableMapOf<String, User>()

    fun findById(id: String): User? {
        return users[id]
    }

    fun createUser(name: String, email: String): User {
        Validator.validateEmail(email)
        val id = "user-${users.size + 1}"
        val user = User(id, name, email)
        users[id] = user
        return user
    }

    fun listAll(): List<User> {
        return users.values.toList()
    }

    fun deleteUser(id: String): Boolean {
        return users.remove(id) != null
    }
}

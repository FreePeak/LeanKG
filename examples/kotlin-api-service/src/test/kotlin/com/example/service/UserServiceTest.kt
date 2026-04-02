package com.example.service

import com.example.model.User

class UserServiceTest {
    fun testCreateUser() {
        val service = UserService()
        val user = service.createUser("Alice", "alice@example.com")
        assert(user.name == "Alice")
        assert(user.isValid())
    }

    fun testFindById() {
        val service = UserService()
        val user = service.createUser("Bob", "bob@example.com")
        val found = service.findById(user.id)
        assert(found != null)
        assert(found?.name == "Bob")
    }

    fun testDeleteUser() {
        val service = UserService()
        val user = service.createUser("Eve", "eve@example.com")
        val deleted = service.deleteUser(user.id)
        assert(deleted)
        assert(service.findById(user.id) == null)
    }
}

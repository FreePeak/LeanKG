package com.example.service;

import com.example.model.User;

/**
 * Unit tests for UserService.
 * This file demonstrates LeanKG's Java test file detection.
 */
public class UserServiceTest {

    private UserService userService;

    public void setUp() {
        userService = new UserService();
    }

    public void testCreateUser() {
        User user = userService.createUser("Alice", "alice@example.com");
        assert user != null : "User should not be null";
        assert "Alice".equals(user.getName()) : "Name should be Alice";
        assert "alice@example.com".equals(user.getEmail()) : "Email should match";
    }

    public void testFindById() {
        User created = userService.createUser("Bob", "bob@example.com");
        assert userService.findById(created.getId()).isPresent() : "Should find user";
        assert userService.findById("nonexistent").isEmpty() : "Should not find missing user";
    }

    public void testDeleteUser() {
        User user = userService.createUser("Charlie", "charlie@example.com");
        assert userService.deleteUser(user.getId()) : "Should delete existing user";
        assert !userService.deleteUser("nonexistent") : "Should not delete missing user";
    }

    public void testUpdateUser() {
        User user = userService.createUser("Dave", "dave@example.com");
        User updated = userService.updateUser(user.getId(), "David", null);
        assert "David".equals(updated.getName()) : "Name should be updated";
        assert "dave@example.com".equals(updated.getEmail()) : "Email should be unchanged";
    }

    public void testGetUserCount() {
        assert userService.getUserCount() == 0 : "Should start empty";
        userService.createUser("Eve", "eve@example.com");
        assert userService.getUserCount() == 1 : "Should have one user";
    }
}

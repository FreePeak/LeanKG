package com.example.service;

import com.example.model.User;
import com.example.util.Validator;
import java.util.HashMap;
import java.util.Map;
import java.util.Optional;

/**
 * Service for managing users.
 */
public class UserService {

    private final Map<String, User> users = new HashMap<>();

    public User createUser(String name, String email) {
        Validator.requireNonEmpty(name, "Name");
        Validator.requireValidEmail(email);

        User user = new User(name, email);
        users.put(user.getId(), user);
        return user;
    }

    public Optional<User> findById(String id) {
        return Optional.ofNullable(users.get(id));
    }

    public boolean deleteUser(String id) {
        return users.remove(id) != null;
    }

    public User updateUser(String id, String name, String email) {
        User user = users.get(id);
        if (user == null) {
            throw new IllegalArgumentException("User not found: " + id);
        }
        if (name != null) {
            user.setName(name);
        }
        if (email != null) {
            Validator.requireValidEmail(email);
            user.setEmail(email);
        }
        return user;
    }

    public int getUserCount() {
        return users.size();
    }
}

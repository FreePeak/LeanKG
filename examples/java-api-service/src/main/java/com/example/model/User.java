package com.example.model;

/**
 * User model representing a system user.
 */
public class User {

    private String id;
    private String name;
    private String email;

    public User(String name, String email) {
        this.id = generateId();
        this.name = name;
        this.email = email;
    }

    public String getId() {
        return id;
    }

    public String getName() {
        return name;
    }

    public void setName(String name) {
        this.name = name;
    }

    public String getEmail() {
        return email;
    }

    public void setEmail(String email) {
        this.email = email;
    }

    public boolean isValid() {
        return name != null && !name.isEmpty()
            && email != null && email.contains("@");
    }

    private String generateId() {
        return "user-" + System.currentTimeMillis();
    }

    @Override
    public String toString() {
        return "User{id='" + id + "', name='" + name + "', email='" + email + "'}";
    }
}

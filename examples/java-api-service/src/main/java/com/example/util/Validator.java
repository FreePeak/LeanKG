package com.example.util;

/**
 * Utility class for input validation.
 */
public class Validator {

    private Validator() {
        // Prevent instantiation
    }

    public static void requireNonEmpty(String value, String fieldName) {
        if (value == null || value.trim().isEmpty()) {
            throw new IllegalArgumentException(fieldName + " must not be empty");
        }
    }

    public static void requireValidEmail(String email) {
        requireNonEmpty(email, "Email");
        if (!email.contains("@") || !email.contains(".")) {
            throw new IllegalArgumentException("Invalid email format: " + email);
        }
    }

    public static void requirePositive(double value, String fieldName) {
        if (value <= 0) {
            throw new IllegalArgumentException(fieldName + " must be positive");
        }
    }

    public static boolean isValidId(String id) {
        return id != null && !id.trim().isEmpty() && id.length() >= 3;
    }
}

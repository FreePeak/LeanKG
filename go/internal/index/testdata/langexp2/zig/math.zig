const std = @import("std");

pub fn add(a: i32, b: i32) i32 {
    return a + b;
}

const Point = struct {
    x: i32,
    y: i32,
};

test "add works" {
    try std.testing.expect(add(1, 2) == 3);
}

const std = @import("std");

pub const Queue = struct {
    items: std.ArrayList(u32),

    pub fn init(allocator: std.mem.Allocator) Queue {
        return .{ .items = std.ArrayList(u32).init(allocator) };
    }

    pub fn push(self: *Queue, value: u32) void {
        self.items.append(value) catch {};
    }
};

pub const Kind = enum { fifo, lifo };

test "queue push" {
    var q = Queue.init(std.testing.allocator);
    defer q.items.deinit();
    q.push(1);
}

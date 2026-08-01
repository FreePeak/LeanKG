#import "Greeter.h"

@interface Greeter (Private)
- (void)internalSetup;
@end

@implementation Greeter

- (void)setup {
    self.name = @"world";
}

- (void)sayHello {
    [self setup];
    [self log:@"Hello" level:1];
    NSLog(@"Hello, %@", self.name);
}

- (void)setName:(NSString *)name age:(NSInteger)age {
    self.name = name;
    [self log:name level:age];
}

- (void)internalSetup {
    [self setup];
}

- (NSString *)greeting {
    return [NSString stringWithFormat:@"Hello, %@", self.name];
}

- (void)greetWithName:(NSString *)name {
    self.name = name;
    [self sayHello];
}

- (void)log:(NSString *)message level:(NSInteger)level {
    NSLog(@"[%ld] %@", (long)level, message);
}

+ (instancetype)sharedGreeter {
    static Greeter *shared = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        shared = [[self alloc] init];
        [shared setup];
    });
    return shared;
}

@end

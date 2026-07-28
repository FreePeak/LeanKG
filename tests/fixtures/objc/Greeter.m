#import "Greeter.h"

@interface Greeter (Private)
- (void)internalSetup;
@end

@implementation Greeter
- (void)sayHello {
    NSLog(@"Hello, %@", self.name);
}

- (void)internalSetup {
    self.name = @"world";
}

+ (instancetype)sharedGreeter {
    static Greeter *shared = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        shared = [[self alloc] init];
    });
    return shared;
}
@end

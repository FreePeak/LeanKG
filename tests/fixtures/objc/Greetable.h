#import <Foundation/Foundation.h>

@protocol Greetable
- (NSString *)greeting;
- (void)greetWithName:(NSString *)name;
@end

@protocol Logging
- (void)log:(NSString *)message level:(NSInteger)level;
@end

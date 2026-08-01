#import <Foundation/Foundation.h>
#import "Greetable.h"

@interface Greeter : NSObject <Greetable, Logging>
@property (nonatomic, strong) NSString *name;
- (void)sayHello;
- (void)setName:(NSString *)name age:(NSInteger)age;
- (void)setup;
- (NSString *)greeting;
- (void)greetWithName:(NSString *)name;
- (void)log:(NSString *)message level:(NSInteger)level;
+ (instancetype)sharedGreeter;
@end

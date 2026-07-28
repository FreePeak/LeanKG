#import <Foundation/Foundation.h>

@interface Greeter : NSObject
@property (nonatomic, strong) NSString *name;
- (void)sayHello;
+ (instancetype)sharedGreeter;
@end

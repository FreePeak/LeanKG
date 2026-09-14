#include <vector>
#include "shape.h"

using namespace std;

namespace geo {
  class Shape {
  public:
    virtual double area() = 0;
  };

  struct Circle : Shape {
    double r;
  };
}

using geo::Circle;

double area_of(const Circle& c) {
  return 3.14 * c.r;
}

using System;
using ExampleLib;

namespace ExampleLib.Tests {
    public class CalculatorTests {
        public void TestAdd() {
            var calc = new Calculator();
            if (calc.Add(2, 3) != 5) {
                throw new Exception("Assertion Failed");
            }
        }
    }
}

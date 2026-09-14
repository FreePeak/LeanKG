module alu (
    input  logic [7:0] a,
    input  logic [7:0] b,
    output logic [7:0] y
);
    function automatic logic [7:0] bitwise_and(input logic [7:0] x, input logic [7:0] y);
        return x & y;
    endfunction

    task automatic reset();
        y = '0;
    endtask

    always_comb begin
        y = bitwise_and(a, b);
    end
endmodule

virtual class base_driver;
    pure virtual task drive();
endclass

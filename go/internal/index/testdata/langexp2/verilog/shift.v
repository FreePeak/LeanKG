module shift_register #(parameter WIDTH = 8) (
    input wire clk,
    input wire rst,
    input wire din,
    output wire dout
);
    reg [WIDTH-1:0] shreg;

    always @(posedge clk) begin
        if (rst) shreg <= 0;
        else shreg <= {shreg[WIDTH-2:0], din};
    end

    assign dout = shreg[WIDTH-1];

    function [WIDTH-1:0] mask;
        begin
            mask = {WIDTH{1'b1}};
        end
    endfunction
endmodule

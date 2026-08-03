module counter(
    input wire clk,
    input wire reset,
    output reg [7:0] q
);
    always @(posedge clk or posedge reset) begin
        if (reset) q <= 8'b0;
        else q <= q + 1;
    end
endmodule
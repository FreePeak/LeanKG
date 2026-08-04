class packet;
    int length;
    bit [7:0] payload [];

    function int get_length();
        return length;
    endfunction

    function void set_length(int len);
        length = len;
    endfunction
endclass
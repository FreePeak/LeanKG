// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import {SafeMath} from "./lib/SafeMath.sol";

interface IReceiver {
    function receiveFunds() external payable;
}

library Fees {
    function compute(uint256 amount) internal pure returns (uint256) {
        return amount / 100;
    }
}

struct Deal {
    address buyer;
    uint256 price;
}

enum State { Open, Closed }

contract Escrow {
    State public state;

    constructor() payable {
        state = State.Open;
    }

    function release(address payable to) public {
        require(state == State.Open, "closed");
        to.transfer(address(this).balance);
    }
}

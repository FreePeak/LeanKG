#!/usr/bin/env python3
"""
Token counter for A/B benchmark results.
Uses tiktoken for accurate GPT token counting.
Falls back to word-based approximation if tiktoken unavailable.
"""

import sys
import os

try:
    import tiktoken

    ENCODER = tiktoken.get_encoding("cl100k_base")

    def count_tokens(text: str) -> int:
        if not text:
            return 0
        return len(ENCODER.encode(text))

except ImportError:

    def count_tokens(text: str) -> int:
        if not text:
            return 0
        words = len(text.split())
        return int(words * 1.3)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: count_tokens.py <text_file_or_string>")
        sys.exit(1)

    input_arg = sys.argv[1]

    if os.path.isfile(input_arg):
        with open(input_arg, "r") as f:
            text = f.read()
    else:
        text = input_arg

    tokens = count_tokens(text)
    print(tokens)
